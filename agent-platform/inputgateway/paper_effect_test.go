package inputgateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"alpheus/agentplatform/papercandidate"
	_ "github.com/lib/pq"
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

func TestPaperEffectAuthorizationDecodesDatabaseJSON(t *testing.T) {
	raw := []byte(`{
		"replay":false,
		"run_id":"b9bc5325-e1c4-41ad-a984-8d2270d156b9",
		"status":"authorized",
		"task_id":"9fca0fdd-a6ff-467a-ad3a-225c14e54de1",
		"proposal":{
			"qty":0.001,"kind":"equity","side":"buy","symbol":"SPY",
			"thesis":"bounded test","strategy_id":"system_acceptance",
			"invalidation":"cancel the test","confidence_bps":7000,
			"schema_revision":1
		},
		"effect_id":"637bf652-2a5a-48f3-b7cd-2bd74e03e0a6",
		"candidate_id":"a7b9928c-a753-4f4f-ac37-4f6988572767",
		"authorized_at":"2026-07-24T08:16:28.603966Z",
		"record_digest":"7a24831c3b4a9641e4eba7988d4566c7b12b52b5e1c337aee3c6fdb4a98a9b9c",
		"schema_revision":1,
		"authorization_id":"6f7a095c-4b9e-44b0-9ec6-b61b8252e8ed",
		"review_generation":1,
		"authorization_kind":"agentic",
		"kernel_mode_generation":12
	}`)
	var value PaperEffectAuthorization
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("trailing JSON err=%v", err)
	}
	if err := validatePaperEffectAuthorization(value); err != nil {
		t.Fatalf("value=%+v err=%v", value, err)
	}
}

func TestPaperEffectAuthorizationDatabaseReplay(t *testing.T) {
	databaseURL := os.Getenv("CORTEX_TEST_DATABASE_URL")
	candidateID := os.Getenv("CORTEX_TEST_PAPER_CANDIDATE_ID")
	if databaseURL == "" || candidateID == "" {
		t.Skip("database replay fixture not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adapter := &PostgresAdapter{db: db}
	authorization, err := adapter.AuthorizePaperEffect(
		context.Background(), "owner-1", candidateID, "agentic", 12,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !authorization.Replay {
		t.Fatal("existing authorization was not replayed")
	}
}

func TestPaperEffectReceiptDatabaseReplay(t *testing.T) {
	databaseURL := os.Getenv("CORTEX_TEST_DATABASE_URL")
	authorizationID := os.Getenv(
		"CORTEX_TEST_PAPER_AUTHORIZATION_ID",
	)
	if databaseURL == "" || authorizationID == "" {
		t.Skip("database receipt replay fixture not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	adapter := &PostgresAdapter{db: db}
	receipt, err := adapter.RecordPaperEffectReceipt(
		context.Background(), authorizationID, "failed", 502,
		json.RawMessage(`{"error":"market data unavailable"}`),
		"kernel_market_data_unavailable",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Replay || receipt.Outcome != "failed" {
		t.Fatalf("receipt=%+v", receipt)
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
