package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type cortexPaperOrderCommand struct {
	SchemaRevision uint16    `json:"schema_revision"`
	EffectID       string    `json:"effect_id"`
	RunID          string    `json:"run_id"`
	TaskID         string    `json:"task_id"`
	Symbol         string    `json:"symbol"`
	Kind           string    `json:"kind"`
	Side           string    `json:"side"`
	Multiplier     int64     `json:"multiplier"`
	Qty            units.Qty `json:"qty"`
}

type cortexPaperOrderResponse struct {
	SchemaRevision uint16                `json:"schema_revision"`
	EffectID       string                `json:"effect_id"`
	RunID          string                `json:"run_id"`
	TaskID         string                `json:"task_id"`
	Environment    string                `json:"environment"`
	Order          store.AgentPaperOrder `json:"order"`
	Idempotent     bool                  `json:"idempotent_replay"`
	AvailableAt    time.Time             `json:"available_at"`
}

func (s *server) postCortexPaperOrder(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !s.authorizeCortexPaperEffect(w, r) {
		return
	}
	var command cortexPaperOrderCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexPaperOrderCommand(command) {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "invalid Cortex Paper order"})
		return
	}
	autonomy, err := s.store.AgentAutonomyProfile("paper")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "Paper autonomy unavailable"})
		return
	}
	if autonomy.Mode != "agentic" {
		writeJSON(w, http.StatusConflict,
			map[string]string{"error": "Paper Agentic mode is not active"})
		return
	}
	provider := s.marketProvider()
	if provider == nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "market data unavailable"})
		return
	}
	now := time.Now().UTC()
	quote, err := provider.Quote(r.Context(), command.Symbol)
	if err != nil || quote.Symbol != command.Symbol ||
		!quote.Usable(s.limits.QuoteMaxAgeSec, now) {
		writeJSON(w, http.StatusBadGateway,
			map[string]string{"error": "market data unavailable"})
		return
	}
	canonical, err := json.Marshal(command)
	if err != nil {
		writeInternalError(w, "encode Cortex Paper order", err)
		return
	}
	requestHash := sha256.Sum256(canonical)
	fillPrice := quote.Ask
	if command.Side == "sell" {
		fillPrice = quote.Bid
	}
	result, err := s.store.ExecuteAgentPaperOrder(
		store.AgentPaperOrderInput{
			OrderID: store.NewID(), AccountID: "agent-default",
			IdempotencyKey: command.EffectID, RequestHash: requestHash,
			ActorKind: "agent", ActorID: command.TaskID,
			Symbol: command.Symbol, Kind: command.Kind, Side: command.Side,
			Multiplier: command.Multiplier, Qty: command.Qty,
			FillPrice: fillPrice, QuoteBid: quote.Bid, QuoteAsk: quote.Ask,
			QuoteSource: quote.Source, QuoteAsOf: quote.AsOf,
		},
	)
	switch {
	case errors.Is(err, store.ErrAgentPaperIdempotencyConflict):
		writeJSON(w, http.StatusConflict,
			map[string]string{"error": "Paper effect identity conflict"})
		return
	case errors.Is(err, store.ErrAgentPaperBuyingPower),
		errors.Is(err, store.ErrAgentPaperPosition):
		writeJSON(w, http.StatusUnprocessableEntity,
			map[string]string{"error": "Paper order cannot be settled"})
		return
	case err != nil:
		writeInternalError(w, "settle Cortex Paper order", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, cortexPaperOrderResponse{
		SchemaRevision: 1, EffectID: command.EffectID,
		RunID: command.RunID, TaskID: command.TaskID,
		Environment: "paper", Order: result.Order,
		Idempotent: result.Replay, AvailableAt: time.Now().UTC(),
	})
}

func validCortexPaperOrderCommand(command cortexPaperOrderCommand) bool {
	return command.SchemaRevision == 1 &&
		validCortexBridgeIdentifier(command.EffectID) &&
		validCortexBridgeIdentifier(command.RunID) &&
		validCortexBridgeIdentifier(command.TaskID) &&
		command.Symbol == strings.ToUpper(strings.TrimSpace(command.Symbol)) &&
		validAgentQuerySymbol(command.Symbol) &&
		command.Kind == "equity" &&
		(command.Side == "buy" || command.Side == "sell") &&
		command.Multiplier == 1 && command.Qty > 0
}

func (s *server) authorizeCortexPaperEffect(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	raw, err := os.ReadFile(s.cortexPaperEffectTokenFile)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "Cortex Paper effect bridge unavailable"})
		return false
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		writeJSON(w, http.StatusServiceUnavailable,
			map[string]string{"error": "Cortex Paper effect bridge unavailable"})
		return false
	}
	if len(r.Header.Values("Authorization")) != 1 ||
		!constantTimeTokenMatch(bearerToken(r), token) {
		writeJSON(w, http.StatusUnauthorized,
			map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func cortexPaperEffectToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("empty Cortex Paper effect token")
	}
	return token, nil
}
