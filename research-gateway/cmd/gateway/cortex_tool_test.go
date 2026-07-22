package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func toolDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestValidCortexToolResultRequiresDurableReceiptAndEvidence(t *testing.T) {
	now := time.Date(2026, 7, 21, 20, 0, 0, 0, time.UTC)
	auth := cortexWebFetchAuthorization{ToolCallID: "tool-1", ToolID: "research_web_fetch", RequestDigest: toolDigest("request"), URL: "https://example.com/research", MaxChars: 12000}
	var result cortexToolResult
	result.Receipt.SchemaRevision = 1
	result.Receipt.ReceiptID = "receipt-1"
	result.Receipt.ToolCallID = "tool-1"
	result.Receipt.ToolID = "research_web_fetch"
	result.Receipt.RequestDigest = auth.RequestDigest
	result.Receipt.State = "succeeded"
	result.Receipt.Evidence.Owner = "research_gateway"
	result.Receipt.Evidence.RecordType = "web_fetch_evidence"
	result.Receipt.Evidence.RecordID = "evidence-1"
	result.Receipt.Evidence.Schema = 1
	result.Receipt.Evidence.RecordDigest = toolDigest("evidence-record")
	result.Receipt.Executor.PrincipalID = "research-gateway-1"
	result.Receipt.Executor.Kind = "workload"
	result.Receipt.Executor.Audience = "research_gateway"
	result.Receipt.CompletedAt = now
	result.Evidence.SchemaRevision = 1
	result.Evidence.EvidenceID = "evidence-1"
	result.Evidence.ToolCallID = "tool-1"
	result.Evidence.Source = "web-page-untrusted"
	result.Evidence.URL = "https://example.com/research"
	result.Evidence.ContentType = "text/html"
	result.Evidence.Text = "Untrusted evidence."
	result.Evidence.ContentDigest = toolDigest("evidence")
	result.Evidence.ObservedAt = now
	result.Evidence.AvailableAt = now
	result.Evidence.ArchivedAt = now
	if !validCortexToolResult(result, auth) {
		t.Fatal("valid receipt rejected")
	}
	result.Receipt.State = "unknown"
	if validCortexToolResult(result, auth) {
		t.Fatal("unknown receipt accepted")
	}
}
