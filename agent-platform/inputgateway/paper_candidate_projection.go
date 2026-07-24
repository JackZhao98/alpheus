package inputgateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"alpheus/agentplatform/papercandidate"
)

type PaperCandidateView struct {
	SchemaRevision uint16                  `json:"schema_revision"`
	CandidateID    string                  `json:"candidate_id"`
	RunID          string                  `json:"run_id"`
	TaskID         string                  `json:"task_id"`
	Status         string                  `json:"status"`
	SourceRunState string                  `json:"source_run_state"`
	Eligible       bool                    `json:"eligible"`
	Proposal       papercandidate.Proposal `json:"proposal"`
	RecordDigest   string                  `json:"record_digest"`
	ProposedAt     string                  `json:"proposed_at"`
}

func (adapter *PostgresAdapter) ListPaperCandidates(
	ctx context.Context,
	subjectID string,
	limit int,
) ([]PaperCandidateView, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid Paper Candidate list")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.list_cortex_paper_trade_candidates(
				$1,$2
			)::TEXT`,
			subjectID, limit,
		).Scan(&raw)
	}); err != nil {
		return nil, fmt.Errorf("list Cortex Paper Candidates: %w", err)
	}
	var candidates []PaperCandidateView
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&candidates) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		len(candidates) > limit {
		return nil, fmt.Errorf("invalid Paper Candidate list response")
	}
	for _, candidate := range candidates {
		if err := validatePaperCandidateView(candidate); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func validatePaperCandidateView(value PaperCandidateView) error {
	proposedAt, timeErr := time.Parse(time.RFC3339Nano, value.ProposedAt)
	validStatus := value.Status == "proposed" ||
		value.Status == "source_not_committed"
	if value.SchemaRevision != papercandidate.SchemaRevisionV1 ||
		!validDecisionTriggerID(value.CandidateID) ||
		!validDecisionTriggerID(value.RunID) ||
		!validDecisionTriggerID(value.TaskID) ||
		!validStatus ||
		!validPaperCandidateSourceRunState(value.SourceRunState) ||
		(value.Eligible != (value.Status == "proposed" &&
			value.SourceRunState == "succeeded")) ||
		value.Proposal.Validate() != nil ||
		!decisionTriggerDigestPattern.MatchString(value.RecordDigest) ||
		timeErr != nil || proposedAt.IsZero() ||
		proposedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper Candidate projection")
	}
	return nil
}

func validPaperCandidateSourceRunState(value string) bool {
	switch value {
	case "queued", "running", "waiting", "canceling", "succeeded",
		"failed", "canceled", "superseded", "dead_lettered":
		return true
	default:
		return false
	}
}
