package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"

	"alpheus/agentplatform/papercandidate"
)

func validPaperCandidateViewFixture() PaperCandidateView {
	return PaperCandidateView{
		SchemaRevision: 1,
		CandidateID:    "11111111-1111-4111-8111-111111111111",
		RunID:          "22222222-2222-4222-8222-222222222222",
		TaskID:         "33333333-3333-4333-8333-333333333333",
		Generation:     1,
		Status:         "proposed",
		SourceRunState: "succeeded",
		Eligible:       true,
		Proposal: papercandidate.Proposal{
			SchemaRevision: 1,
			StrategyID:     "manual",
			Symbol:         "SPY",
			Kind:           "equity",
			Side:           "buy",
			Qty:            json.Number("0.25"),
			Thesis:         "The reviewed evidence supports a bounded entry.",
			Invalidation:   "Cancel if the reviewed trigger no longer holds.",
			ConfidenceBPS:  6100,
		},
		RecordDigest: strings.Repeat("a", 64),
		ProposedAt:   "2026-07-24T07:32:50.008043Z",
	}
}

func TestPaperCandidateProjectionRequiresCommittedSourceRun(t *testing.T) {
	value := validPaperCandidateViewFixture()
	if err := validatePaperCandidateView(value); err != nil {
		t.Fatal(err)
	}
	value.Eligible = false
	if err := validatePaperCandidateView(value); err == nil {
		t.Fatal("eligible Candidate with mismatched source state was accepted")
	}
	value.Status = "source_not_committed"
	value.SourceRunState = "failed"
	if err := validatePaperCandidateView(value); err != nil {
		t.Fatal(err)
	}
}

func TestPaperCandidateProjectionRejectsMalformedProposal(t *testing.T) {
	value := validPaperCandidateViewFixture()
	value.Proposal.Symbol = "spy"
	if err := validatePaperCandidateView(value); err == nil {
		t.Fatal("malformed Candidate proposal was accepted")
	}
}

func TestPaperCandidateReviewValidationSeparatesDecisionFromConflict(
	t *testing.T,
) {
	reviewed := PaperCandidateReview{
		Status:      "reviewed",
		CandidateID: "11111111-1111-4111-8111-111111111111",
		Generation:  2,
		State:       "approved",
		DecidedBy:   "owner-1",
		DecidedAt:   "2026-07-24T08:00:00Z",
	}
	if err := validatePaperCandidateReview(reviewed); err != nil {
		t.Fatal(err)
	}
	conflict := PaperCandidateReview{
		Status:      "conflict",
		ReasonCode:  "candidate_review_conflict",
		CandidateID: reviewed.CandidateID,
		Generation:  2,
		State:       "rejected",
	}
	if err := validatePaperCandidateReview(conflict); err != nil {
		t.Fatal(err)
	}
	conflict.ReasonCode = "invented"
	if err := validatePaperCandidateReview(conflict); err == nil {
		t.Fatal("invented review conflict was accepted")
	}
}
