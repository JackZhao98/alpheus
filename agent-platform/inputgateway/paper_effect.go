package inputgateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"

	"alpheus/agentplatform/papercandidate"
)

var paperEffectFailurePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type PaperEffectAuthorization struct {
	SchemaRevision       uint16                  `json:"schema_revision"`
	Status               string                  `json:"status"`
	Replay               bool                    `json:"replay"`
	AuthorizationID      string                  `json:"authorization_id"`
	CandidateID          string                  `json:"candidate_id"`
	EffectID             string                  `json:"effect_id"`
	AuthorizationKind    string                  `json:"authorization_kind"`
	ReviewGeneration     int64                   `json:"review_generation"`
	KernelModeGeneration int64                   `json:"kernel_mode_generation"`
	RunID                string                  `json:"run_id"`
	TaskID               string                  `json:"task_id"`
	Proposal             papercandidate.Proposal `json:"proposal"`
	RecordDigest         string                  `json:"record_digest"`
	AuthorizedAt         string                  `json:"authorized_at"`
}

type PaperEffectReceipt struct {
	SchemaRevision  uint16          `json:"schema_revision"`
	Status          string          `json:"status"`
	Replay          bool            `json:"replay"`
	ReceiptID       string          `json:"receipt_id"`
	AuthorizationID string          `json:"authorization_id"`
	CandidateID     string          `json:"candidate_id"`
	EffectID        string          `json:"effect_id"`
	Outcome         string          `json:"outcome"`
	HTTPStatus      int             `json:"http_status"`
	KernelResponse  json.RawMessage `json:"kernel_response"`
	FailureCode     string          `json:"failure_code"`
	RecordDigest    string          `json:"record_digest"`
	RecordedAt      string          `json:"recorded_at"`
}

func (adapter *PostgresAdapter) AuthorizePaperEffect(
	ctx context.Context,
	subjectID string,
	candidateID string,
	authorizationKind string,
	kernelModeGeneration int64,
) (PaperEffectAuthorization, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) ||
		!validDecisionTriggerID(candidateID) ||
		(authorizationKind != "copilot" &&
			authorizationKind != "agentic") ||
		kernelModeGeneration < 1 {
		return PaperEffectAuthorization{},
			fmt.Errorf("invalid Paper effect authorization")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.authorize_cortex_paper_effect(
				$1,$2,$3,$4
			)::TEXT`,
			subjectID, candidateID, authorizationKind,
			kernelModeGeneration,
		).Scan(&raw)
	}); err != nil {
		return PaperEffectAuthorization{},
			fmt.Errorf("authorize Cortex Paper effect: %w", err)
	}
	var authorization PaperEffectAuthorization
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&authorization) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		validatePaperEffectAuthorization(authorization) != nil {
		return PaperEffectAuthorization{},
			fmt.Errorf("invalid Paper effect authorization response")
	}
	return authorization, nil
}

func (adapter *PostgresAdapter) RecordPaperEffectReceipt(
	ctx context.Context,
	authorizationID string,
	outcome string,
	httpStatus int,
	kernelResponse json.RawMessage,
	failureCode string,
) (PaperEffectReceipt, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(authorizationID) ||
		(outcome != "succeeded" && outcome != "failed") ||
		httpStatus < 100 || httpStatus > 599 ||
		(outcome == "succeeded" &&
			(httpStatus < 200 || httpStatus > 299 ||
				!validJSONObject(kernelResponse) || failureCode != "")) ||
		(outcome == "failed" &&
			(httpStatus >= 200 && httpStatus <= 299 ||
				!paperEffectFailurePattern.MatchString(failureCode))) {
		return PaperEffectReceipt{},
			fmt.Errorf("invalid Paper effect receipt")
	}
	var responseValue any
	if len(kernelResponse) > 0 &&
		json.Unmarshal(kernelResponse, &responseValue) != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("invalid Paper effect response")
	}
	responseJSON, err := json.Marshal(responseValue)
	if err != nil {
		return PaperEffectReceipt{}, err
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.record_cortex_paper_effect_receipt(
				$1,$2,$3,$4::JSONB,$5
			)::TEXT`,
			authorizationID, outcome, httpStatus, string(responseJSON),
			nullablePaperEffectFailure(failureCode),
		).Scan(&raw)
	}); err != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("record Cortex Paper effect receipt: %w", err)
	}
	var receipt PaperEffectReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		validatePaperEffectReceipt(receipt) != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("invalid Paper effect receipt response")
	}
	return receipt, nil
}

func validatePaperEffectAuthorization(value PaperEffectAuthorization) error {
	authorizedAt, timeErr := time.Parse(time.RFC3339Nano, value.AuthorizedAt)
	expectedReview := int64(1)
	if value.AuthorizationKind == "copilot" {
		expectedReview = 2
	}
	if value.SchemaRevision != 1 || value.Status != "authorized" ||
		!validDecisionTriggerID(value.AuthorizationID) ||
		!validDecisionTriggerID(value.CandidateID) ||
		!validDecisionTriggerID(value.EffectID) ||
		(value.AuthorizationKind != "copilot" &&
			value.AuthorizationKind != "agentic") ||
		value.ReviewGeneration != expectedReview ||
		value.KernelModeGeneration < 1 ||
		!validDecisionTriggerID(value.RunID) ||
		!validDecisionTriggerID(value.TaskID) ||
		value.Proposal.Validate() != nil ||
		!decisionTriggerDigestPattern.MatchString(value.RecordDigest) ||
		timeErr != nil || authorizedAt.IsZero() ||
		authorizedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper effect authorization")
	}
	return nil
}

func validatePaperEffectReceipt(value PaperEffectReceipt) error {
	recordedAt, timeErr := time.Parse(time.RFC3339Nano, value.RecordedAt)
	validOutcome := value.Outcome == "succeeded" ||
		value.Outcome == "failed"
	if value.SchemaRevision != 1 || value.Status != "recorded" ||
		!validDecisionTriggerID(value.ReceiptID) ||
		!validDecisionTriggerID(value.AuthorizationID) ||
		!validDecisionTriggerID(value.CandidateID) ||
		!validDecisionTriggerID(value.EffectID) || !validOutcome ||
		value.HTTPStatus < 100 || value.HTTPStatus > 599 ||
		!decisionTriggerDigestPattern.MatchString(value.RecordDigest) ||
		timeErr != nil || recordedAt.IsZero() ||
		recordedAt.Location() != time.UTC {
		return fmt.Errorf("invalid Paper effect receipt")
	}
	if value.Outcome == "succeeded" {
		if value.HTTPStatus < 200 || value.HTTPStatus > 299 ||
			!validJSONObject(value.KernelResponse) ||
			value.FailureCode != "" {
			return fmt.Errorf("invalid successful Paper effect receipt")
		}
	} else if value.HTTPStatus >= 200 && value.HTTPStatus <= 299 ||
		!paperEffectFailurePattern.MatchString(value.FailureCode) {
		return fmt.Errorf("invalid failed Paper effect receipt")
	}
	return nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return false
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func nullablePaperEffectFailure(value string) any {
	if value == "" {
		return nil
	}
	return value
}
