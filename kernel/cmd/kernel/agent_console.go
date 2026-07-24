package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

const agentConsoleOperationLimit = 20

type agentConsoleEnvironment struct {
	Selected         string `json:"selected"`
	DataScope        string `json:"data_scope"`
	KernelMode       string `json:"kernel_mode"`
	PaperAvailable   bool   `json:"paper_available"`
	LiveAvailable    bool   `json:"live_available"`
	ExecutionEnabled bool   `json:"execution_enabled"`
}

type agentConsoleAutonomy struct {
	Selected  string   `json:"selected"`
	Available []string `json:"available"`
}

type agentConsolePortfolio struct {
	Available bool      `json:"available"`
	ErrorCode string    `json:"error_code,omitempty"`
	Account   any       `json:"account,omitempty"`
	Positions any       `json:"positions,omitempty"`
	Orders    any       `json:"open_orders,omitempty"`
	AsOf      time.Time `json:"as_of,omitempty"`
	Source    string    `json:"source,omitempty"`
}

type agentConsoleActivity struct {
	Available  bool                 `json:"available"`
	ErrorCode  string               `json:"error_code,omitempty"`
	Operations []store.OperationRow `json:"operations"`
}

type agentConsoleTriggerCommand struct {
	ExpectedGeneration int64       `json:"expected_generation"`
	Title              string      `json:"title"`
	StrategyID         string      `json:"strategy_id"`
	DataSource         string      `json:"data_source"`
	Symbol             string      `json:"symbol"`
	Metric             string      `json:"metric"`
	Comparator         string      `json:"comparator"`
	Threshold          json.Number `json:"threshold"`
	CooldownSeconds    int64       `json:"cooldown_seconds"`
	Objective          string      `json:"objective"`
	Enabled            bool        `json:"enabled"`
}

func (s *server) agentConsoleEnvironment(
	requested string,
) agentConsoleEnvironment {
	mode := s.tradingMode()
	paper := true
	live := s.robinhoodEnabled
	selected := "paper"
	dataScope := "paper"
	if requested == "live" && live ||
		requested == "" && live {
		selected = "live"
		dataScope = "live"
	}
	executionEnabled := selected == "live" && mode == config.ModeLive ||
		selected == "paper" &&
			(mode == config.ModeSim || mode == config.ModeShadow)
	return agentConsoleEnvironment{
		Selected:         selected,
		DataScope:        dataScope,
		KernelMode:       mode,
		PaperAvailable:   paper,
		LiveAvailable:    live,
		ExecutionEnabled: executionEnabled,
	}
}

func (s *server) getAgentConsoleSnapshot(w http.ResponseWriter, r *http.Request) {
	environment := s.agentConsoleEnvironment(
		strings.TrimSpace(r.URL.Query().Get("environment")),
	)
	var portfolio agentConsolePortfolio
	if environment.Selected == "paper" {
		portfolio = s.agentPaperConsolePortfolio(r.Context())
	} else {
		if snapshot, err := s.captureProviderSnapshot(
			r.Context(), "read_model",
		); err != nil {
			portfolio.ErrorCode = "portfolio_unavailable"
		} else {
			portfolio.Available = true
			portfolio.Account = snapshot.Account
			portfolio.Positions = snapshot.Positions
			portfolio.Orders = snapshot.Orders
			portfolio.AsOf = snapshot.Observation.CompletedAt
			portfolio.Source = snapshot.Observation.Source
		}
	}

	activity := agentConsoleActivity{Operations: []store.OperationRow{}}
	if environment.Selected == "paper" {
		// Paper events are independent from legacy/live Kernel operations.
		// Until the Paper execution slice lands there are truthfully no trades.
		activity.Available = true
	} else {
		if operations, err := s.store.ListOperations(
			"", agentConsoleOperationLimit, nil,
		); err != nil {
			activity.ErrorCode = "operations_unavailable"
		} else {
			activity.Available = true
			activity.Operations = operations
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"environment": environment,
		"autonomy": agentConsoleAutonomy{
			Selected: "observe", Available: []string{"observe"},
		},
		"portfolio": portfolio,
		"activity":  activity,
		"triggers": map[string]any{
			"available": false,
			"items":     []any{},
			"reason":    "trigger_registry_not_installed",
		},
		"generated_at": time.Now().UTC(),
		"source":       "kernel_console_projection",
	})
}

func (s *server) agentPaperConsolePortfolio(
	ctx context.Context,
) agentConsolePortfolio {
	account, storedPositions, err := s.store.AgentPaperPortfolio(
		"agent-default",
	)
	if err != nil {
		return agentConsolePortfolio{ErrorCode: "paper_portfolio_unavailable"}
	}
	equity := account.Cash
	positions := make([]broker.Position, 0, len(storedPositions))
	asOf := account.UpdatedAt.UTC()
	for _, stored := range storedPositions {
		provider := s.marketProvider()
		if provider == nil {
			return agentConsolePortfolio{
				ErrorCode: "paper_mark_unavailable",
			}
		}
		quote, quoteErr := provider.Quote(ctx, stored.Symbol)
		if quoteErr != nil ||
			!quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
			return agentConsolePortfolio{
				ErrorCode: "paper_mark_unavailable",
			}
		}
		marketValue, valueErr := units.MulQtyPrice(
			stored.Qty, quote.Bid, stored.Multiplier, false,
		)
		if valueErr != nil {
			return agentConsolePortfolio{
				ErrorCode: "paper_mark_unavailable",
			}
		}
		equity, err = units.Add(equity, marketValue)
		if err != nil {
			return agentConsolePortfolio{
				ErrorCode: "paper_mark_unavailable",
			}
		}
		if stored.UpdatedAt.After(asOf) {
			asOf = stored.UpdatedAt.UTC()
		}
		positions = append(positions, broker.Position{
			PositionID:   "paper:" + stored.Symbol,
			InstrumentID: "paper:" + stored.Symbol,
			Symbol:       stored.Symbol, Qty: stored.Qty,
			AvgPrice: stored.AvgPrice, AvgPriceKnown: true,
			Kind: stored.Kind, Multiplier: stored.Multiplier,
			Source: "agent-paper-ledger", AsOf: stored.UpdatedAt.UTC(),
		})
	}
	return agentConsolePortfolio{
		Available: true,
		Account: broker.AccountState{
			AccountType: "paper", BuyingPower: account.BuyingPower,
			Equity: equity, EquityKnown: true,
			Cash: account.Cash, CashKnown: true,
			Source: "agent-paper-ledger", AsOf: asOf,
		},
		Positions: positions,
		Orders:    []any{},
		AsOf:      asOf,
		Source:    "agent-paper-ledger",
	}
}

func (s *server) getAgentConsoleTriggers(w http.ResponseWriter, r *http.Request) {
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodGet, "/v1/decision-triggers", nil)
	if code != "" || status != http.StatusOK {
		writeAgentQueryError(w, http.StatusServiceUnavailable,
			"cortex_trigger_registry_unavailable",
			"Decision Trigger Registry is unavailable")
		return
	}
	writeAgentConsoleUpstream(w, raw, status)
}

func (s *server) putAgentConsoleTrigger(w http.ResponseWriter, r *http.Request) {
	triggerID := strings.TrimSpace(r.PathValue("id"))
	var input agentConsoleTriggerCommand
	if !validCortexConversationID(triggerID) ||
		!decodeJSONBody(w, r, &input) {
		if !validCortexConversationID(triggerID) {
			writeAgentQueryError(w, http.StatusBadRequest,
				"cortex_trigger_invalid", "Decision Trigger is invalid")
		}
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.StrategyID = strings.TrimSpace(input.StrategyID)
	input.DataSource = strings.TrimSpace(input.DataSource)
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Metric = strings.TrimSpace(input.Metric)
	input.Comparator = strings.TrimSpace(input.Comparator)
	input.Objective = strings.TrimSpace(input.Objective)
	body, err := json.Marshal(input)
	if err != nil {
		writeInternalError(w, "encode decision Trigger", err)
		return
	}
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodPut,
		"/v1/decision-triggers/"+triggerID, body)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable,
			"cortex_trigger_registry_unavailable",
			"Decision Trigger Registry is unavailable")
		return
	}
	writeAgentConsoleUpstream(w, raw, status)
}

func writeAgentConsoleUpstream(
	w http.ResponseWriter,
	raw json.RawMessage,
	status int,
) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
