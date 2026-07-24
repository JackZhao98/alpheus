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
	SchemaRevision uint16                   `json:"schema_revision"`
	CandidateID    string                   `json:"candidate_id"`
	RunID          string                   `json:"run_id"`
	TaskID         string                   `json:"task_id"`
	Generation     int64                    `json:"generation"`
	Status         string                   `json:"status"`
	SourceRunState string                   `json:"source_run_state"`
	Eligible       bool                     `json:"eligible"`
	Proposal       papercandidate.Proposal  `json:"proposal"`
	RecordDigest   string                   `json:"record_digest"`
	ProposedAt     string                   `json:"proposed_at"`
	Execution      *PaperCandidateExecution `json:"execution"`
}

type PaperCandidateExecution struct {
	AuthorizationID      string          `json:"authorization_id"`
	AuthorizationKind    string          `json:"authorization_kind"`
	AuthorizationDigest  string          `json:"authorization_digest"`
	EffectID             string          `json:"effect_id"`
	KernelModeGeneration int64           `json:"kernel_mode_generation"`
	AuthorizedAt         string          `json:"authorized_at"`
	ReceiptID            string          `json:"receipt_id"`
	Outcome              string          `json:"outcome"`
	HTTPStatus           int             `json:"http_status"`
	FailureCode          string          `json:"failure_code"`
	RecordedAt           string          `json:"recorded_at"`
	Order                json.RawMessage `json:"order"`
}

type PaperCandidateReview struct {
	Status      string `json:"status"`
	Replay      bool   `json:"replay,omitempty"`
	ReasonCode  string `json:"reason_code,omitempty"`
	CandidateID string `json:"candidate_id"`
	Generation  int64  `json:"generation"`
	State       string `json:"state"`
	DecidedBy   string `json:"decided_by,omitempty"`
	DecidedAt   string `json:"decided_at,omitempty"`
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
		value.Status == "approved" ||
		value.Status == "rejected" ||
		value.Status == "source_not_committed"
	if value.SchemaRevision != papercandidate.SchemaRevisionV1 ||
		!validDecisionTriggerID(value.CandidateID) ||
		!validDecisionTriggerID(value.RunID) ||
		!validDecisionTriggerID(value.TaskID) ||
		value.Generation < 1 || value.Generation > 2 ||
		!validStatus ||
		!validPaperCandidateSourceRunState(value.SourceRunState) ||
		(value.Eligible != (value.SourceRunState == "succeeded")) ||
		(value.SourceRunState != "succeeded" &&
			value.Status != "source_not_committed") ||
		(value.SourceRunState == "succeeded" &&
			value.Status == "source_not_committed") ||
		value.Proposal.Validate() != nil ||
		!decisionTriggerDigestPattern.MatchString(value.RecordDigest) ||
		timeErr != nil || proposedAt.IsZero() ||
		proposedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper Candidate projection")
	}
	if value.Execution != nil &&
		validatePaperCandidateExecution(*value.Execution) != nil {
		return fmt.Errorf("invalid Paper Candidate execution projection")
	}
	return nil
}

func validatePaperCandidateExecution(
	value PaperCandidateExecution,
) error {
	authorizedAt, authorizedErr := time.Parse(
		time.RFC3339Nano, value.AuthorizedAt,
	)
	if !validDecisionTriggerID(value.AuthorizationID) ||
		(value.AuthorizationKind != "copilot" &&
			value.AuthorizationKind != "agentic") ||
		!decisionTriggerDigestPattern.MatchString(
			value.AuthorizationDigest,
		) ||
		!validDecisionTriggerID(value.EffectID) ||
		value.KernelModeGeneration < 1 ||
		authorizedErr != nil || authorizedAt.IsZero() ||
		authorizedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper Candidate authorization projection")
	}
	if value.Outcome == "" {
		if value.ReceiptID != "" || value.HTTPStatus != 0 ||
			value.FailureCode != "" || value.RecordedAt != "" ||
			len(value.Order) != 0 && string(value.Order) != "null" {
			return fmt.Errorf("invalid pending Paper Candidate execution")
		}
		return nil
	}
	recordedAt, recordedErr := time.Parse(
		time.RFC3339Nano, value.RecordedAt,
	)
	if !validDecisionTriggerID(value.ReceiptID) ||
		(value.Outcome != "succeeded" && value.Outcome != "failed") ||
		value.HTTPStatus < 100 || value.HTTPStatus > 599 ||
		recordedErr != nil || recordedAt.IsZero() ||
		recordedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper Candidate receipt projection")
	}
	if value.Outcome == "succeeded" {
		if value.HTTPStatus < 200 || value.HTTPStatus > 299 ||
			!validJSONObject(value.Order) || value.FailureCode != "" {
			return fmt.Errorf("invalid successful Candidate execution")
		}
	} else if value.HTTPStatus >= 200 && value.HTTPStatus <= 299 ||
		!paperEffectFailurePattern.MatchString(value.FailureCode) {
		return fmt.Errorf("invalid failed Candidate execution")
	}
	return nil
}

func (adapter *PostgresAdapter) ReviewPaperCandidate(
	ctx context.Context,
	subjectID string,
	candidateID string,
	expectedGeneration int64,
	decision string,
) (PaperCandidateReview, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) ||
		!validDecisionTriggerID(candidateID) ||
		expectedGeneration < 1 ||
		(decision != "approve" && decision != "reject") {
		return PaperCandidateReview{},
			fmt.Errorf("invalid Paper Candidate review")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.review_cortex_paper_trade_candidate(
				$1,$2,$3,$4
			)::TEXT`,
			subjectID, candidateID, expectedGeneration, decision,
		).Scan(&raw)
	}); err != nil {
		return PaperCandidateReview{},
			fmt.Errorf("review Cortex Paper Candidate: %w", err)
	}
	var review PaperCandidateReview
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&review) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		validatePaperCandidateReview(review) != nil {
		return PaperCandidateReview{},
			fmt.Errorf("invalid Paper Candidate review response")
	}
	return review, nil
}

func validatePaperCandidateReview(value PaperCandidateReview) error {
	if !validDecisionTriggerID(value.CandidateID) ||
		value.Generation < 1 || value.Generation > 2 ||
		(value.State != "proposed" && value.State != "approved" &&
			value.State != "rejected") {
		return fmt.Errorf("invalid Paper Candidate review")
	}
	if value.Status == "conflict" {
		if value.ReasonCode != "candidate_review_conflict" &&
			value.ReasonCode != "candidate_source_not_committed" {
			return fmt.Errorf("invalid Paper Candidate review conflict")
		}
		return nil
	}
	decidedAt, timeErr := time.Parse(time.RFC3339Nano, value.DecidedAt)
	if value.Status != "reviewed" ||
		(value.State != "approved" && value.State != "rejected") ||
		value.Generation != 2 ||
		!validDecisionTriggerID(value.DecidedBy) ||
		timeErr != nil || decidedAt.IsZero() ||
		decidedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper Candidate review result")
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
