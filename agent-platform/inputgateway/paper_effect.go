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
	if err := decoder.Decode(&authorization); err != nil {
		return PaperEffectAuthorization{},
			fmt.Errorf("decode Paper effect authorization response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PaperEffectAuthorization{},
			fmt.Errorf("Paper effect authorization response has trailing JSON")
	}
	if err := validatePaperEffectAuthorization(authorization); err != nil {
		return PaperEffectAuthorization{},
			fmt.Errorf("invalid Paper effect authorization response: %w", err)
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
	var responseParameter any
	if responseValue != nil {
		responseParameter = string(responseJSON)
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.record_cortex_paper_effect_receipt(
				$1,$2,$3,$4::JSONB,$5
			)::TEXT`,
			authorizationID, outcome, httpStatus, responseParameter,
			nullablePaperEffectFailure(failureCode),
		).Scan(&raw)
	}); err != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("record Cortex Paper effect receipt: %w", err)
	}
	var receipt PaperEffectReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("decode Paper effect receipt response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PaperEffectReceipt{},
			fmt.Errorf("Paper effect receipt response has trailing JSON")
	}
	if err := validatePaperEffectReceipt(receipt); err != nil {
		return PaperEffectReceipt{},
			fmt.Errorf("invalid Paper effect receipt response: %w", err)
	}
	return receipt, nil
}

func validatePaperEffectAuthorization(value PaperEffectAuthorization) error {
	authorizedAt, timeErr := time.Parse(time.RFC3339Nano, value.AuthorizedAt)
	expectedReview := int64(1)
	if value.AuthorizationKind == "copilot" {
		expectedReview = 2
	}
	switch {
	case value.SchemaRevision != 1:
		return fmt.Errorf("schema revision")
	case value.Status != "authorized":
		return fmt.Errorf("status")
	case !validDecisionTriggerID(value.AuthorizationID):
		return fmt.Errorf("authorization id")
	case !validDecisionTriggerID(value.CandidateID):
		return fmt.Errorf("candidate id")
	case !validDecisionTriggerID(value.EffectID):
		return fmt.Errorf("effect id")
	case value.AuthorizationKind != "copilot" &&
		value.AuthorizationKind != "agentic":
		return fmt.Errorf("authorization kind")
	case value.ReviewGeneration != expectedReview:
		return fmt.Errorf("review generation")
	case value.KernelModeGeneration < 1:
		return fmt.Errorf("Kernel mode generation")
	case !validDecisionTriggerID(value.RunID):
		return fmt.Errorf("Run id")
	case !validDecisionTriggerID(value.TaskID):
		return fmt.Errorf("Task id")
	case value.Proposal.Validate() != nil:
		return fmt.Errorf("proposal")
	case !decisionTriggerDigestPattern.MatchString(value.RecordDigest):
		return fmt.Errorf("record digest")
	case timeErr != nil || authorizedAt.IsZero():
		return fmt.Errorf("authorized time")
	case authorizedAt.Location() != time.UTC:
		return fmt.Errorf("authorized time zone")
	}
	return nil
}

func validatePaperEffectReceipt(value PaperEffectReceipt) error {
	recordedAt, timeErr := time.Parse(time.RFC3339Nano, value.RecordedAt)
	validOutcome := value.Outcome == "succeeded" ||
		value.Outcome == "failed"
	switch {
	case value.SchemaRevision != 1:
		return fmt.Errorf("schema revision")
	case value.Status != "recorded":
		return fmt.Errorf("status")
	case !validDecisionTriggerID(value.ReceiptID):
		return fmt.Errorf("receipt id")
	case !validDecisionTriggerID(value.AuthorizationID):
		return fmt.Errorf("authorization id")
	case !validDecisionTriggerID(value.CandidateID):
		return fmt.Errorf("candidate id")
	case !validDecisionTriggerID(value.EffectID):
		return fmt.Errorf("effect id")
	case !validOutcome:
		return fmt.Errorf("outcome")
	case value.HTTPStatus < 100 || value.HTTPStatus > 599:
		return fmt.Errorf("HTTP status")
	case !decisionTriggerDigestPattern.MatchString(value.RecordDigest):
		return fmt.Errorf("record digest")
	case timeErr != nil || recordedAt.IsZero():
		return fmt.Errorf("recorded time")
	case recordedAt.Location() != time.UTC:
		return fmt.Errorf("recorded time zone")
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
