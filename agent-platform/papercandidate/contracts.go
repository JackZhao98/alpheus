// Package papercandidate defines an effect-free Cortex proposal. A Candidate
// is not an order, approval, or execution authority.
package papercandidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

const SchemaRevisionV1 uint16 = 1

var (
	ErrInvalidCandidate = errors.New("invalid Paper Candidate")
	symbolPattern       = regexp.MustCompile(`^[A-Z][A-Z0-9.^-]{0,15}$`)
	strategyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	quantityPattern     = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,6})?$`)
)

type Proposal struct {
	SchemaRevision uint16      `json:"schema_revision"`
	StrategyID     string      `json:"strategy_id"`
	Symbol         string      `json:"symbol"`
	Kind           string      `json:"kind"`
	Side           string      `json:"side"`
	Qty            json.Number `json:"qty"`
	Thesis         string      `json:"thesis"`
	Invalidation   string      `json:"invalidation"`
	ConfidenceBPS  int         `json:"confidence_bps"`
}

type Admission struct {
	SchemaRevision uint16   `json:"schema_revision"`
	Status         string   `json:"status"`
	CandidateID    string   `json:"candidate_id"`
	RunID          string   `json:"run_id"`
	TaskID         string   `json:"task_id"`
	AttemptID      string   `json:"attempt_id"`
	Proposal       Proposal `json:"proposal"`
	RecordDigest   string   `json:"record_digest"`
	ProposedAt     string   `json:"proposed_at"`
}

func DecodeProposal(raw []byte) (Proposal, error) {
	var proposal Proposal
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&proposal) != nil ||
		decoder.Decode(&struct{}{}) == nil ||
		proposal.Validate() != nil {
		return Proposal{}, ErrInvalidCandidate
	}
	return proposal, nil
}

func (proposal Proposal) Validate() error {
	if proposal.SchemaRevision != SchemaRevisionV1 ||
		!strategyPattern.MatchString(proposal.StrategyID) ||
		!symbolPattern.MatchString(proposal.Symbol) ||
		proposal.Kind != "equity" ||
		(proposal.Side != "buy" && proposal.Side != "sell") ||
		!validQuantity(proposal.Qty) ||
		strings.TrimSpace(proposal.Thesis) != proposal.Thesis ||
		proposal.Thesis == "" || len(proposal.Thesis) > 4000 ||
		strings.TrimSpace(proposal.Invalidation) != proposal.Invalidation ||
		proposal.Invalidation == "" || len(proposal.Invalidation) > 2000 ||
		proposal.ConfidenceBPS < 0 || proposal.ConfidenceBPS > 10000 {
		return ErrInvalidCandidate
	}
	return nil
}

func OutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_revision", "strategy_id", "symbol", "kind", "side",
			"qty", "thesis", "invalidation", "confidence_bps",
		},
		"properties": map[string]any{
			"schema_revision": map[string]any{
				"type": "integer", "const": 1,
			},
			"strategy_id": map[string]any{
				"type": "string", "pattern": "^[a-z][a-z0-9_-]{0,63}$",
			},
			"symbol": map[string]any{
				"type": "string", "pattern": "^[A-Z][A-Z0-9.^-]{0,15}$",
			},
			"kind": map[string]any{
				"type": "string", "const": "equity",
			},
			"side": map[string]any{
				"type": "string", "enum": []string{"buy", "sell"},
			},
			"qty": map[string]any{
				"type": "number", "exclusiveMinimum": 0,
				"maximum": 1000,
			},
			"thesis": map[string]any{
				"type": "string", "minLength": 1, "maxLength": 4000,
			},
			"invalidation": map[string]any{
				"type": "string", "minLength": 1, "maxLength": 2000,
			},
			"confidence_bps": map[string]any{
				"type": "integer", "minimum": 0, "maximum": 10000,
			},
		},
	}
}

func validQuantity(value json.Number) bool {
	text := value.String()
	if !quantityPattern.MatchString(text) {
		return false
	}
	quantity, ok := new(big.Rat).SetString(text)
	if !ok || quantity.Sign() <= 0 {
		return false
	}
	return quantity.Cmp(big.NewRat(1000, 1)) <= 0
}
