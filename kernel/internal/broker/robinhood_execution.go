package broker

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/units"
)

// RobinhoodExecution is the live-only production mutation capability. It is
// constructed separately from the retrying read client and is never present in
// shadow or read-only modes.
type RobinhoodExecution struct {
	read         *Robinhood
	mutation     rhmcp.MutationCaller
	instruments  InstrumentReader
	allowOption  bool
	cancelDelays []time.Duration
}

// defaultCancelConfirmationDelays is the wait before each successive poll for a
// cancel to reach a terminal state. It starts at 1s because Robinhood does not
// reflect a cancel for a second or two; polling earlier is wasted and pushed the
// attempt into an unknown latch that stalled all live execution. Polls land at
// ~1s, 2s, 5s and 8s -- all four inside brokerCallTimeout with room for the
// mutation and reads; if that deadline fires first the last known state is
// returned, exactly as before.
var defaultCancelConfirmationDelays = []time.Duration{
	1 * time.Second,
	1 * time.Second,
	3 * time.Second,
	3 * time.Second,
}

func NewRobinhoodExecution(read *Robinhood, mutation rhmcp.MutationCaller, instruments InstrumentReader) (*RobinhoodExecution, error) {
	return newRobinhoodExecution(read, mutation, instruments, false)
}

func newRobinhoodExecution(read *Robinhood, mutation rhmcp.MutationCaller, instruments InstrumentReader, allowOption bool) (*RobinhoodExecution, error) {
	if read == nil || mutation == nil || instruments == nil {
		return nil, fmt.Errorf("Robinhood execution provider requires mutation and instrument capabilities")
	}
	return &RobinhoodExecution{
		read: read, mutation: mutation, instruments: instruments,
		allowOption: allowOption, cancelDelays: defaultCancelConfirmationDelays,
	}, nil
}

func (r *RobinhoodExecution) SupportsOrderKind(kind string) bool {
	return kind == "equity" || (kind == "option" && r.allowOption)
}

func (r *RobinhoodExecution) PlaceOrder(ctx context.Context, req PlaceRequest) (OrderResult, error) {
	if req.OrderType == "" {
		req.OrderType = "limit"
	}
	if !r.SupportsOrderKind(req.Kind) {
		return OrderResult{}, fmt.Errorf("provider order kind is not certified for live mutation")
	}
	if _, err := r.read.selectedAccount(ctx); err != nil {
		return OrderResult{}, err
	}
	instrument, err := r.validatePlaceRequest(ctx, req)
	if err != nil {
		return OrderResult{}, err
	}
	args := map[string]any{
		"account_number": r.read.accountID,
		"side":           req.Side,
		"type":           req.OrderType,
		"time_in_force":  "gfd",
		"market_hours":   "regular_hours",
		"ref_id":         req.ClientOrderID,
	}
	tool := "place_equity_order"
	if req.Kind == "equity" {
		args["symbol"] = req.Symbol
		args["quantity"] = req.Qty.String()
		if req.OrderType == "limit" || req.OrderType == "stop_limit" {
			args["limit_price"] = req.Limit.String()
		}
		if req.OrderType == "stop_market" || req.OrderType == "stop_limit" {
			args["stop_price"] = req.StopPrice.String()
		}
	} else {
		tool = "place_option_order"
		args["quantity"] = req.Qty.String()
		args["price"] = req.Limit.String()
		args["legs"] = []map[string]any{{
			"option_id": req.Symbol, "side": req.Side,
			"position_effect": req.PositionEffect, "ratio_quantity": 1,
		}}
	}
	raw, err := r.mutation.Call(ctx, tool, args)
	if err != nil {
		return OrderResult{}, err
	}
	if req.Kind == "equity" {
		var data struct {
			Order struct {
				ID string `json:"id"`
			} `json:"order"`
		}
		if err := decodeRobinhoodData(r.mutation, raw, &data); err != nil {
			return OrderResult{}, err
		}
		if !looksLikeCanonicalUUID(data.Order.ID) {
			return OrderResult{}, robinhoodSchemaError(r.mutation, "equity placement identity drift")
		}
		orders, err := r.readEquityOrder(ctx, data.Order.ID)
		if err != nil || len(orders) != 1 {
			return OrderResult{}, fmt.Errorf("canonical equity placement unavailable")
		}
		order := orders[0]
		// Robinhood omits price while a market order is working, then may
		// backfill it with the execution price once terminal. Price is therefore
		// result data, not part of a market-order intent match.
		providerType, providerTrigger := providerOrderShape(req.OrderType)
		priceMatches := providerType == "market"
		if providerType == "limit" {
			priceMatches = order.Price != nil && *order.Price == req.Limit
		}
		stopMatches := req.StopPrice == 0 && order.StopPrice.Present && !order.StopPrice.Known
		if req.StopPrice > 0 {
			stopMatches = order.StopPrice.Present && order.StopPrice.Known && order.StopPrice.Value == req.StopPrice
		}
		if order.Symbol != req.Symbol || order.InstrumentID != instrument.InstrumentID ||
			order.Side != req.Side || order.Quantity == nil || *order.Quantity != req.Qty ||
			order.DollarBasedAmount != nil || !priceMatches ||
			!stopMatches || order.Type != providerType ||
			order.Trigger != providerTrigger || order.TimeInForce != "gfd" ||
			order.MarketHours != "regular_hours" || order.PlacedAgent != "agentic" {
			return OrderResult{}, robinhoodSchemaError(r.mutation, "canonical equity placement drift")
		}
		result, err := normalizeEquityOrder(r.mutation, order)
		if err != nil {
			return OrderResult{}, err
		}
		result.ClientOrderID = req.ClientOrderID
		return result, nil
	}

	var data struct {
		Order optionReadOrder `json:"order"`
	}
	if err := decodeRobinhoodData(r.mutation, raw, &data); err != nil {
		return OrderResult{}, err
	}
	if data.Order.Quantity == nil || *data.Order.Quantity != req.Qty || !data.Order.Price.Known ||
		data.Order.Price.Value != req.Limit || len(data.Order.Legs) != 1 ||
		!strings.EqualFold(data.Order.Legs[0].OptionID, instrument.InstrumentID) ||
		data.Order.Legs[0].Side != req.Side || data.Order.Legs[0].PositionEffect != req.PositionEffect ||
		data.Order.Legs[0].RatioQuantity == nil || int64(*data.Order.Legs[0].RatioQuantity) != 1 {
		return OrderResult{}, robinhoodSchemaError(r.mutation, "option placement echo drift")
	}
	result, err := normalizeOptionOrder(r.mutation, data.Order)
	if err != nil {
		return OrderResult{}, err
	}
	result.ClientOrderID = req.ClientOrderID
	return result, nil
}

func (r *RobinhoodExecution) validatePlaceRequest(ctx context.Context, req PlaceRequest) (Instrument, error) {
	if !looksLikeCanonicalUUID(req.ClientOrderID) || strings.TrimSpace(req.Symbol) == "" ||
		(req.Side != "buy" && req.Side != "sell") || (req.PositionEffect != "open" && req.PositionEffect != "close") ||
		(req.Kind != "equity" && req.Kind != "option") || req.Qty <= 0 || req.Limit <= 0 ||
		(req.OrderType != "limit" && req.OrderType != "market" && req.OrderType != "stop_market" && req.OrderType != "stop_limit") ||
		(req.OrderType == "market" && (req.Kind != "equity" || req.Side != "buy" || req.PositionEffect != "open" ||
			req.Qty%units.Qty(units.Scale) != 0 || req.StopPrice != 0)) ||
		(req.OrderType == "limit" && req.StopPrice != 0) ||
		((req.OrderType == "stop_market" || req.OrderType == "stop_limit") &&
			(req.Kind != "equity" || req.Side != "buy" || req.PositionEffect != "open" ||
				req.Qty%units.Qty(units.Scale) != 0 || req.StopPrice <= 0)) ||
		(req.OrderType == "stop_limit" && req.Limit < req.StopPrice) {
		return Instrument{}, fmt.Errorf("invalid persisted order request")
	}
	instrument, err := r.instruments.Instrument(ctx, req.Symbol)
	if err != nil {
		return Instrument{}, fmt.Errorf("instrument metadata unavailable")
	}
	identityMatches := instrument.Symbol == req.Symbol
	if req.Kind == "option" {
		identityMatches = strings.EqualFold(instrument.InstrumentID, req.Symbol)
	}
	if !identityMatches || instrument.Kind != req.Kind || !instrument.PrecisionSane() ||
		!instrument.SupportsPrice(req.Limit) || (req.StopPrice > 0 && !instrument.SupportsPrice(req.StopPrice)) ||
		req.Qty%instrument.QtyIncrement != 0 ||
		(req.Kind == "equity" && instrument.Multiplier != 1) ||
		(req.Kind == "option" && instrument.Multiplier != 100) {
		return Instrument{}, fmt.Errorf("persisted order does not match provider instrument metadata")
	}
	return instrument, nil
}

func providerOrderShape(orderType string) (string, string) {
	switch orderType {
	case "limit":
		return "limit", "immediate"
	case "market":
		return "market", "immediate"
	case "stop_limit":
		return "limit", "stop"
	case "stop_market":
		return "market", "stop"
	default:
		return "", ""
	}
}

func (r *RobinhoodExecution) CancelOrder(ctx context.Context, brokerOrderID string) (OrderResult, error) {
	// Execution is constructed with the authority-only Robinhood reader. A
	// stale working state here can race a fill or duplicate a cancellation that
	// already reached the broker, so it cannot use the UI/read-model channel.
	current, kind, err := r.getOrder(ctx, brokerOrderID)
	if err != nil {
		return OrderResult{}, err
	}
	if current.State != "submitted" && current.State != "partially_filled" {
		return current, nil
	}
	if !r.SupportsOrderKind(kind) {
		return OrderResult{}, fmt.Errorf("provider order kind is not certified for live mutation")
	}
	tool := "cancel_equity_order"
	if kind == "option" {
		tool = "cancel_option_order"
	}
	raw, err := r.mutation.Call(ctx, tool, map[string]any{
		"account_number": r.read.accountID, "order_id": brokerOrderID,
	})
	if err != nil {
		return OrderResult{}, err
	}
	var data struct {
		Accepted *bool `json:"accepted"`
	}
	if err := decodeRobinhoodData(r.mutation, raw, &data); err != nil || data.Accepted == nil {
		return OrderResult{}, robinhoodSchemaError(r.mutation, "cancel response schema drift")
	}
	if !*data.Accepted {
		current.State = "rejected"
		current.Reason = "cancel rejected by provider"
		return current, nil
	}
	return r.confirmCancelState(ctx, brokerOrderID, kind, current)
}

func (r *RobinhoodExecution) confirmCancelState(ctx context.Context, brokerOrderID, kind string, last OrderResult) (OrderResult, error) {
	var lastErr error
	for _, delay := range r.cancelDelays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if last.State != "" {
					return last, nil
				}
				return OrderResult{}, fmt.Errorf("cancel final state unavailable")
			case <-timer.C:
			}
		}
		confirmed, err := r.readKnownOrder(ctx, brokerOrderID, kind)
		if err != nil {
			lastErr = err
			continue
		}
		last, lastErr = confirmed, nil
		if confirmed.State != "submitted" && confirmed.State != "partially_filled" {
			return confirmed, nil
		}
	}
	if last.State != "" {
		return last, nil
	}
	return OrderResult{}, fmt.Errorf("cancel final state unavailable: %w", lastErr)
}

func (r *RobinhoodExecution) readKnownOrder(ctx context.Context, brokerOrderID, kind string) (OrderResult, error) {
	switch kind {
	case "equity":
		orders, err := r.readEquityOrder(ctx, brokerOrderID)
		if err != nil {
			return OrderResult{}, err
		}
		if len(orders) != 1 {
			return OrderResult{}, ErrNotFound
		}
		return normalizeEquityOrder(r.read.caller, orders[0])
	case "option":
		orders, err := r.readOptionOrder(ctx, brokerOrderID)
		if err != nil {
			return OrderResult{}, err
		}
		if len(orders) != 1 {
			return OrderResult{}, ErrNotFound
		}
		return normalizeOptionOrder(r.read.caller, orders[0])
	default:
		return OrderResult{}, fmt.Errorf("cancel order kind is invalid")
	}
}

func (r *RobinhoodExecution) GetOrder(ctx context.Context, brokerOrderID string) (OrderResult, error) {
	result, _, err := r.getOrder(ctx, brokerOrderID)
	return result, err
}

func (r *RobinhoodExecution) FindExactPlaceCandidates(ctx context.Context, query ExactPlaceCandidateQuery) ([]OrderResult, error) {
	intent := query.Intent
	if query.AccountID != r.read.accountID || !looksLikeCanonicalUUID(query.ClientOrderID) ||
		query.WindowStart.IsZero() || query.WindowEnd.IsZero() || query.WindowEnd.Before(query.WindowStart) {
		return nil, fmt.Errorf("invalid exact candidate query")
	}
	if intent.Kind != "equity" {
		return nil, fmt.Errorf("exact option candidate recovery is not enabled")
	}
	if intent.InstrumentID == "" || strings.TrimSpace(intent.Symbol) == "" ||
		(intent.Side != "buy" && intent.Side != "sell") || intent.Qty <= 0 ||
		(intent.OrderType == "limit" && intent.Limit <= 0) ||
		(intent.OrderType == "market" && (intent.Limit != 0 || intent.Side != "buy")) ||
		(intent.OrderType != "limit" && intent.OrderType != "market") ||
		(intent.Trigger == "immediate" && intent.StopPrice != 0) ||
		(intent.Trigger == "stop" && intent.StopPrice <= 0) ||
		(intent.Trigger != "immediate" && intent.Trigger != "stop") ||
		intent.TimeInForce != "gfd" || intent.MarketHours != "regular_hours" {
		return nil, fmt.Errorf("unsupported exact equity candidate intent")
	}
	orders, err := readOrderPages[equityReadOrder](ctx, r.read, "get_equity_orders", map[string]any{
		"created_at_gte": query.WindowStart.UTC().Format(time.RFC3339Nano),
		"placed_agent":   "agentic",
		"symbol":         intent.Symbol,
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]OrderResult, 0, 1)
	for _, order := range orders {
		createdAt, parseErr := parseProviderTime(order.CreatedAt)
		if parseErr != nil || !order.StopPrice.Present || order.Type == "" || order.TimeInForce == "" ||
			order.MarketHours == "" || order.Trigger == "" || order.PlacedAgent == "" {
			return nil, robinhoodSchemaError(r.read.caller, "equity candidate schema drift")
		}
		priceMatches := intent.OrderType == "market"
		if intent.OrderType == "limit" {
			priceMatches = order.Price != nil && *order.Price == intent.Limit
		}
		stopMatches := intent.StopPrice == 0 && order.StopPrice.Present && !order.StopPrice.Known
		if intent.StopPrice > 0 {
			stopMatches = order.StopPrice.Present && order.StopPrice.Known && order.StopPrice.Value == intent.StopPrice
		}
		if createdAt.Before(query.WindowStart) || createdAt.After(query.WindowEnd) ||
			order.InstrumentID != intent.InstrumentID || order.Symbol != intent.Symbol || order.Side != intent.Side ||
			order.Quantity == nil || *order.Quantity != intent.Qty || order.DollarBasedAmount != nil ||
			!priceMatches || !stopMatches ||
			order.Type != intent.OrderType || order.Trigger != intent.Trigger ||
			order.TimeInForce != intent.TimeInForce || order.MarketHours != intent.MarketHours ||
			order.PlacedAgent != "agentic" {
			continue
		}
		result, normalizeErr := normalizeEquityOrder(r.read.caller, order)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		result.ClientOrderID = query.ClientOrderID
		candidates = append(candidates, result)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].BrokerOrderID < candidates[j].BrokerOrderID })
	return candidates, nil
}

func (r *RobinhoodExecution) getOrder(ctx context.Context, brokerOrderID string) (OrderResult, string, error) {
	if !looksLikeCanonicalUUID(brokerOrderID) {
		return OrderResult{}, "", ErrNotFound
	}
	if _, err := r.read.selectedAccount(ctx); err != nil {
		return OrderResult{}, "", err
	}
	equities, err := r.readEquityOrder(ctx, brokerOrderID)
	if err != nil {
		return OrderResult{}, "", err
	}
	options, err := r.readOptionOrder(ctx, brokerOrderID)
	if err != nil {
		return OrderResult{}, "", err
	}
	if len(equities)+len(options) == 0 {
		return OrderResult{}, "", ErrNotFound
	}
	if len(equities)+len(options) != 1 {
		return OrderResult{}, "", robinhoodSchemaError(r.read.caller, "broker order identity is ambiguous")
	}
	if len(equities) == 1 {
		result, err := normalizeEquityOrder(r.read.caller, equities[0])
		return result, "equity", err
	}
	result, err := normalizeOptionOrder(r.read.caller, options[0])
	return result, "option", err
}

func (r *RobinhoodExecution) readEquityOrder(ctx context.Context, brokerOrderID string) ([]equityReadOrder, error) {
	raw, err := r.read.caller.Call(ctx, "get_equity_orders", map[string]any{
		"account_number": r.read.accountID, "order_id": brokerOrderID,
	})
	if err != nil {
		return nil, fmt.Errorf("order data unavailable")
	}
	var data struct {
		Orders []equityReadOrder `json:"orders"`
		Next   string            `json:"next"`
	}
	if err := decodeRobinhoodData(r.read.caller, raw, &data); err != nil {
		return nil, err
	}
	if data.Next != "" || len(data.Orders) > 1 || (len(data.Orders) == 1 && data.Orders[0].ID != brokerOrderID) {
		return nil, robinhoodSchemaError(r.read.caller, "equity order lookup drift")
	}
	return data.Orders, nil
}

func (r *RobinhoodExecution) readOptionOrder(ctx context.Context, brokerOrderID string) ([]optionReadOrder, error) {
	raw, err := r.read.caller.Call(ctx, "get_option_orders", map[string]any{
		"account_number": r.read.accountID, "order_id": brokerOrderID,
	})
	if err != nil {
		return nil, fmt.Errorf("order data unavailable")
	}
	var data struct {
		Orders []optionReadOrder `json:"orders"`
		Next   string            `json:"next"`
	}
	if err := decodeRobinhoodData(r.read.caller, raw, &data); err != nil {
		return nil, err
	}
	if data.Next != "" || len(data.Orders) > 1 || (len(data.Orders) == 1 && data.Orders[0].ID != brokerOrderID) {
		return nil, robinhoodSchemaError(r.read.caller, "option order lookup drift")
	}
	return data.Orders, nil
}

func normalizeEquityOrder(caller rhmcp.Caller, order equityReadOrder) (OrderResult, error) {
	priceSane := false
	switch {
	case order.Type == "market" && order.Trigger == "immediate":
		priceSane = (order.Price == nil || *order.Price > 0) && order.StopPrice.Present && !order.StopPrice.Known
	case order.Type == "limit" && order.Trigger == "immediate":
		priceSane = order.Price != nil && *order.Price > 0 && order.StopPrice.Present && !order.StopPrice.Known
	case order.Type == "market" && order.Trigger == "stop":
		priceSane = (order.Price == nil || *order.Price > 0) && order.StopPrice.Present &&
			order.StopPrice.Known && order.StopPrice.Value > 0
	case order.Type == "limit" && order.Trigger == "stop":
		priceSane = order.Price != nil && *order.Price > 0 && order.StopPrice.Present &&
			order.StopPrice.Known && order.StopPrice.Value > 0
	}
	if !looksLikeCanonicalUUID(order.ID) || order.InstrumentID == "" || order.Symbol == "" ||
		(order.Side != "buy" && order.Side != "sell") || order.Quantity == nil || order.CumulativeQuantity == nil ||
		*order.Quantity <= 0 || *order.CumulativeQuantity < 0 || *order.CumulativeQuantity > *order.Quantity ||
		!priceSane {
		return OrderResult{}, robinhoodSchemaError(caller, "equity order schema drift")
	}
	state, err := normalizeRobinhoodOrderState(order.State)
	if err != nil {
		return OrderResult{}, robinhoodSchemaError(caller, "equity order state drift")
	}
	if !filledQuantityMatchesState(state, *order.CumulativeQuantity, *order.Quantity) {
		return OrderResult{}, robinhoodSchemaError(caller, "equity order quantity/state drift")
	}
	fills := make([]ReadFill, 0, len(order.Executions))
	var fillQuantity units.Qty
	for _, execution := range order.Executions {
		asOf, err := parseProviderTime(execution.Timestamp)
		if err != nil || !looksLikeCanonicalUUID(execution.ID) || execution.Quantity == nil || execution.Price == nil ||
			execution.Fees == nil || *execution.Quantity <= 0 || *execution.Price <= 0 || *execution.Fees < 0 ||
			fillQuantity > *order.CumulativeQuantity-*execution.Quantity {
			return OrderResult{}, robinhoodSchemaError(caller, "equity fill schema drift")
		}
		fillQuantity += *execution.Quantity
		fills = append(fills, ReadFill{
			FillID: execution.ID, BrokerOrderID: order.ID, InstrumentID: order.InstrumentID,
			Symbol: order.Symbol, Side: order.Side, Qty: *execution.Quantity, Price: *execution.Price,
			Fees: *execution.Fees, Source: robinhoodSource, AsOf: asOf,
		})
	}
	if fillQuantity != *order.CumulativeQuantity {
		return OrderResult{}, robinhoodSchemaError(caller, "equity cumulative fill drift")
	}
	return OrderResult{
		BrokerOrderID: order.ID, State: state, FilledQty: *order.CumulativeQuantity,
		FilledPrice: weightedFillPrice(fills), Fills: fills, Reason: order.RejectReason,
	}, nil
}

func normalizeOptionOrder(caller rhmcp.Caller, order optionReadOrder) (OrderResult, error) {
	if !looksLikeCanonicalUUID(order.ID) || order.ChainSymbol == "" || order.Quantity == nil ||
		order.ProcessedQuantity == nil || *order.Quantity <= 0 || *order.ProcessedQuantity < 0 ||
		*order.ProcessedQuantity > *order.Quantity || !order.Price.Present || !order.Price.Known ||
		order.Price.Value <= 0 || len(order.Legs) != 1 {
		return OrderResult{}, robinhoodSchemaError(caller, "option order schema drift")
	}
	state, err := normalizeRobinhoodOrderState(order.State)
	if err != nil {
		return OrderResult{}, robinhoodSchemaError(caller, "option order state drift")
	}
	if !filledQuantityMatchesState(state, *order.ProcessedQuantity, *order.Quantity) {
		return OrderResult{}, robinhoodSchemaError(caller, "option order quantity/state drift")
	}
	leg := order.Legs[0]
	if leg.OptionID == "" || (leg.Side != "buy" && leg.Side != "sell") ||
		(leg.PositionEffect != "open" && leg.PositionEffect != "close") || leg.RatioQuantity == nil ||
		int64(*leg.RatioQuantity) != 1 {
		return OrderResult{}, robinhoodSchemaError(caller, "option order leg drift")
	}
	fills := make([]ReadFill, 0, len(leg.Executions))
	var fillQuantity units.Qty
	for _, execution := range leg.Executions {
		asOf, err := parseProviderTime(execution.Timestamp)
		if err != nil || !looksLikeCanonicalUUID(execution.ID) || execution.Quantity == nil || execution.Price == nil ||
			*execution.Quantity <= 0 || units.Micros(*execution.Price) <= 0 ||
			fillQuantity > *order.ProcessedQuantity-*execution.Quantity {
			return OrderResult{}, robinhoodSchemaError(caller, "option fill schema drift")
		}
		fillQuantity += *execution.Quantity
		fills = append(fills, ReadFill{
			FillID: execution.ID, BrokerOrderID: order.ID, InstrumentID: leg.OptionID,
			Symbol: order.ChainSymbol, Side: leg.Side, Qty: *execution.Quantity,
			Price: units.Micros(*execution.Price), Source: robinhoodSource, AsOf: asOf,
		})
	}
	if fillQuantity != *order.ProcessedQuantity {
		return OrderResult{}, robinhoodSchemaError(caller, "option cumulative fill drift")
	}
	return OrderResult{
		BrokerOrderID: order.ID, State: state, FilledQty: *order.ProcessedQuantity,
		FilledPrice: weightedFillPrice(fills), Fills: fills, Reason: order.RejectReason,
	}, nil
}

func normalizeRobinhoodOrderState(state string) (string, error) {
	switch state {
	case "new", "queued", "confirmed", "unconfirmed", "pending_cancelled", "locating":
		return "submitted", nil
	case "partially_filled":
		return "partially_filled", nil
	case "filled":
		return "filled", nil
	case "cancelled", "partially_filled_rest_cancelled":
		return "cancelled", nil
	case "expired":
		return "expired", nil
	case "rejected", "failed", "voided", "locate_failed":
		return "rejected", nil
	default:
		return "", fmt.Errorf("unknown provider order state")
	}
}

func filledQuantityMatchesState(state string, filled, quantity units.Qty) bool {
	switch state {
	case "filled":
		return filled == quantity
	case "partially_filled":
		return filled > 0 && filled < quantity
	case "submitted":
		return filled < quantity
	default:
		return filled <= quantity
	}
}

func weightedFillPrice(fills []ReadFill) units.Micros {
	numerator := new(big.Int)
	denominator := new(big.Int)
	for _, fill := range fills {
		term := new(big.Int).Mul(big.NewInt(int64(fill.Qty)), big.NewInt(int64(fill.Price)))
		numerator.Add(numerator, term)
		denominator.Add(denominator, big.NewInt(int64(fill.Qty)))
	}
	if denominator.Sign() == 0 {
		return 0
	}
	numerator.Quo(numerator, denominator)
	if !numerator.IsInt64() {
		return 0
	}
	return units.Micros(numerator.Int64())
}

func looksLikeCanonicalUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

var _ ExecutionProvider = (*RobinhoodExecution)(nil)
var _ OrderKindSupport = (*RobinhoodExecution)(nil)
