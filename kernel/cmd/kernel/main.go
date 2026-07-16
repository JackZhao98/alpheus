// alpheus kernel — the ONLY surface agents ever see. Broker credentials
// live below this line; prompts live above it; neither crosses.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
)

type server struct {
	limits config.Limits
	broker broker.Adapter
	store  *store.Store
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
	var op risk.Operation
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	day, err := s.dayState()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var quote *broker.Quote
	if op.Action == "open" {
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

	// Class A and B execute now (shadow ops are journaled, never sent to broker)
	status := "auto_approved"
	_ = s.store.InsertOperation(opID, op.Proposer, v.Class, status, op, v)
	resp := map[string]any{"operation_id": opID, "status": status, "class": v.Class, "checks": v.Checks, "reasons": v.Reasons, "shadow": op.Shadow}
	if !op.Shadow && (op.Action == "open" || op.Action == "close") {
		limit := 0.0
		switch {
		case op.Limit != nil:
			limit = *op.Limit
		case quote != nil:
			limit = quote.Mid()
		}
		if limit > 0 {
			side := op.Side
			if op.Action == "close" {
				if side == "buy" {
					side = "sell"
				} else {
					side = "buy"
				}
			}
			sym := op.Symbol
			if sym == "" {
				sym = op.Underlying
			}
			res, err := s.broker.PlaceLimitOrder(sym, side, op.Qty, limit, op.Kind)
			if err != nil {
				writeJSON(w, 502, map[string]any{"operation_id": opID, "status": "failed", "error": err.Error()})
				return
			}
			s.store.Event("order_update", map[string]any{"operation_id": opID, "order": res})
			if res.State == "filled" {
				status = "executed"
				_ = s.store.SetOperationStatus(opID, status, nil)
			}
			resp["status"], resp["order"] = status, res
		}
	}
	writeJSON(w, 200, resp)
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
