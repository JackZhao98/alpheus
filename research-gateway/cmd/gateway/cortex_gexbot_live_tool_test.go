package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestValidCortexGEXBOTLiveResultPreservesFourTimes(t *testing.T) {
	source := time.Date(2026, 7, 22, 20, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 7, 23, 9, 44, 37, 0, time.UTC)
	fetched := observed.Add(time.Second)
	available := fetched.Add(time.Millisecond)
	auth := cortexGEXBOTLiveAuthorization{ToolCallID: "tool-live-1", ToolID: "market_gexbot_live", RequestDigest: toolDigest("request"), Symbol: "SPX", Category: "gex_full"}
	result := cortexGEXBOTLiveResult{
		Receipt: cortexGEXBOTLiveReceipt{
			SchemaRevision: 1, ReceiptID: "receipt-live-1", ToolCallID: auth.ToolCallID, ToolID: auth.ToolID,
			RequestDigest: auth.RequestDigest, State: "succeeded",
			Evidence: cortexGEXRecordRef{Owner: "research_gateway", RecordType: "gexbot_live_evidence", RecordID: "evidence-live-1", Schema: 1, RecordDigest: toolDigest("evidence")},
			Executor: cortexGEXActor{PrincipalID: "research-gateway-1", Kind: "workload", Audience: "research_gateway"}, CompletedAt: available,
		},
		Evidence: cortexGEXBOTLiveEvidence{
			SchemaRevision: 1, EvidenceID: "evidence-live-1", ToolCallID: auth.ToolCallID, Provider: "gexbot_classic",
			Symbol: "SPX", Category: "gex_full", ObservationID: "bb2ced01-d133-55cf-887a-4874ef708dc7",
			ObservationDigest: toolDigest("observation"), SourceTimestamp: source, ObservedAt: observed, FetchedAt: fetched,
			AvailableAt: available, Metrics: json.RawMessage(`{"spot":7498.48}`),
			Raw: cortexGEXRaw{BlobID: "66e2f5f0-1536-4ad8-a0cf-44ef8659efa4", ContentDigest: toolDigest("raw"), SizeBytes: 6859},
		},
	}
	if !validCortexGEXBOTLiveResult(result, auth) {
		t.Fatal("valid GEXBOT live result rejected")
	}
	result.Evidence.FetchedAt = observed.Add(-time.Second)
	if validCortexGEXBOTLiveResult(result, auth) {
		t.Fatal("time-inverted GEXBOT live result accepted")
	}
}
