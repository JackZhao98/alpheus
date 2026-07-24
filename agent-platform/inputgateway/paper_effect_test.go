package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"

	"alpheus/agentplatform/papercandidate"
)

func validPaperEffectAuthorizationFixture() PaperEffectAuthorization {
	return PaperEffectAuthorization{
		SchemaRevision:       1,
		Status:               "authorized",
		AuthorizationID:      "11111111-1111-4111-8111-111111111111",
		CandidateID:          "22222222-2222-4222-8222-222222222222",
		EffectID:             "33333333-3333-4333-8333-333333333333",
		AuthorizationKind:    "agentic",
		ReviewGeneration:     1,
		KernelModeGeneration: 2,
		RunID:                "44444444-4444-4444-8444-444444444444",
		TaskID:               "55555555-5555-4555-8555-555555555555",
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
		AuthorizedAt: "2026-07-24T08:00:00Z",
	}
}

func TestPaperEffectAuthorizationBindsModeToReviewGeneration(t *testing.T) {
	value := validPaperEffectAuthorizationFixture()
	if err := validatePaperEffectAuthorization(value); err != nil {
		t.Fatal(err)
	}
	value.AuthorizationKind = "copilot"
	if err := validatePaperEffectAuthorization(value); err == nil {
		t.Fatal("Copilot authorization accepted Agentic review generation")
	}
	value.ReviewGeneration = 2
	if err := validatePaperEffectAuthorization(value); err != nil {
		t.Fatal(err)
	}
}

func TestPaperEffectReceiptSeparatesSuccessAndFailure(t *testing.T) {
	success := PaperEffectReceipt{
		SchemaRevision:  1,
		Status:          "recorded",
		ReceiptID:       "11111111-1111-4111-8111-111111111111",
		AuthorizationID: "22222222-2222-4222-8222-222222222222",
		CandidateID:     "33333333-3333-4333-8333-333333333333",
		EffectID:        "44444444-4444-4444-8444-444444444444",
		Outcome:         "succeeded",
		HTTPStatus:      200,
		KernelResponse:  json.RawMessage(`{"order":{"state":"filled"}}`),
		RecordDigest:    strings.Repeat("b", 64),
		RecordedAt:      "2026-07-24T08:01:00Z",
	}
	if err := validatePaperEffectReceipt(success); err != nil {
		t.Fatal(err)
	}
	failed := success
	failed.Outcome = "failed"
	failed.HTTPStatus = 409
	failed.KernelResponse = json.RawMessage("null")
	failed.FailureCode = "kernel_mode_conflict"
	if err := validatePaperEffectReceipt(failed); err != nil {
		t.Fatal(err)
	}
	failed.HTTPStatus = 200
	if err := validatePaperEffectReceipt(failed); err == nil {
		t.Fatal("failed receipt accepted a successful HTTP status")
	}
}
