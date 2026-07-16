// alpheus kernel — the ONLY surface agents ever see. Broker credentials
// live below this line; prompts live above it; neither crosses.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"strings"
	"sync"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
)

type server struct {
	limits      config.Limits
	broker      broker.Adapter
	store       storeAPI
	executionMu sync.Mutex
}

// storeAPI keeps the HTTP surface testable without adding a database-mocking
// dependency. The production implementation is *store.Store.
type storeAPI interface {
	CountTradesToday() (int, error)
	Event(kind string, payload any)
	InsertOperation(id, proposer, class, status string, payload, verdict any) error
	SetOperationStatus(id, status string, verdict any) error
	GetOperation(id string) (*store.OperationRow, error)
	InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error
	TopLessons(limit int) ([]store.Lesson, error)
	GetBlackboard(day string) (json.RawMessage, error)
	PutBlackboard(day string, doc json.RawMessage) error
}

func main() {
	limits, err := config.LoadLimits()
	if err != nil {
		log.Fatalf("limits: %v", err)
	}
	b, err := broker.New()
	if err != nil {
		log.Fatalf("broker: %v", err)
	}
	st, err := store.Open(config.Env("DATABASE_URL", "postgresql://alpheus:alpheus@localhost:5432/alpheus?sslmode=disable"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	s := &server{limits: limits, broker: b, store: st}

	if _, err := startWatchdog(s); err != nil {
		log.Fatalf("watchdog: %v", err)
	}
	st.Event("kernel_start", map[string]string{"broker": os.Getenv("BROKER"), "profile": limits.Profile})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /limits", s.getLimits)
	mux.HandleFunc("GET /state", s.getState)
	mux.HandleFunc("POST /operations", s.propose)
	mux.HandleFunc("GET /operations/{id}", s.getOperation)
	mux.HandleFunc("POST /operations/{id}/review", s.review)
	mux.HandleFunc("POST /journal", s.postJournal)
	mux.HandleFunc("GET /lessons", s.getLessons)
	mux.HandleFunc("GET /blackboard/{day}", s.getBlackboard)
	mux.HandleFunc("PUT /blackboard/{day}", s.putBlackboard)
	mux.HandleFunc("POST /sim/quote", s.simQuote)

	log.Println("alpheus-kernel listening on :8100")
	log.Fatal(http.ListenAndServe(":8100", mux))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) dayState() (risk.DayState, error) {
	n, err := s.store.CountTradesToday()
	if err != nil {
		return risk.DayState{}, err
	}
	acct, err := s.broker.GetAccount()
	if err != nil {
		return risk.DayState{}, err
	}
	// TODO: OpenRisk from positions+journal; daily pnl vs MaxDailyLossPct;
	// consecutive-loss breaker evaluation.
	return risk.DayState{TradesToday: n, OpenRisk: 0, Equity: acct.Equity}, nil
}

func (s *server) getLimits(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.limits) }

func (s *server) getState(w http.ResponseWriter, _ *http.Request) {
	acct, err := s.broker.GetAccount()
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	pos, err := s.broker.GetPositions()
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	day, err := s.dayState()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"account": acct, "positions": pos, "day": day})
}

func (s *server) propose(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, 400, map[string]string{"error": "content-type must be application/json"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var op risk.Operation
	if err := dec.Decode(&op); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "request body must contain one JSON object"})
		return
	}
	op, err = validateAndNormalizeOperation(op)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
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
			positions, err := s.broker.GetPositions()
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
	day, err := s.dayState()
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
		if q, err := s.broker.GetQuote(sym); err == nil {
			quote = &q
		}
	}
	v := risk.Classify(op, s.limits, day, quote)
	opID := store.NewID()
	s.store.Event("operation_proposed", map[string]any{"id": opID, "op": op, "verdict": v})

	switch v.Class {
	case "REJECT":
		_ = s.store.InsertOperation(opID, op.Proposer, "C", "rejected", op, v)
		writeJSON(w, 200, map[string]any{"operation_id": opID, "status": "rejected", "class": v.Class, "reasons": v.Reasons})
		return
	case "C":
		_ = s.store.InsertOperation(opID, op.Proposer, "C", "pending_review", op, v)
		writeJSON(w, 200, map[string]any{"operation_id": opID, "status": "pending_review", "class": v.Class, "checks": v.Checks, "reasons": v.Reasons})
		return
	}

	// Class A and B execute now. execute enforces that shadow operations never
	// reach the broker.
	status := "auto_approved"
	if err := s.store.InsertOperation(opID, op.Proposer, v.Class, status, op, v); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"operation_id": opID, "status": status, "class": v.Class, "checks": v.Checks, "reasons": v.Reasons, "shadow": op.Shadow}
	var execution map[string]any
	if closeLockHeld {
		execution, err = s.executeLocked(opID, op, quote)
	} else {
		execution, err = s.execute(opID, op, quote)
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
func (s *server) execute(opID string, op risk.Operation, quote *broker.Quote) (map[string]any, error) {
	if !op.Shadow && op.Action == "close" && !op.VerifiedReduction {
		return nil, fmt.Errorf("refusing unverified close execution")
	}
	if !op.Shadow && (op.Action == "open" || op.Action == "close" || op.Action == "cancel") {
		s.executionMu.Lock()
		defer s.executionMu.Unlock()
	}
	return s.executeLocked(opID, op, quote)
}

func (s *server) executeLocked(opID string, op risk.Operation, quote *broker.Quote) (map[string]any, error) {
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

	if op.Action == "cancel" {
		res, err := s.broker.CancelOrder(op.BrokerOrderID)
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

	limit := 0.0
	switch {
	case op.Limit != nil:
		limit = *op.Limit
	case quote != nil && op.Action == "close" && side == "sell":
		limit = quote.Bid
	case quote != nil && op.Action == "close" && side == "buy":
		limit = quote.Ask
	case quote != nil:
		limit = quote.Mid()
	}
	if limit <= 0 {
		return nil, fmt.Errorf("no executable price for %s", op.Action)
	}

	sym := op.Symbol
	if sym == "" {
		sym = op.Underlying
	}
	res, err := s.broker.PlaceLimitOrder(sym, side, op.Qty, limit, op.Kind)
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

func validateAndNormalizeOperation(op risk.Operation) (risk.Operation, error) {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
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
		if !finite(op.Qty) || op.Qty <= 0 {
			return op, fmt.Errorf("qty must be finite and greater than zero")
		}
		if !finite(op.MaxRiskUSD) {
			return op, fmt.Errorf("max_risk_usd must be finite")
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
		if !finite(op.Qty) || op.Qty <= 0 {
			return op, fmt.Errorf("qty must be finite and greater than zero")
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

	if op.Limit != nil && (!finite(*op.Limit) || *op.Limit <= 0) {
		return op, fmt.Errorf("limit must be finite and greater than zero")
	}
	return op, nil
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
	if op.Qty > math.Abs(position.Qty) {
		return op, fmt.Errorf("close qty %.8g exceeds position qty %.8g", op.Qty, math.Abs(position.Qty))
	}
	if position.Kind != "equity" && position.Kind != "option" {
		return op, fmt.Errorf("position %s has unsupported kind %q", symbol, position.Kind)
	}
	if op.Kind != "" && op.Kind != position.Kind {
		return op, fmt.Errorf("close kind %q does not match position kind %q", op.Kind, position.Kind)
	}

	op.Kind = position.Kind
	if position.Qty > 0 {
		op.Side = "sell"
	} else {
		op.Side = "buy"
	}
	op.VerifiedReduction = true
	return op, nil
}

func (s *server) getOperation(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetOperation(r.PathValue("id"))
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
		Reviewer  string `json:"reviewer"` // role id or "human"
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	row, err := s.store.GetOperation(id)
	if err != nil || row.Status != "pending_review" {
		writeJSON(w, 409, map[string]string{"error": "not pending review"})
		return
	}
	status := "rejected"
	if in.Verdict == "approved" {
		status = "approved"
		// TODO: execute approved C-class ops through the same path as Class B.
	}
	_ = s.store.SetOperationStatus(id, status, map[string]string{"reviewer": in.Reviewer, "rationale": in.Rationale})
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
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	var outcome any
	if in.Outcome != nil {
		outcome = in.Outcome
	}
	if err := s.store.InsertJournal(in.OperationID, in.Hypothesis, outcome, in.PromptVersions, in.Shadow); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *server) getLessons(w http.ResponseWriter, r *http.Request) {
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := jsonNumber(v); err == nil && n > 0 {
			limit = n
		}
	}
	ls, err := s.store.TopLessons(limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
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
	doc, err := s.store.GetBlackboard(r.PathValue("day"))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, doc)
}

func (s *server) putBlackboard(w http.ResponseWriter, r *http.Request) {
	var doc json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.PutBlackboard(r.PathValue("day"), doc); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// simQuote is only meaningful with the fake broker: shadow mode's market feed
// and the backtest replay surface.
func (s *server) simQuote(w http.ResponseWriter, r *http.Request) {
	f, ok := s.broker.(*broker.Fake)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "not a sim broker"})
		return
	}
	var q broker.Quote
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	f.SetQuote(q)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
