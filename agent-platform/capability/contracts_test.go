package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

func capabilityDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestCrossPlaneWebFetchContractsFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	result := contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: "model_call_result", RecordID: "result-1", SchemaRevision: 1, RecordDigest: capabilityDigest("result")}
	intent := ToolCallIntent{SchemaRevision: 1, ToolCallID: "tool-1", ToolID: ToolResearchWebFetch, SourceResult: result,
		Request: WebFetchRequest{URL: "https://example.com/research", MaxChars: 12000}, RequestDigest: capabilityDigest("request"),
		AuthorizedBy: contracts.AuditActor{PrincipalID: "cortex-control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}, AuthorizedAt: now}
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence := WebFetchEvidence{SchemaRevision: 1, EvidenceID: "evidence-1", ToolCallID: "tool-1", Source: "web-page-untrusted", URL: "https://example.com/research", ContentType: "text/html", Text: "Untrusted source text.", ContentDigest: capabilityDigest("evidence"), ObservedAt: now, AvailableAt: now, ArchivedAt: now}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt := ToolReceipt{SchemaRevision: 1, ReceiptID: "receipt-1", ToolCallID: "tool-1", ToolID: ToolResearchWebFetch, RequestDigest: intent.RequestDigest, State: ReceiptSucceeded,
		Evidence: contracts.RecordRef{Owner: contracts.OwnerResearchGateway, RecordType: "web_fetch_evidence", RecordID: "evidence-1", SchemaRevision: 1, RecordDigest: capabilityDigest("evidence-record")},
		Executor: contracts.AuditActor{PrincipalID: "research-gateway-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceResearchGateway}, CompletedAt: now}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}

	intent.Request.URL = "http://127.0.0.1/private"
	if err := intent.Validate(); err == nil {
		t.Fatal("private URL was accepted by contract")
	}
	evidence.ArchivedAt = now.Add(-time.Second)
	if err := evidence.Validate(); err == nil {
		t.Fatal("time-inverted evidence was accepted")
	}
	receipt.State = "unknown"
	if err := receipt.Validate(); err == nil {
		t.Fatal("non-success receipt was accepted")
	}
}

func TestGEXBOTLiveContractsKeepSourceAndFetchTimeDistinct(t *testing.T) {
	source := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 7, 23, 9, 44, 37, 0, time.UTC)
	fetched := observed.Add(time.Second)
	available := fetched.Add(time.Millisecond)
	spot := "7498.48"
	evidence := GEXBOTLiveEvidence{
		SchemaRevision: 1, EvidenceID: "evidence-live-1", ToolCallID: "tool-live-1", Provider: "gexbot_classic",
		Symbol: "SPX", Category: "gex_full", ObservationID: "bb2ced01-d133-55cf-887a-4874ef708dc7",
		ObservationDigest: capabilityDigest("observation"), SourceTimestamp: source, ObservedAt: observed, FetchedAt: fetched,
		AvailableAt: available, Metrics: GEXBOTMetrics{Spot: &spot},
		Raw: GEXBOTRawMetadata{BlobID: "66e2f5f0-1536-4ad8-a0cf-44ef8659efa4", ContentDigest: capabilityDigest("raw"), SizeBytes: 6859},
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt := GEXBOTLiveToolReceipt{
		SchemaRevision: 1, ReceiptID: "receipt-live-1", ToolCallID: evidence.ToolCallID, ToolID: ToolMarketGEXBOTLive,
		RequestDigest: capabilityDigest("request"), State: ReceiptSucceeded,
		Evidence:    contracts.RecordRef{Owner: contracts.OwnerResearchGateway, RecordType: "gexbot_live_evidence", RecordID: evidence.EvidenceID, SchemaRevision: 1, RecordDigest: capabilityDigest("evidence")},
		Executor:    contracts.AuditActor{PrincipalID: "research-gateway-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceResearchGateway},
		CompletedAt: available,
	}
	if err := receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence.SourceTimestamp = time.Time{}
	if err := evidence.Validate(); err == nil {
		t.Fatal("live evidence without provider source time was accepted")
	}
}
