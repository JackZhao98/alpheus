package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/units"
)

const robinhoodSource = "robinhood-mcp"

type Robinhood struct {
	caller    rhmcp.Caller
	accountID string
}

func NewRobinhood(caller rhmcp.Caller, accountID string) (*Robinhood, error) {
	if caller == nil || strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("Robinhood account provider requires caller and LIVE_ACCOUNT_ID")
	}
	return &Robinhood{caller: caller, accountID: strings.TrimSpace(accountID)}, nil
}

func decodeRobinhoodData(caller rhmcp.Caller, raw json.RawMessage, dst any) error {
	err := rhmcp.DecodeData(raw, dst)
	if err != nil {
		if reporter, ok := caller.(rhmcp.DriftReporter); ok {
			reporter.MarkSchemaDrift()
		}
	}
	return err
}

func robinhoodSchemaError(caller rhmcp.Caller, message string) error {
	if reporter, ok := caller.(rhmcp.DriftReporter); ok {
		reporter.MarkSchemaDrift()
	}
	return fmt.Errorf("%s", message)
}

type robinhoodAccount struct {
	AccountNumber          string  `json:"account_number"`
	RHSAccountNumber       string  `json:"rhs_account_number"`
	Type                   string  `json:"type"`
	BrokerageAccountType   string  `json:"brokerage_account_type"`
	IsDefault              *bool   `json:"is_default"`
	AgenticAllowed         *bool   `json:"agentic_allowed"`
	OptionLevel            *string `json:"option_level"`
	State                  string  `json:"state"`
	Deactivated            *bool   `json:"deactivated"`
	PermanentlyDeactivated *bool   `json:"permanently_deactivated"`
	ManagementType         string  `json:"management_type"`
	Nickname               string  `json:"nickname"`
	Affiliate              string  `json:"affiliate"`
	RHCAccountNumber       string  `json:"rhc_account_number"`
}

type RobinhoodAccountChoice struct {
	AccountNumber          string `json:"-"`
	RHSAccountNumber       string `json:"-"`
	MaskedAccount          string `json:"masked_account"`
	Type                   string `json:"type"`
	BrokerageAccountType   string `json:"brokerage_account_type"`
	Nickname               string `json:"nickname,omitempty"`
	IsDefault              bool   `json:"is_default"`
	AgenticAllowed         bool   `json:"agentic_allowed"`
	OptionLevel            string `json:"option_level"`
	State                  string `json:"state"`
	Deactivated            bool   `json:"deactivated"`
	PermanentlyDeactivated bool   `json:"permanently_deactivated"`
}

func validateRobinhoodAccount(caller rhmcp.Caller, account robinhoodAccount) error {
	if account.AccountNumber == "" || account.RHSAccountNumber == "" || account.Type == "" ||
		account.BrokerageAccountType == "" || account.IsDefault == nil || account.AgenticAllowed == nil ||
		account.OptionLevel == nil || account.State == "" || account.Deactivated == nil ||
		account.PermanentlyDeactivated == nil {
		return robinhoodSchemaError(caller, "account schema drift")
	}
	return nil
}

func maskAccountNumber(accountNumber string) string {
	if len(accountNumber) <= 4 {
		return "••••"
	}
	return "••••" + accountNumber[len(accountNumber)-4:]
}

func RobinhoodAccountChoices(ctx context.Context, caller rhmcp.Caller) ([]RobinhoodAccountChoice, error) {
	if caller == nil {
		return nil, fmt.Errorf("Robinhood account discovery requires caller")
	}
	raw, err := caller.Call(ctx, "get_accounts", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("account data unavailable")
	}
	var data struct {
		Accounts []robinhoodAccount `json:"accounts"`
	}
	if err := decodeRobinhoodData(caller, raw, &data); err != nil {
		return nil, err
	}
	choices := make([]RobinhoodAccountChoice, 0, len(data.Accounts))
	for _, account := range data.Accounts {
		if err := validateRobinhoodAccount(caller, account); err != nil {
			return nil, err
		}
		choices = append(choices, RobinhoodAccountChoice{
			AccountNumber: account.AccountNumber, RHSAccountNumber: account.RHSAccountNumber,
			MaskedAccount: maskAccountNumber(account.AccountNumber),
			Type:          account.Type, BrokerageAccountType: account.BrokerageAccountType, Nickname: account.Nickname,
			IsDefault: *account.IsDefault, AgenticAllowed: *account.AgenticAllowed,
			OptionLevel: *account.OptionLevel, State: account.State,
			Deactivated: *account.Deactivated, PermanentlyDeactivated: *account.PermanentlyDeactivated,
		})
	}
	return choices, nil
}

func (r *Robinhood) selectedAccount(ctx context.Context) (robinhoodAccount, error) {
	raw, err := r.caller.Call(ctx, "get_accounts", map[string]any{})
	if err != nil {
		return robinhoodAccount{}, fmt.Errorf("account data unavailable")
	}
	var data struct {
		Accounts []robinhoodAccount `json:"accounts"`
	}
	if err := decodeRobinhoodData(r.caller, raw, &data); err != nil {
		return robinhoodAccount{}, err
	}
	var match *robinhoodAccount
	for i := range data.Accounts {
		if err := validateRobinhoodAccount(r.caller, data.Accounts[i]); err != nil {
			return robinhoodAccount{}, err
		}
		if data.Accounts[i].AccountNumber != r.accountID {
			continue
		}
		if match != nil {
			return robinhoodAccount{}, fmt.Errorf("account binding is ambiguous")
		}
		match = &data.Accounts[i]
	}
	if match == nil {
		return robinhoodAccount{}, fmt.Errorf("account binding failed")
	}
	if !*match.AgenticAllowed || match.State != "active" || *match.Deactivated || *match.PermanentlyDeactivated {
		return robinhoodAccount{}, fmt.Errorf("bound account is not an active agentic account")
	}
	return *match, nil
}

func (r *Robinhood) AccountID(ctx context.Context) (string, error) {
	account, err := r.selectedAccount(ctx)
	if err != nil {
		return "", err
	}
	return account.AccountNumber, nil
}

func (r *Robinhood) RealizedPnL(ctx context.Context, marketDay time.Time, marketTZ string) (RealizedPnLSnapshot, error) {
	account, err := r.selectedAccount(ctx)
	if err != nil {
		return RealizedPnLSnapshot{}, err
	}
	if strings.TrimSpace(marketTZ) == "" {
		return RealizedPnLSnapshot{}, fmt.Errorf("realized PnL timezone is required")
	}
	day := marketDay.Format(time.DateOnly)
	raw, err := r.caller.Call(ctx, "get_realized_pnl", map[string]any{
		"account_number": account.RHSAccountNumber,
		"start_date":     day, "end_date": day, "timezone": marketTZ,
		"asset_classes": []string{"equity", "option", "crypto"},
	})
	if err != nil {
		return RealizedPnLSnapshot{}, fmt.Errorf("realized PnL unavailable")
	}
	var response struct {
		AccountNumber string       `json:"account_number"`
		Window        string       `json:"window"`
		Currency      string       `json:"display_currency"`
		Total         *exactMicros `json:"total_returns"`
	}
	if err := decodeRobinhoodData(r.caller, raw, &response); err != nil {
		return RealizedPnLSnapshot{}, err
	}
	if response.AccountNumber != account.RHSAccountNumber || response.Window != day+".."+day ||
		response.Currency != "USD" || response.Total == nil {
		return RealizedPnLSnapshot{}, robinhoodSchemaError(r.caller, "realized PnL schema drift")
	}
	return RealizedPnLSnapshot{
		Total: units.Micros(*response.Total), Source: robinhoodSource, AsOf: time.Now().UTC(),
	}, nil
}

func (r *Robinhood) Account(ctx context.Context) (AccountState, error) {
	account, err := r.selectedAccount(ctx)
	if err != nil {
		return AccountState{}, err
	}
	raw, err := r.caller.Call(ctx, "get_portfolio", map[string]any{"account_number": r.accountID})
	if err != nil {
		return AccountState{}, fmt.Errorf("portfolio data unavailable")
	}
	var portfolio struct {
		TotalValue          *units.Micros `json:"total_value"`
		EquityValue         *units.Micros `json:"equity_value"`
		OptionsValue        *units.Micros `json:"options_value"`
		FuturesValue        *units.Micros `json:"futures_value"`
		EventContractsValue *units.Micros `json:"event_contracts_value"`
		CryptoValue         *units.Micros `json:"crypto_value"`
		Cash                *units.Micros `json:"cash"`
		PendingDeposits     *units.Micros `json:"pending_deposits"`
		MutualFundsValue    *units.Micros `json:"mutual_funds_value"`
		FixedIncomeValue    *units.Micros `json:"fixed_income_value"`
		Currency            string        `json:"currency"`
		BuyingPower         *struct {
			BuyingPower            *units.Micros `json:"buying_power"`
			UnleveragedBuyingPower *units.Micros `json:"unleveraged_buying_power"`
			DisplayCurrency        string        `json:"display_currency"`
		} `json:"buying_power"`
	}
	if err := decodeRobinhoodData(r.caller, raw, &portfolio); err != nil {
		return AccountState{}, err
	}
	if portfolio.TotalValue == nil || portfolio.EquityValue == nil || portfolio.OptionsValue == nil ||
		portfolio.FuturesValue == nil || portfolio.EventContractsValue == nil || portfolio.CryptoValue == nil ||
		portfolio.Cash == nil || portfolio.PendingDeposits == nil || portfolio.MutualFundsValue == nil ||
		portfolio.FixedIncomeValue == nil || portfolio.Currency != "USD" || portfolio.BuyingPower == nil ||
		portfolio.BuyingPower.BuyingPower == nil || portfolio.BuyingPower.UnleveragedBuyingPower == nil ||
		portfolio.BuyingPower.DisplayCurrency != "USD" {
		return AccountState{}, robinhoodSchemaError(r.caller, "portfolio schema drift")
	}
	return AccountState{
		ExternalID: account.AccountNumber, AccountType: account.Type + "/" + account.BrokerageAccountType,
		BuyingPower: *portfolio.BuyingPower.BuyingPower, Equity: *portfolio.TotalValue, EquityKnown: true,
		Cash: *portfolio.Cash, CashKnown: true, SettledCashKnown: false,
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}, nil
}

type equityPosition struct {
	Symbol       string        `json:"symbol"`
	Type         string        `json:"type"`
	Quantity     *units.Qty    `json:"quantity"`
	AveragePrice *units.Micros `json:"average_buy_price"`
}

type optionPosition struct {
	OptionID             string        `json:"option_id"`
	ChainSymbol          string        `json:"chain_symbol"`
	Type                 string        `json:"type"`
	Quantity             *units.Qty    `json:"quantity"`
	AveragePrice         *units.Micros `json:"average_price"`
	TradeValueMultiplier *exactInt64   `json:"trade_value_multiplier"`
}

type exactInt64 int64

func (v *exactInt64) UnmarshalJSON(raw []byte) error {
	parsed, err := rhmcp.DecodeExactWhole(raw)
	if err != nil {
		return fmt.Errorf("invalid exact integer")
	}
	*v = exactInt64(parsed)
	return nil
}

type exactMicros units.Micros

func (v *exactMicros) UnmarshalJSON(raw []byte) error {
	normalized, err := rhmcp.DecodeExactDecimal(raw, 6)
	if err != nil {
		return fmt.Errorf("invalid exact money")
	}
	var parsed units.Micros
	if err := parsed.UnmarshalJSON([]byte(normalized)); err != nil {
		return fmt.Errorf("invalid exact money")
	}
	*v = exactMicros(parsed)
	return nil
}

type optionalMicros struct {
	Value   units.Micros
	Present bool
	Known   bool
}

func (v *optionalMicros) UnmarshalJSON(raw []byte) error {
	v.Present = true
	if string(raw) == "null" || string(raw) == `""` {
		return nil
	}
	var parsed exactMicros
	if err := parsed.UnmarshalJSON(raw); err != nil {
		return err
	}
	v.Value, v.Known = units.Micros(parsed), true
	return nil
}

func (r *Robinhood) Positions(ctx context.Context) ([]Position, error) {
	if _, err := r.selectedAccount(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := []Position{}
	cursor := ""
	for page := 0; page < 100; page++ {
		args := map[string]any{"account_number": r.accountID}
		if cursor != "" {
			args["cursor"] = cursor
		}
		raw, err := r.caller.Call(ctx, "get_equity_positions", args)
		if err != nil {
			return nil, fmt.Errorf("position data unavailable")
		}
		var equities struct {
			Positions []equityPosition `json:"positions"`
			Next      string           `json:"next"`
		}
		if err := decodeRobinhoodData(r.caller, raw, &equities); err != nil {
			return nil, err
		}
		for _, position := range equities.Positions {
			if position.Symbol == "" || position.Quantity == nil ||
				(position.Type != "long" && position.Type != "short" && position.Type != "boxed" && position.Type != "empty") {
				return nil, robinhoodSchemaError(r.caller, "equity position schema drift")
			}
			if position.Type == "boxed" ||
				(position.Type == "long" && *position.Quantity <= 0) ||
				(position.Type == "short" && *position.Quantity >= 0) ||
				(position.Type == "empty" && *position.Quantity != 0) {
				return nil, robinhoodSchemaError(r.caller, "unsupported equity position semantics")
			}
			if position.Type == "empty" {
				continue
			}
			avg := units.Micros(0)
			avgKnown := position.AveragePrice != nil
			if avgKnown {
				avg = *position.AveragePrice
			}
			out = append(out, Position{
				PositionID: "equity:" + position.Symbol, Symbol: position.Symbol,
				Qty: *position.Quantity, AvgPrice: avg, AvgPriceKnown: avgKnown, Kind: "equity", Multiplier: 1,
				Source: robinhoodSource, AsOf: now,
			})
		}
		cursor, err = providerNextCursor(equities.Next)
		if err != nil {
			return nil, robinhoodSchemaError(r.caller, "equity position pagination drift")
		}
		if cursor == "" {
			break
		}
		if page == 99 {
			return nil, fmt.Errorf("position pagination exceeds safety bound")
		}
	}
	cursor = ""
	for page := 0; page < 100; page++ {
		args := map[string]any{"account_number": r.accountID, "nonzero": true}
		if cursor != "" {
			args["cursor"] = cursor
		}
		raw, err := r.caller.Call(ctx, "get_option_positions", args)
		if err != nil {
			return nil, fmt.Errorf("position data unavailable")
		}
		var options struct {
			Positions []optionPosition `json:"positions"`
			Next      string           `json:"next"`
		}
		if err := decodeRobinhoodData(r.caller, raw, &options); err != nil {
			return nil, err
		}
		for _, position := range options.Positions {
			if position.OptionID == "" || position.ChainSymbol == "" || position.Quantity == nil ||
				position.AveragePrice == nil || position.TradeValueMultiplier == nil || int64(*position.TradeValueMultiplier) != 100 {
				return nil, robinhoodSchemaError(r.caller, "option position schema drift")
			}
			qty := *position.Quantity
			if position.Type == "short" {
				if qty > 0 {
					qty = -qty
				}
			} else if position.Type != "long" {
				return nil, robinhoodSchemaError(r.caller, "option position side drift")
			}
			out = append(out, Position{
				PositionID: position.OptionID, InstrumentID: position.OptionID, Symbol: position.ChainSymbol,
				Qty: qty, AvgPrice: *position.AveragePrice, AvgPriceKnown: true, Kind: "option", Multiplier: 100,
				Source: robinhoodSource, AsOf: now,
			})
		}
		cursor, err = providerNextCursor(options.Next)
		if err != nil {
			return nil, robinhoodSchemaError(r.caller, "option position pagination drift")
		}
		if cursor == "" {
			break
		}
		if page == 99 {
			return nil, fmt.Errorf("position pagination exceeds safety bound")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].PositionID < out[j].PositionID
	})
	return out, nil
}

type equityExecution struct {
	ID        string        `json:"id"`
	Quantity  *units.Qty    `json:"quantity"`
	Price     *units.Micros `json:"price"`
	Timestamp string        `json:"timestamp"`
}

type equityReadOrder struct {
	ID                 string            `json:"id"`
	InstrumentID       string            `json:"instrument_id"`
	Symbol             string            `json:"symbol"`
	Side               string            `json:"side"`
	State              string            `json:"state"`
	Quantity           *units.Qty        `json:"quantity"`
	CumulativeQuantity *units.Qty        `json:"cumulative_quantity"`
	Price              *units.Micros     `json:"price"`
	CreatedAt          string            `json:"created_at"`
	LastTransactionAt  *string           `json:"last_transaction_at"`
	Executions         []equityExecution `json:"executions"`
}

type optionExecution struct {
	ID        string       `json:"id"`
	Quantity  *units.Qty   `json:"quantity"`
	Price     *exactMicros `json:"price"`
	Timestamp string       `json:"timestamp"`
}

type optionOrderLeg struct {
	ID             string            `json:"id"`
	OptionID       string            `json:"option_id"`
	Side           string            `json:"side"`
	PositionEffect string            `json:"position_effect"`
	RatioQuantity  *exactInt64       `json:"ratio_quantity"`
	Executions     []optionExecution `json:"executions"`
}

type optionReadOrder struct {
	ID                string           `json:"id"`
	ChainSymbol       string           `json:"chain_symbol"`
	State             string           `json:"state"`
	Quantity          *units.Qty       `json:"quantity"`
	ProcessedQuantity *units.Qty       `json:"processed_quantity"`
	Price             optionalMicros   `json:"price"`
	UpdatedAt         string           `json:"updated_at"`
	Legs              []optionOrderLeg `json:"legs"`
}

func parseProviderTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() == nil {
		return time.Time{}, fmt.Errorf("provider timestamp drift")
	}
	return parsed.UTC(), nil
}

func providerNextCursor(next string) (string, error) {
	if next == "" {
		return "", nil
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return "", err
	}
	values, ok := parsed.Query()["cursor"]
	if !ok || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", fmt.Errorf("invalid provider pagination")
	}
	return values[0], nil
}

func (r *Robinhood) equityOrders(ctx context.Context) ([]equityReadOrder, error) {
	return readOrderPages[equityReadOrder](ctx, r, "get_equity_orders")
}

func (r *Robinhood) optionOrders(ctx context.Context) ([]optionReadOrder, error) {
	return readOrderPages[optionReadOrder](ctx, r, "get_option_orders")
}

func readOrderPages[T any](ctx context.Context, r *Robinhood, tool string) ([]T, error) {
	if _, err := r.selectedAccount(ctx); err != nil {
		return nil, err
	}
	out := []T{}
	cursor := ""
	for page := 0; page < 100; page++ {
		args := map[string]any{"account_number": r.accountID}
		if cursor != "" {
			args["cursor"] = cursor
		}
		raw, err := r.caller.Call(ctx, tool, args)
		if err != nil {
			return nil, fmt.Errorf("order data unavailable")
		}
		var data struct {
			Orders []T    `json:"orders"`
			Next   string `json:"next"`
		}
		if err := decodeRobinhoodData(r.caller, raw, &data); err != nil {
			return nil, err
		}
		out = append(out, data.Orders...)
		cursor, err = providerNextCursor(data.Next)
		if err != nil {
			return nil, robinhoodSchemaError(r.caller, "order pagination drift")
		}
		if cursor == "" {
			return out, nil
		}
	}
	return nil, fmt.Errorf("order pagination exceeds safety bound")
}

func isOpenOrderState(state string) bool {
	switch state {
	case "new", "queued", "confirmed", "unconfirmed", "partially_filled", "pending_cancelled":
		return true
	default:
		return false
	}
}

func (r *Robinhood) OpenOrders(ctx context.Context) ([]ReadOrder, error) {
	equities, err := r.equityOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := []ReadOrder{}
	for _, order := range equities {
		if !isOpenOrderState(order.State) {
			continue
		}
		if order.ID == "" || order.InstrumentID == "" || order.Symbol == "" ||
			(order.Side != "buy" && order.Side != "sell") || order.Quantity == nil || order.CumulativeQuantity == nil {
			return nil, robinhoodSchemaError(r.caller, "equity order schema drift")
		}
		asOfText := order.CreatedAt
		if order.LastTransactionAt != nil {
			asOfText = *order.LastTransactionAt
		}
		asOf, err := parseProviderTime(asOfText)
		if err != nil {
			return nil, robinhoodSchemaError(r.caller, "equity order timestamp drift")
		}
		price := units.Micros(0)
		priceKnown := order.Price != nil
		if priceKnown {
			price = *order.Price
		}
		out = append(out, ReadOrder{
			BrokerOrderID: order.ID,
			InstrumentID:  order.InstrumentID, Symbol: order.Symbol, Side: order.Side, State: order.State,
			Qty: *order.Quantity, FilledQty: *order.CumulativeQuantity, LimitPrice: price, LimitPriceKnown: priceKnown,
			Source: robinhoodSource, AsOf: asOf,
		})
	}
	options, err := r.optionOrders(ctx)
	if err != nil {
		return nil, err
	}
	for _, order := range options {
		if !isOpenOrderState(order.State) {
			continue
		}
		if order.ID == "" || order.ChainSymbol == "" || order.Quantity == nil || order.ProcessedQuantity == nil ||
			len(order.Legs) != 1 || order.Legs[0].OptionID == "" || order.Legs[0].RatioQuantity == nil ||
			int64(*order.Legs[0].RatioQuantity) != 1 || (order.Legs[0].Side != "buy" && order.Legs[0].Side != "sell") {
			return nil, robinhoodSchemaError(r.caller, "option order schema drift")
		}
		asOf, err := parseProviderTime(order.UpdatedAt)
		if err != nil {
			return nil, robinhoodSchemaError(r.caller, "option order timestamp drift")
		}
		if !order.Price.Present {
			return nil, robinhoodSchemaError(r.caller, "option order price schema drift")
		}
		leg := order.Legs[0]
		out = append(out, ReadOrder{
			BrokerOrderID: order.ID, InstrumentID: leg.OptionID, Symbol: order.ChainSymbol,
			Side: leg.Side, State: order.State, Qty: *order.Quantity, FilledQty: *order.ProcessedQuantity,
			LimitPrice: order.Price.Value, LimitPriceKnown: order.Price.Known, Source: robinhoodSource, AsOf: asOf,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BrokerOrderID < out[j].BrokerOrderID })
	return out, nil
}

func (r *Robinhood) RecentFills(ctx context.Context, since time.Time) ([]ReadFill, error) {
	equities, err := r.equityOrders(ctx)
	if err != nil {
		return nil, err
	}
	out := []ReadFill{}
	for _, order := range equities {
		if order.ID == "" || order.InstrumentID == "" || order.Symbol == "" || (order.Side != "buy" && order.Side != "sell") {
			return nil, robinhoodSchemaError(r.caller, "equity order schema drift")
		}
		for _, execution := range order.Executions {
			asOf, err := parseProviderTime(execution.Timestamp)
			if err != nil || execution.ID == "" || execution.Quantity == nil || execution.Price == nil {
				return nil, robinhoodSchemaError(r.caller, "fill schema drift")
			}
			if asOf.Before(since) {
				continue
			}
			out = append(out, ReadFill{
				FillID: execution.ID, BrokerOrderID: order.ID, InstrumentID: order.InstrumentID,
				Symbol: order.Symbol, Side: order.Side, Qty: *execution.Quantity, Price: *execution.Price,
				Source: robinhoodSource, AsOf: asOf,
			})
		}
	}
	options, err := r.optionOrders(ctx)
	if err != nil {
		return nil, err
	}
	for _, order := range options {
		if order.ID == "" || order.ChainSymbol == "" || len(order.Legs) == 0 {
			return nil, robinhoodSchemaError(r.caller, "option order schema drift")
		}
		for _, leg := range order.Legs {
			if leg.OptionID == "" || (leg.Side != "buy" && leg.Side != "sell") {
				return nil, robinhoodSchemaError(r.caller, "option order leg drift")
			}
			for _, execution := range leg.Executions {
				asOf, err := parseProviderTime(execution.Timestamp)
				if err != nil || execution.ID == "" || execution.Quantity == nil || execution.Price == nil {
					return nil, robinhoodSchemaError(r.caller, "option fill schema drift")
				}
				if asOf.Before(since) {
					continue
				}
				out = append(out, ReadFill{
					FillID: execution.ID, BrokerOrderID: order.ID, InstrumentID: leg.OptionID,
					Symbol: order.ChainSymbol, Side: leg.Side, Qty: *execution.Quantity, Price: units.Micros(*execution.Price),
					Source: robinhoodSource, AsOf: asOf,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AsOf.Equal(out[j].AsOf) {
			return out[i].AsOf.Before(out[j].AsOf)
		}
		return out[i].FillID < out[j].FillID
	})
	return out, nil
}

var _ AccountProvider = (*Robinhood)(nil)
