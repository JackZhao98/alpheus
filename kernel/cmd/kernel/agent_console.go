package main

import (
	"context"
	"encoding/json"
	"errors"
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
	Environment string    `json:"environment"`
	Selected    string    `json:"selected"`
	Available   []string  `json:"available"`
	Generation  int64     `json:"generation"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
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
	Available   bool                    `json:"available"`
	ErrorCode   string                  `json:"error_code,omitempty"`
	Operations  []store.OperationRow    `json:"operations"`
	PaperOrders []store.AgentPaperOrder `json:"paper_orders"`
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

type agentConsoleAutonomyCommand struct {
	ExpectedGeneration int64  `json:"expected_generation"`
	Mode               string `json:"mode"`
}

type agentConsoleCandidateReviewCommand struct {
	Environment        string `json:"environment"`
	ExpectedGeneration int64  `json:"expected_generation"`
	Decision           string `json:"decision"`
}

type agentConsoleReplayCreateCommand struct {
	RequestID   string       `json:"request_id"`
	Environment string       `json:"environment"`
	Symbol      string       `json:"symbol"`
	Category    string       `json:"category"`
	Start       string       `json:"start_available_at"`
	End         string       `json:"end_available_at"`
	AsOf        string       `json:"as_of"`
	InitialCash units.Micros `json:"initial_cash"`
	DetectorIDs []string     `json:"detector_ids"`
}

type agentConsoleReplayStepCommand struct {
	Generation int64 `json:"generation"`
}

type agentConsoleReplayEnvelope struct {
	ReplayID    string `json:"replay_id"`
	State       string `json:"state"`
	Generation  int64  `json:"generation"`
	Observation *struct {
		SourceTimestamp time.Time `json:"source_timestamp"`
		AvailableAt     time.Time `json:"available_at"`
	} `json:"observation"`
	TriggerEvaluations []struct {
		Wake *struct {
			RunID string `json:"run_id"`
		} `json:"wake"`
	} `json:"trigger_evaluations"`
}

func (s *server) agentConsoleEnvironment(
	requested string,
) agentConsoleEnvironment {
	mode := s.tradingMode()
	paper := true
	live := s.robinhoodEnabled
	selected := "paper"
	dataScope := "paper"
	if requested == "live" && live {
		selected = "live"
		dataScope = "live"
	}
	executionEnabled := selected == "live" && mode == config.ModeLive ||
		selected == "paper" &&
			(mode == config.ModeSim || mode == config.ModeShadow)
	if selected == "paper" && !executionEnabled && s.store != nil {
		if autonomy, err := s.store.AgentAutonomyProfile("paper"); err == nil {
			executionEnabled = autonomy.Mode == "copilot" ||
				autonomy.Mode == "agentic"
		}
	}
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

	activity := agentConsoleActivity{
		Operations:  []store.OperationRow{},
		PaperOrders: []store.AgentPaperOrder{},
	}
	if environment.Selected == "paper" {
		if orders, err := s.store.ListAgentPaperOrders(
			"agent-default", agentConsoleOperationLimit,
		); err != nil {
			activity.ErrorCode = "paper_activity_unavailable"
		} else {
			activity.Available = true
			activity.PaperOrders = orders
		}
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
		"autonomy":    s.agentConsoleAutonomy(environment.Selected),
		"portfolio":   portfolio,
		"activity":    activity,
		"triggers": map[string]any{
			"available": false,
			"items":     []any{},
			"reason":    "trigger_registry_not_installed",
		},
		"generated_at": time.Now().UTC(),
		"source":       "kernel_console_projection",
	})
}

func (s *server) agentConsoleAutonomy(
	environment string,
) agentConsoleAutonomy {
	autonomy := agentConsoleAutonomy{
		Environment: environment,
		Selected:    "observe",
		Available:   []string{},
	}
	profile, err := s.store.AgentAutonomyProfile(environment)
	if err != nil {
		autonomy.ErrorCode = "autonomy_profile_unavailable"
		return autonomy
	}
	autonomy.Selected = profile.Mode
	autonomy.Generation = profile.Generation
	autonomy.UpdatedAt = profile.UpdatedAt.UTC()
	autonomy.Available = []string{"observe"}
	if environment == "paper" {
		autonomy.Available = []string{"observe", "copilot", "agentic"}
	}
	return autonomy
}

func (s *server) putAgentConsoleAutonomy(
	w http.ResponseWriter,
	r *http.Request,
) {
	environment := strings.TrimSpace(r.PathValue("environment"))
	var input agentConsoleAutonomyCommand
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Mode = strings.TrimSpace(input.Mode)
	if environment != "paper" && environment != "live" ||
		input.ExpectedGeneration < 1 ||
		input.Mode != "observe" && input.Mode != "copilot" &&
			input.Mode != "agentic" {
		writeAgentQueryError(w, http.StatusBadRequest,
			"autonomy_transition_invalid",
			"Autonomy transition is invalid")
		return
	}
	if environment == "live" && input.Mode != "observe" {
		writeAgentQueryError(w, http.StatusConflict,
			"live_autonomy_locked",
			"Live autonomy remains locked to Observe")
		return
	}
	profile, err := s.store.SetAgentAutonomy(
		environment, input.Mode, input.ExpectedGeneration,
		authenticatedSubject(r),
	)
	if errors.Is(err, store.ErrAgentAutonomyGenerationConflict) {
		writeAgentQueryError(w, http.StatusConflict,
			"autonomy_generation_conflict",
			"Autonomy setting changed; reload before retrying")
		return
	}
	if err != nil {
		writeInternalError(w, "set Agent autonomy", err)
		return
	}
	available := []string{"observe"}
	if environment == "paper" {
		available = []string{"observe", "copilot", "agentic"}
	}
	writeJSON(w, http.StatusOK, agentConsoleAutonomy{
		Environment: profile.Environment,
		Selected:    profile.Mode, Available: available,
		Generation: profile.Generation, UpdatedAt: profile.UpdatedAt.UTC(),
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

func (s *server) getAgentConsoleCandidates(
	w http.ResponseWriter,
	r *http.Request,
) {
	if strings.TrimSpace(r.URL.Query().Get("environment")) == "live" {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": true,
			"items":     []any{},
			"reason":    "paper_candidates_hidden_in_live",
		})
		return
	}
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodGet, "/v1/paper-candidates", nil)
	if code != "" || status != http.StatusOK {
		writeAgentQueryError(w, http.StatusServiceUnavailable,
			"cortex_paper_candidates_unavailable",
			"Paper Candidates are unavailable")
		return
	}
	writeAgentConsoleUpstream(w, raw, status)
}

func (s *server) postAgentConsoleCandidateReview(
	w http.ResponseWriter,
	r *http.Request,
) {
	candidateID := strings.TrimSpace(r.PathValue("id"))
	var input agentConsoleCandidateReviewCommand
	if !validCortexConversationID(candidateID) ||
		!decodeJSONBody(w, r, &input) {
		if !validCortexConversationID(candidateID) {
			writeAgentQueryError(w, http.StatusBadRequest,
				"paper_candidate_invalid",
				"Paper Candidate is invalid")
		}
		return
	}
	input.Environment = strings.TrimSpace(input.Environment)
	input.Decision = strings.TrimSpace(input.Decision)
	if input.Environment != "paper" ||
		input.ExpectedGeneration < 1 ||
		(input.Decision != "approve" && input.Decision != "reject") {
		writeAgentQueryError(w, http.StatusBadRequest,
			"paper_candidate_review_invalid",
			"Paper Candidate review is invalid")
		return
	}
	profile, err := s.store.AgentAutonomyProfile("paper")
	if err != nil {
		writeInternalError(w, "read Paper autonomy", err)
		return
	}
	if profile.Mode != "copilot" {
		writeAgentQueryError(w, http.StatusConflict,
			"paper_candidate_review_requires_copilot",
			"Paper Candidate review requires Copilot mode")
		return
	}
	body, _ := json.Marshal(map[string]any{
		"expected_generation": input.ExpectedGeneration,
		"decision":            input.Decision,
	})
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodPost,
		"/v1/paper-candidates/"+candidateID+"/review", body,
	)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable,
			"cortex_paper_candidate_review_unavailable",
			"Paper Candidate review is unavailable")
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

func (s *server) postAgentConsoleReplay(
	w http.ResponseWriter,
	r *http.Request,
) {
	var input agentConsoleReplayCreateCommand
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Environment = strings.TrimSpace(input.Environment)
	if input.Environment == "" {
		input.Environment = "paper"
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	input.Start = strings.TrimSpace(input.Start)
	input.End = strings.TrimSpace(input.End)
	input.AsOf = strings.TrimSpace(input.AsOf)
	if input.InitialCash == 0 {
		input.InitialCash = units.MustMicros("100000")
	}
	if input.Environment != "paper" {
		writeAgentQueryError(
			w, http.StatusBadRequest,
			"strategy_playground_paper_only",
			"Strategy Playground is available only in Paper",
		)
		return
	}
	if s.agentConsoleEnvironment(input.Environment).Selected !=
		input.Environment {
		writeAgentQueryError(
			w, http.StatusConflict,
			"agent_intraday_session_environment_unavailable",
			"Intraday session environment is unavailable",
		)
		return
	}
	body, err := json.Marshal(map[string]any{
		"request_id":         input.RequestID,
		"symbol":             input.Symbol,
		"category":           input.Category,
		"start_available_at": input.Start,
		"end_available_at":   input.End,
		"as_of":              input.AsOf,
	})
	if err != nil {
		writeInternalError(w, "encode Moody Blues replay", err)
		return
	}
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodPost,
		"/v1/data-streams/gexbot/replays", body,
	)
	if code != "" {
		writeAgentQueryError(
			w, http.StatusServiceUnavailable,
			"moody_blues_replay_unavailable",
			"Moody Blues replay is unavailable",
		)
		return
	}
	if status >= 200 && status < 300 && s.store != nil {
		envelope, envelopeErr := decodeAgentConsoleReplayEnvelope(raw)
		start, startErr := time.Parse(time.RFC3339Nano, input.Start)
		end, endErr := time.Parse(time.RFC3339Nano, input.End)
		asOf, asOfErr := time.Parse(time.RFC3339Nano, input.AsOf)
		if envelopeErr != nil || startErr != nil || endErr != nil ||
			asOfErr != nil {
			writeAgentQueryError(
				w, http.StatusBadGateway,
				"moody_blues_response_invalid",
				"Moody Blues replay response is invalid",
			)
			return
		}
		_, persistErr := s.store.CreateAgentIntradaySession(
			store.AgentIntradaySessionCreate{
				Subject:          authenticatedSubject(r),
				Environment:      input.Environment,
				RequestID:        input.RequestID,
				ReplayID:         envelope.ReplayID,
				ProviderID:       "gexbot-classic",
				Symbol:           input.Symbol,
				Category:         input.Category,
				StartAvailableAt: start.UTC(),
				EndAvailableAt:   end.UTC(),
				AsOf:             asOf.UTC(),
				State:            envelope.State,
				ReplayGeneration: envelope.Generation,
				InitialCash:      input.InitialCash,
				DetectorIDs:      input.DetectorIDs,
				Payload:          raw,
			},
		)
		if persistErr != nil {
			writeAgentQueryError(
				w, http.StatusServiceUnavailable,
				"agent_intraday_session_unavailable",
				"Intraday session could not be persisted",
			)
			return
		}
	}
	writeAgentConsoleUpstream(w, raw, status)
}

func (s *server) postAgentConsoleReplayNext(
	w http.ResponseWriter,
	r *http.Request,
) {
	replayID := strings.TrimSpace(r.PathValue("id"))
	var input agentConsoleReplayStepCommand
	if !validCortexConversationID(replayID) ||
		!decodeJSONBody(w, r, &input) {
		if !validCortexConversationID(replayID) {
			writeAgentQueryError(
				w, http.StatusBadRequest,
				"moody_blues_replay_invalid",
				"Moody Blues replay is invalid",
			)
		}
		return
	}
	if s.store != nil {
		session, err := s.store.AgentIntradaySessionByReplay(
			authenticatedSubject(r), replayID,
		)
		if err != nil {
			writeAgentQueryError(
				w, http.StatusNotFound,
				"agent_intraday_session_not_found",
				"Intraday session was not found",
			)
			return
		}
		body, err := json.Marshal(map[string]any{
			"generation":   input.Generation,
			"detector_ids": session.DetectorIDs,
		})
		if err != nil {
			writeInternalError(w, "encode Strategy Playground cursor", err)
			return
		}
		raw, status, code := s.agentRoomUpstream(
			r.Context(), http.MethodPost,
			"/v1/data-streams/gexbot/replays/"+replayID+"/next", body,
		)
		s.finishAgentConsoleReplayNext(
			w, r, replayID, raw, status, code,
		)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		writeInternalError(w, "encode Moody Blues replay cursor", err)
		return
	}
	raw, status, code := s.agentRoomUpstream(
		r.Context(), http.MethodPost,
		"/v1/data-streams/gexbot/replays/"+replayID+"/next", body,
	)
	s.finishAgentConsoleReplayNext(w, r, replayID, raw, status, code)
}

func (s *server) finishAgentConsoleReplayNext(
	w http.ResponseWriter,
	r *http.Request,
	replayID string,
	raw json.RawMessage,
	status int,
	code string,
) {
	if code != "" {
		writeAgentQueryError(
			w, http.StatusServiceUnavailable,
			"moody_blues_replay_unavailable",
			"Moody Blues replay is unavailable",
		)
		return
	}
	if status >= 200 && status < 300 && s.store != nil {
		envelope, envelopeErr := decodeAgentConsoleReplayEnvelope(raw)
		if envelopeErr != nil {
			writeAgentQueryError(
				w, http.StatusBadGateway,
				"moody_blues_response_invalid",
				"Moody Blues replay response is invalid",
			)
			return
		}
		sourceTimestamp := time.Time{}
		availableAt := time.Time{}
		if envelope.Observation != nil {
			sourceTimestamp = envelope.Observation.SourceTimestamp.UTC()
			availableAt = envelope.Observation.AvailableAt.UTC()
		}
		wakeRunID := ""
		for _, evaluation := range envelope.TriggerEvaluations {
			if evaluation.Wake != nil {
				wakeRunID = strings.TrimSpace(evaluation.Wake.RunID)
			}
		}
		if _, err := s.store.RecordAgentIntradaySessionFrame(
			store.AgentIntradaySessionFrame{
				Subject:          authenticatedSubject(r),
				ReplayID:         replayID,
				State:            envelope.State,
				ReplayGeneration: envelope.Generation,
				SourceTimestamp:  sourceTimestamp,
				AvailableAt:      availableAt,
				LatestWakeRunID:  wakeRunID,
				Payload:          raw,
			},
		); err != nil {
			writeAgentQueryError(
				w, http.StatusServiceUnavailable,
				"agent_intraday_session_unavailable",
				"Intraday session frame could not be persisted",
			)
			return
		}
	}
	writeAgentConsoleUpstream(w, raw, status)
}

func (s *server) getAgentConsoleSessions(
	w http.ResponseWriter,
	r *http.Request,
) {
	if s.store == nil {
		writeAgentQueryError(
			w, http.StatusServiceUnavailable,
			"agent_intraday_session_unavailable",
			"Intraday session projection is unavailable",
		)
		return
	}
	subject := authenticatedSubject(r)
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if environment != "" && environment != "paper" &&
		environment != "live" {
		writeAgentQueryError(
			w, http.StatusBadRequest,
			"agent_intraday_session_invalid",
			"Intraday session environment is invalid",
		)
		return
	}
	allSessions, err := s.store.ListAgentIntradaySessions(subject, 20)
	if err != nil {
		writeAgentQueryError(
			w, http.StatusServiceUnavailable,
			"agent_intraday_session_unavailable",
			"Intraday sessions could not be read",
		)
		return
	}
	sessions := make([]store.AgentIntradaySession, 0, 10)
	for _, session := range allSessions {
		if environment != "" && session.Environment != environment {
			continue
		}
		sessions = append(sessions, session)
		if len(sessions) == 10 {
			break
		}
	}
	events := []store.AgentIntradaySessionEvent{}
	if len(sessions) > 0 {
		events, err = s.store.ListAgentIntradaySessionEvents(
			subject, sessions[0].SessionID, 200,
		)
		if err != nil {
			writeAgentQueryError(
				w, http.StatusServiceUnavailable,
				"agent_intraday_session_unavailable",
				"Intraday session events could not be read",
			)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"items":     sessions,
		"events":    events,
	})
}

func decodeAgentConsoleReplayEnvelope(
	raw json.RawMessage,
) (agentConsoleReplayEnvelope, error) {
	var envelope agentConsoleReplayEnvelope
	if json.Unmarshal(raw, &envelope) != nil ||
		!validCortexConversationID(strings.TrimSpace(envelope.ReplayID)) ||
		(envelope.State != "active" && envelope.State != "complete" &&
			envelope.State != "failed") ||
		envelope.Generation < 1 {
		return agentConsoleReplayEnvelope{},
			errors.New("invalid Moody Blues replay envelope")
	}
	return envelope, nil
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
