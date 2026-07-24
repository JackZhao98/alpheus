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

func (adapter *PostgresAdapter) AdmitPaperCandidate(
	ctx context.Context,
	sourceCallID string,
	attemptID string,
	leaseGeneration int64,
	leaseToken string,
	proposal papercandidate.Proposal,
) (papercandidate.Admission, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(sourceCallID) ||
		!validDecisionTriggerID(attemptID) ||
		leaseGeneration < 1 ||
		!validDecisionTriggerID(leaseToken) ||
		proposal.Validate() != nil {
		return papercandidate.Admission{},
			fmt.Errorf("invalid Cortex Paper Candidate admission")
	}
	proposalRaw, err := json.Marshal(proposal)
	if err != nil {
		return papercandidate.Admission{}, err
	}
	var responseRaw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.admit_cortex_paper_trade_candidate(
				$1,$2,$3,$4::UUID,$5::JSONB
			)::TEXT`,
			sourceCallID, attemptID, leaseGeneration, leaseToken,
			string(proposalRaw),
		).Scan(&responseRaw)
	}); err != nil {
		return papercandidate.Admission{},
			fmt.Errorf("admit Cortex Paper Candidate: %w", err)
	}
	var admission papercandidate.Admission
	decoder := json.NewDecoder(bytes.NewReader(responseRaw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	proposedAt, timeErr := time.Time{}, error(nil)
	if decoder.Decode(&admission) == nil {
		proposedAt, timeErr = time.Parse(
			time.RFC3339Nano, admission.ProposedAt,
		)
	}
	if decoder.Decode(&struct{}{}) != io.EOF ||
		admission.SchemaRevision != 1 ||
		admission.Status != "proposed" ||
		!validDecisionTriggerID(admission.CandidateID) ||
		!validDecisionTriggerID(admission.RunID) ||
		!validDecisionTriggerID(admission.TaskID) ||
		admission.AttemptID != attemptID ||
		admission.Proposal.Validate() != nil ||
		admission.Proposal != proposal ||
		!decisionTriggerDigestPattern.MatchString(admission.RecordDigest) ||
		timeErr != nil || proposedAt.IsZero() ||
		proposedAt.Location() != time.UTC {
		return papercandidate.Admission{},
			fmt.Errorf("invalid Cortex Paper Candidate response")
	}
	return admission, nil
}
