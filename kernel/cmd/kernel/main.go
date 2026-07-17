// alpheus kernel — the ONLY surface agents ever see. Broker credentials
// live below this line; prompts live above it; neither crosses.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type server struct {
	limits    config.Limits
	mode      config.ModeConfig
	account   broker.AccountProvider
	execution broker.ExecutionProvider
	market    marketdata.Provider
	mcpLab    *mcpReadLab
	simVenue  *broker.Fake
	// broker is a simulation/test compatibility seam. Production construction
	// leaves it nil and registers read and execution capabilities separately.
	broker      broker.Adapter
	store       storeAPI
	executionMu sync.Mutex
	haltMu      sync.RWMutex
	halted      bool
	haltReason  string
}

// storeAPI keeps the HTTP surface testable without adding a database-mocking
// dependency. The production implementation is *store.Store.
type storeAPI interface {
	store.OperationGate
	WithLedgerLock(shadow bool, marketDay time.Time, fn func(store.OperationGate) error) error
	Event(kind string, payload any)
	SetOperationStatus(id, status string, verdict any) error
	GetOperation(id string) (*store.OperationRow, error)
	ListOperations(status string, limit int, cursor *store.OperationCursor) ([]store.OperationRow, error)
	InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error
	TopLessons(limit int) ([]store.Lesson, error)
	GetBlackboard(day string) (json.RawMessage, error)
	PutBlackboard(day string, doc json.RawMessage) error
	LoadGlobalHalt() (bool, string, error)
}

func main() {
	mode, err := config.LoadModeConfig()
	if err != nil {
		log.Fatalf("mode config: %v", err)
	}
	if err := mode.ValidateBroker(config.Env("BROKER", "fake")); err != nil {
		log.Fatalf("mode config: %v", err)
	}
	limits, err := config.LoadLimits()
	if err != nil {
		log.Fatalf("limits: %v", err)
	}
	brokerName := config.Env("BROKER", "fake")
	if err := validateProductionQuoteAge(brokerName, limits.QuoteMaxAgeSec); err != nil {
		log.Fatalf("limits: %v", err)
	}
	var account broker.AccountProvider
	var execution broker.ExecutionProvider
	var market marketdata.Provider
	var mcpLab *mcpReadLab
	var simVenue *broker.Fake
	switch brokerName {
	case "fake":
		simVenue = broker.NewFake(units.MustMicros("300"))
		account, execution = simVenue, simVenue
		market = marketdata.NewFakeProvider(simVenue)
	case "robinhood":
		if mode.TradingMode == config.ModeLive {
			log.Fatalf("broker: production execution is unavailable before M11")
		}
		if mode.LiveAccountID == "" {
			log.Fatalf("broker: LIVE_ACCOUNT_ID is required for Robinhood reads")
		}
		snapshot, err := rhmcp.LoadCapabilitySnapshot(config.Env("RH_MCP_CAPABILITIES_FILE", "../docs/rh_mcp_capabilities.json"))
		if err != nil {
			log.Fatalf("broker: %v", err)
		}
		client, err := rhmcp.New(rhmcp.Config{
			TokenFile: config.Env("RH_MCP_TOKEN_FILE", ""), AllowedTools: rhmcp.SafeQueryTools,
		})
		if err != nil {
			log.Fatalf("broker: %v", err)
		}
		if err := rhmcp.ValidateSnapshot(context.Background(), client, snapshot, rhmcp.SafeQueryTools); err != nil {
			_ = client.Close()
			log.Fatalf("broker: capability snapshot validation failed: %v", err)
		}
		account, err = broker.NewRobinhood(client, mode.LiveAccountID)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
		mcpLab, err = newMCPReadLab(client, mode.LiveAccountID, snapshot)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
		market, err = marketdata.NewRobinhoodProvider(client, client, snapshot.Version)
		if err != nil {
			_ = client.Close()
			log.Fatalf("broker: %v", err)
		}
	default:
		log.Fatalf("broker: unknown BROKER %q", brokerName)
	}
	st, err := store.Open(config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	if brokerName == "robinhood" {
		actual, bindingErr := account.AccountID(context.Background())
		if bindingErr != nil || actual != mode.LiveAccountID {
			st.Event("account_binding_violation", map[string]string{
				"reason": "read_provider_binding_failed", "mode": mode.TradingMode,
			})
			log.Fatalf("broker: account binding failed")
		}
	}
	s := &server{limits: limits, mode: mode, account: account, execution: execution, market: market, mcpLab: mcpLab, simVenue: simVenue, broker: simVenue, store: st}
	if err := s.loadGlobalHalt(); err != nil {
		log.Fatalf("halt state: %v", err)
	}

	if _, err := startWatchdog(s); err != nil {
		log.Fatalf("watchdog: %v", err)
	}
	st.Event("kernel_start", map[string]string{
		"broker": os.Getenv("BROKER"), "profile": limits.Profile, "mode": mode.TradingMode,
	})

	log.Printf("alpheus-kernel listening on :8100 mode=%s", mode.TradingMode)
	log.Fatal(http.ListenAndServe(":8100", s.routes()))
}

func validateProductionQuoteAge(brokerName string, maxAgeSec int) error {
	if brokerName == "robinhood" && maxAgeSec <= 0 {
		return fmt.Errorf("quote_max_age_sec must be set to a positive human-approved value for Robinhood")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

const maxJSONBodyBytes int64 = 1 << 20
const maxLessonsLimit = 100

// decodeJSONBody is the single boundary for every JSON write endpoint. It
// enforces media type, a bounded body, a strict schema, and exactly one JSON
// value before endpoint-specific validation or side effects can run.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON value"})
		}
		return false
	}
	return true
}

func writeInternalError(w http.ResponseWriter, context string, err error) {
	log.Printf("%s: %v", context, err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func validDate(day string) bool {
	parsed, err := time.Parse(time.DateOnly, day)
	return err == nil && parsed.Format(time.DateOnly) == day
}

func validUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	for i := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

type marketWindow struct {
	day        time.Time
	start, end time.Time
}

func currentMarketWindow() (marketWindow, error) {
	return marketDayWindow(time.Now(), config.Env("TZ_MARKET", "America/New_York"))
}

func marketDayWindow(now time.Time, tzName string) (marketWindow, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return marketWindow{}, fmt.Errorf("market timezone %q: %w", tzName, err)
	}
	localNow := now.In(loc)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	return marketWindow{day: day, start: day.UTC(), end: day.AddDate(0, 0, 1).UTC()}, nil
}

func (s *server) dayStateAtAccount(gate store.OperationGate, shadow bool, account broker.AccountState, window marketWindow, halted bool, haltReason string) (risk.DayState, error) {
	n, err := gate.CountTradesForDay(shadow, window.start, window.end)
	if err != nil {
		return risk.DayState{}, err
	}
	// TODO: OpenRisk from positions+journal; daily pnl vs MaxDailyLossPct;
	// consecutive-loss breaker evaluation.
	return risk.DayState{
		TradesToday: n, OpenRisk: 0, Equity: account.Equity,
		EquityKnown: account.EquityKnown, BuyingPower: account.BuyingPower,
		Halted: halted, HaltReason: haltReason,
	}, nil
}

func (s *server) getLimits(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.limits) }

func (s *server) accountProvider() broker.AccountProvider {
	if s.account != nil {
		return s.account
	}
	return s.broker
}

func (s *server) executionProvider() broker.ExecutionProvider {
	if s.execution != nil {
		return s.execution
	}
	return s.broker
}

func (s *server) marketProvider() marketdata.Provider {
	if s.market != nil {
		return s.market
	}
	if fake, ok := s.broker.(*broker.Fake); ok {
		return marketdata.NewFakeProvider(fake)
	}
	return nil
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshGlobalHalt(); err != nil {
		writeInternalError(w, "refresh global halt", err)
		return
	}
	account := s.accountProvider()
	if account == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account provider unavailable"})
		return
	}
	acct, err := account.Account(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "account data unavailable"})
		return
	}
	pos, err := account.Positions(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "position data unavailable"})
		return
	}
	window, err := currentMarketWindow()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	orders, err := account.OpenOrders(r.Context())
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "order data unavailable"})
		return
	}
	fills, err := account.RecentFills(r.Context(), window.start)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "fill data unavailable"})
		return
	}
	halted, haltReason := s.haltSnapshot()
	liveDay, err := s.dayStateAtAccount(s.store, false, acct, window, halted, haltReason)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	shadowDay, err := s.dayStateAtAccount(s.store, true, acct, window, halted, haltReason)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"account":      acct,
		"positions":    pos,
		"open_orders":  orders,
		"recent_fills": fills,
		"day":          map[string]risk.DayState{"live": liveDay, "shadow": shadowDay},
		"mode":         s.tradingMode(),
		"source":       "kernel",
		"as_of":        time.Now().UTC(),
	})
}

func (s *server) propose(w http.ResponseWriter, r *http.Request) {
	var request proposeRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	op, err := validateAndBuildOperation(request)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if s.tradingMode() == config.ModeShadow {
		op.Shadow = true
	}

	// A live close must keep the position read and order placement in one
	// critical section; otherwise two simultaneous closes can both observe the
	// same position and the second can open risk. Other broker mutations acquire
	// the same lock inside execute.
	closeLockHeld := !op.Shadow && op.Action == "close"
	if closeLockHeld {
		s.executionMu.Lock()
		defer s.executionMu.Unlock()
	}
	if op.Action == "close" {
		if op.Shadow {
			op.VerifiedReduction = true // no broker effect; no shadow position ledger yet
		} else {
			positions, err := s.accountProvider().Positions(r.Context())
			if err != nil {
				writeJSON(w, 502, map[string]string{"error": err.Error()})
				return
			}
			op, err = normalizeClose(op, positions)
			if err != nil {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
		}
	}
	acct, err := s.accountProvider().Account(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	window, err := currentMarketWindow()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var quote *broker.Quote
	if op.Action == "open" || op.Action == "close" {
		sym := op.Symbol
		if sym == "" {
			sym = op.Underlying
		}
		if q, err := s.marketProvider().Quote(r.Context(), sym); err == nil {
			quote = &q
		}
	}
	if op.Action == "open" {
		op = s.deriveOpenOperation(r.Context(), op, quote)
		if err := s.refreshGlobalHalt(); err != nil {
			writeInternalError(w, "refresh global halt", err)
			return
		}
	}
	opID := store.NewID()
	var halted bool
	var haltReason string
	if op.Action == "open" {
		// An open and a halt transition are linearized here. Once POST /halt
		// acquires the write lock, no later open can classify or reach a broker.
		s.haltMu.RLock()
		defer s.haltMu.RUnlock()
		halted, haltReason = s.halted, s.haltReason
	} else {
		halted, haltReason = s.haltSnapshot()
	}
	var v risk.Verdict
	status := "auto_approved"
	if err := s.store.WithLedgerLock(op.Shadow, window.day, func(gate store.OperationGate) error {
		day, err := s.dayStateAtAccount(gate, op.Shadow, acct, window, halted, haltReason)
		if err != nil {
			return err
		}
		v = risk.Classify(op, s.limits, day, quote)
		if err := gate.InsertEvent("operation_proposed", map[string]any{"id": opID, "op": op, "verdict": v}); err != nil {
			return err
		}
		class := v.Class
		switch v.Class {
		case "REJECT":
			class, status = "C", "rejected"
		case "C":
			status = "pending_review"
		}
		return gate.InsertOperation(opID, op.Proposer, class, status, op, v)
	}); err != nil {
		writeInternalError(w, "propose transaction", err)
		return
	}

	switch v.Class {
	case "REJECT":
		resp := map[string]any{"operation_id": opID, "status": "rejected", "class": v.Class, "reasons": v.Reasons}
		addRiskFacts(resp, op)
		writeJSON(w, 200, resp)
		return
	case "C":
		resp := map[string]any{"operation_id": opID, "status": "pending_review", "class": v.Class, "checks": v.Checks, "reasons": v.Reasons}
		addRiskFacts(resp, op)
		writeJSON(w, 200, resp)
		return
	}

	// Class A and B execute now. execute enforces that shadow operations never
	// reach the broker.
	resp := map[string]any{"operation_id": opID, "status": status, "class": v.Class, "checks": v.Checks, "reasons": v.Reasons, "shadow": op.Shadow}
	addRiskFacts(resp, op)
	var execution map[string]any
	if closeLockHeld {
		execution, err = s.executeLocked(r.Context(), opID, op, quote)
	} else {
		execution, err = s.execute(r.Context(), opID, op, quote)
	}
	if err != nil {
		_ = s.store.SetOperationStatus(opID, "failed", nil)
		writeJSON(w, 502, map[string]any{"operation_id": opID, "status": "failed", "error": err.Error()})
		return
	}
	for k, value := range execution {
		resp[k] = value
	}
	writeJSON(w, 200, resp)
}

// execute is the single Class-A/Class-B execution path. Milestone 4 reuses it
// after a human approves a Class-C operation. Live broker mutations are
// serialized here; propose calls executeLocked only while it already holds the
// lock across live-close position verification.
func (s *server) execute(ctx context.Context, opID string, op risk.Operation, quote *broker.Quote) (map[string]any, error) {
	if !op.Shadow && op.Action == "close" && !op.VerifiedReduction {
		return nil, fmt.Errorf("refusing unverified close execution")
	}
	if !op.Shadow && (op.Action == "open" || op.Action == "close" || op.Action == "cancel") {
		s.executionMu.Lock()
		defer s.executionMu.Unlock()
	}
	return s.executeLocked(ctx, opID, op, quote)
}

func (s *server) executeLocked(ctx context.Context, opID string, op risk.Operation, quote *broker.Quote) (map[string]any, error) {
	status := "auto_approved"

	if op.Action == "tighten_stop" {
		sym := op.Symbol
		if sym == "" {
			sym = op.Underlying
		}
		hypothesis := map[string]any{"action": op.Action, "symbol": sym, "stop": op.Plan["stop"]}
		if err := s.store.InsertJournal(opID, hypothesis, nil, map[string]any{}, op.Shadow); err != nil {
			return nil, err
		}
		s.store.Event("stop_tightened", map[string]any{"operation_id": opID, "symbol": sym, "stop": op.Plan["stop"], "shadow": op.Shadow})
		return map[string]any{"status": status, "stop": op.Plan["stop"]}, nil
	}

	// Shadow operations exercise classification and journaling only.
	if op.Shadow {
		return map[string]any{"status": status}, nil
	}
	execution := s.executionProvider()
	if execution == nil {
		return nil, fmt.Errorf("execution capability unavailable")
	}

	if op.Action == "cancel" {
		if err := s.assertLiveAccountBinding(ctx, opID); err != nil {
			return nil, err
		}
		res, err := execution.CancelOrder(ctx, op.BrokerOrderID)
		if err != nil {
			return nil, err
		}
		s.store.Event("order_update", map[string]any{"operation_id": opID, "order": res})
		status = "executed"
		if res.State == "rejected" {
			status = "failed"
		}
		if err := s.store.SetOperationStatus(opID, status, nil); err != nil {
			return nil, err
		}
		return map[string]any{"status": status, "order": res}, nil
	}

	if op.Action != "open" && op.Action != "close" {
		return map[string]any{"status": status}, nil
	}

	// For open, Side is the requested order side. For a live close it was
	// derived from the signed broker position by normalizeClose; payload side
	// is never trusted to decide whether the broker buys or sells.
	side := op.Side

	var limit units.Micros
	if op.Action == "open" {
		limit = op.WorkingPrice
	} else {
		if quote == nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
			return nil, fmt.Errorf("market_data_unavailable")
		}
		switch {
		case op.Limit != nil:
			limit = *op.Limit
		case side == "sell":
			limit = quote.Bid
		case side == "buy":
			limit = quote.Ask
		}
	}
	if limit <= 0 {
		return nil, fmt.Errorf("no executable price for %s", op.Action)
	}

	sym := op.Symbol
	if sym == "" {
		sym = op.Underlying
	}
	if err := s.assertLiveAccountBinding(ctx, opID); err != nil {
		return nil, err
	}
	res, err := execution.PlaceLimitOrder(ctx, broker.PlaceRequest{
		ClientOrderID: opID, Symbol: sym, Side: side, Qty: op.Qty, Limit: limit, Kind: op.Kind,
	})
	if err != nil {
		return nil, err
	}
	s.store.Event("order_update", map[string]any{"operation_id": opID, "order": res})
	if res.State == "filled" {
		status = "executed"
	} else if res.State == "rejected" {
		status = "failed"
	}
	if status != "auto_approved" {
		if err := s.store.SetOperationStatus(opID, status, nil); err != nil {
			return nil, err
		}
	}
	return map[string]any{"status": status, "order": res}, nil
}

// proposeRequest is the complete client-writable operation surface. Derived
// risk, execution prices, multiplier, resolved close direction, and verified
// reduction do not exist here, so strict JSON decoding rejects attempts to set
// them before any gate runs.
type proposeRequest struct {
	Proposer          string            `json:"proposer"`
	Action            string            `json:"action"`
	Kind              string            `json:"kind"`
	Underlying        string            `json:"underlying"`
	Symbol            string            `json:"symbol"`
	Side              string            `json:"side"`
	Qty               units.Qty         `json:"qty"`
	Limit             *units.Micros     `json:"limit"`
	MaxRiskUSD        *units.Micros     `json:"max_risk_usd"`
	Short             bool              `json:"short"`
	Plan              map[string]string `json:"plan"`
	Thesis            string            `json:"thesis"`
	Setup             string            `json:"setup"`
	Shadow            bool              `json:"shadow"`
	BrokerOrderID     string            `json:"broker_order_id,omitempty"`
	ClosesOperationID string            `json:"closes_operation_id,omitempty"`
}

func validateAndBuildOperation(request proposeRequest) (risk.Operation, error) {
	op := risk.Operation{
		Proposer: request.Proposer, Action: request.Action, Kind: request.Kind,
		Underlying: request.Underlying, Symbol: request.Symbol, Side: request.Side,
		Qty: request.Qty, Limit: request.Limit, MaxRiskUSD: request.MaxRiskUSD,
		Short: request.Short, Plan: request.Plan, Thesis: request.Thesis,
		Setup: request.Setup, Shadow: request.Shadow,
		BrokerOrderID:     request.BrokerOrderID,
		ClosesOperationID: request.ClosesOperationID,
	}
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}

	switch op.Action {
	case "open":
		if strings.TrimSpace(op.Underlying) == "" {
			return op, fmt.Errorf("open requires underlying")
		}
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("open requires symbol or underlying")
		}
		if op.Kind != "equity" && op.Kind != "option" {
			return op, fmt.Errorf("bad kind %q", op.Kind)
		}
		if op.Side != "buy" && op.Side != "sell" {
			return op, fmt.Errorf("bad side %q", op.Side)
		}
		if op.Qty <= 0 {
			return op, fmt.Errorf("qty must be greater than zero")
		}
		if op.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
			return op, fmt.Errorf("option qty must be a whole number of contracts")
		}
	case "close":
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("close requires symbol or underlying")
		}
		if op.Kind != "" && op.Kind != "equity" && op.Kind != "option" {
			return op, fmt.Errorf("bad kind %q", op.Kind)
		}
		// Close side is optional for compatibility and never controls execution.
		if op.Side != "" && op.Side != "buy" && op.Side != "sell" {
			return op, fmt.Errorf("bad side %q", op.Side)
		}
		if op.Qty <= 0 {
			return op, fmt.Errorf("qty must be greater than zero")
		}
		if op.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
			return op, fmt.Errorf("option qty must be a whole number of contracts")
		}
	case "cancel":
		op.BrokerOrderID = strings.TrimSpace(op.BrokerOrderID)
		if op.BrokerOrderID == "" {
			return op, fmt.Errorf("cancel requires broker_order_id")
		}
	case "tighten_stop":
		if strings.TrimSpace(symbol) == "" {
			return op, fmt.Errorf("tighten_stop requires symbol or underlying")
		}
		stop := strings.TrimSpace(op.Plan["stop"])
		if stop == "" {
			return op, fmt.Errorf("tighten_stop requires non-blank plan.stop")
		}
		op.Plan["stop"] = stop
	default:
		return op, fmt.Errorf("bad action %q", op.Action)
	}

	if op.Limit != nil && *op.Limit <= 0 {
		return op, fmt.Errorf("limit must be greater than zero")
	}
	return op, nil
}

func (s *server) deriveOpenOperation(ctx context.Context, op risk.Operation, quote *broker.Quote) risk.Operation {
	if quote == nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
		op.RejectReason = "market_data_unavailable"
		return op
	}

	switch op.Kind {
	case "equity":
		op.Multiplier = 1
	case "option":
		symbol := op.Symbol
		if symbol == "" {
			symbol = op.Underlying
		}
		instrument, err := s.marketProvider().Instrument(ctx, symbol)
		if err != nil || instrument.Kind != "option" || instrument.Multiplier != 100 {
			op.RejectReason = "unsupported_contract"
			return op
		}
		op.Multiplier = instrument.Multiplier
	default:
		op.RejectReason = "unsupported_contract"
		return op
	}

	if op.Limit != nil {
		op.ApprovedPriceCap = *op.Limit
	} else {
		op.ApprovedPriceCap = quote.Ask
	}
	op.WorkingPrice = quote.Ask
	if s.limits.ExecutionPolicy.StartAt == "mid" {
		op.WorkingPrice = quote.Mid()
	}
	if op.WorkingPrice > op.ApprovedPriceCap {
		op.WorkingPrice = op.ApprovedPriceCap
	}

	// Required cash rounds up, against the account, so fractional micro-dollars
	// of premium never become unreserved capacity.
	required, err := units.MulQtyPrice(op.Qty, op.ApprovedPriceCap, op.Multiplier, true)
	if err != nil {
		op.RejectReason = "risk_overflow"
		return op
	}
	feePerUnit := s.limits.ExecutionPolicy.FeePerShare
	if op.Kind == "option" {
		feePerUnit = s.limits.ExecutionPolicy.FeePerContract
	}
	// Fees also round up because they increase the account's required cash.
	fees, err := units.MulQtyPrice(op.Qty, feePerUnit, 1, true)
	if err != nil {
		op.RejectReason = "risk_overflow"
		return op
	}
	required, err = units.Add(required, fees)
	if err != nil || required <= 0 {
		op.RejectReason = "risk_overflow"
		return op
	}
	op.RequiredCash = required
	// In the single-leg long-only model, premium/cash is the maximum loss. A
	// planned stop is not a broker guarantee and never reduces this value.
	op.DerivedMaxRisk = required

	return op
}

func addRiskFacts(response map[string]any, op risk.Operation) {
	if op.Action != "open" {
		return
	}
	response["derived_max_risk"] = op.DerivedMaxRisk
	response["required_cash"] = op.RequiredCash
	response["approved_price_cap"] = op.ApprovedPriceCap
	response["working_price"] = op.WorkingPrice
	response["multiplier"] = op.Multiplier
}

func normalizeClose(op risk.Operation, positions []broker.Position) (risk.Operation, error) {
	symbol := op.Symbol
	if symbol == "" {
		symbol = op.Underlying
	}
	var position *broker.Position
	for i := range positions {
		if positions[i].Symbol == symbol && positions[i].Qty != 0 {
			position = &positions[i]
			break
		}
	}
	if position == nil {
		return op, fmt.Errorf("close requires an existing position for %s", symbol)
	}
	positionQty, err := units.AbsQty(position.Qty)
	if err != nil {
		return op, fmt.Errorf("position quantity is out of range")
	}
	if op.Qty > positionQty {
		return op, fmt.Errorf("close qty %s exceeds position qty %s", op.Qty, positionQty)
	}
	if position.Kind != "equity" && position.Kind != "option" {
		return op, fmt.Errorf("position %s has unsupported kind %q", symbol, position.Kind)
	}
	if op.Kind != "" && op.Kind != position.Kind {
		return op, fmt.Errorf("close kind %q does not match position kind %q", op.Kind, position.Kind)
	}
	if position.Kind == "option" && op.Qty%units.Qty(units.Scale) != 0 {
		return op, fmt.Errorf("option qty must be a whole number of contracts")
	}

	op.Kind = position.Kind
	op.Multiplier = position.Multiplier
	if position.Qty > 0 {
		op.Side = "sell"
	} else {
		op.Side = "buy"
	}
	op.VerifiedReduction = true
	return op, nil
}

func (s *server) getOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validUUID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a UUID"})
		return
	}
	row, err := s.store.GetOperation(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 200, row)
}

func (s *server) review(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Verdict   string `json:"verdict"` // approved | rejected
		Rationale string `json:"rationale"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id must be a UUID"})
		return
	}
	if in.Verdict != "approved" && in.Verdict != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "verdict must be approved or rejected"})
		return
	}
	row, err := s.store.GetOperation(id)
	if err != nil || row.Status != "pending_review" {
		writeJSON(w, 409, map[string]string{"error": "not pending review"})
		return
	}
	status := in.Verdict
	if status == "approved" {
		// TODO: execute approved C-class ops through the same path as Class B.
	}
	if err := s.store.SetOperationStatus(id, status, map[string]string{
		"reviewer": authenticatedSubject(r), "rationale": in.Rationale,
	}); err != nil {
		writeInternalError(w, "review operation", err)
		return
	}
	writeJSON(w, 200, map[string]string{"operation_id": id, "status": status})
}

func (s *server) postJournal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OperationID    string         `json:"operation_id"`
		Hypothesis     map[string]any `json:"hypothesis"`
		Outcome        map[string]any `json:"outcome"`
		PromptVersions map[string]any `json:"prompt_versions"`
		Shadow         bool           `json:"shadow"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	if s.tradingMode() == config.ModeShadow {
		in.Shadow = true
	}
	in.OperationID = strings.TrimSpace(in.OperationID)
	if !validUUID(in.OperationID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operation_id must be a UUID"})
		return
	}
	var outcome any
	if in.Outcome != nil {
		outcome = in.Outcome
	}
	if err := s.store.InsertJournal(in.OperationID, in.Hypothesis, outcome, in.PromptVersions, in.Shadow); err != nil {
		if errors.Is(err, store.ErrInvalidOperationReference) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operation_id does not reference an existing operation"})
		} else {
			writeInternalError(w, "insert journal", err)
		}
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) getLessons(w http.ResponseWriter, r *http.Request) {
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := jsonNumber(v)
		if err != nil || n < 1 || n > maxLessonsLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be an integer between 1 and 100"})
			return
		}
		limit = n
	}
	ls, err := s.store.TopLessons(limit)
	if err != nil {
		writeInternalError(w, "get lessons", err)
		return
	}
	writeJSON(w, 200, ls)
}

func jsonNumber(s string) (int, error) {
	var n int
	err := json.Unmarshal([]byte(s), &n)
	return n, err
}

func (s *server) getBlackboard(w http.ResponseWriter, r *http.Request) {
	day := r.PathValue("day")
	if !validDate(day) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "day must be YYYY-MM-DD"})
		return
	}
	doc, err := s.store.GetBlackboard(day)
	if err != nil {
		writeInternalError(w, "get blackboard", err)
		return
	}
	writeJSON(w, 200, doc)
}

func (s *server) putBlackboard(w http.ResponseWriter, r *http.Request) {
	day := r.PathValue("day")
	if !validDate(day) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "day must be YYYY-MM-DD"})
		return
	}
	var doc json.RawMessage
	if !decodeJSONBody(w, r, &doc) {
		return
	}
	if err := s.store.PutBlackboard(day, doc); err != nil {
		writeInternalError(w, "put blackboard", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// simQuote is only meaningful with the fake broker: shadow mode's market feed
// and the backtest replay surface.
func (s *server) simQuote(w http.ResponseWriter, r *http.Request) {
	venue := s.simVenue
	if venue == nil {
		venue, _ = s.broker.(*broker.Fake)
	}
	if venue == nil {
		writeJSON(w, 400, map[string]string{"error": "not a sim broker"})
		return
	}
	var q broker.Quote
	if !decodeJSONBody(w, r, &q) {
		return
	}
	if err := venue.SetQuote(q); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
