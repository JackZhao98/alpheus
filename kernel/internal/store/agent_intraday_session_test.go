package store

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentIntradaySessionContractsRejectUnsafeProjection(t *testing.T) {
	start := time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC)
	valid := AgentIntradaySessionCreate{
		Subject: "owner", Environment: "paper",
		RequestID:  "console-replay-1",
		ReplayID:   "11111111-1111-4111-8111-111111111111",
		ProviderID: "gexbot-classic", Symbol: "SPX",
		Category: "gex_full", StartAvailableAt: start,
		EndAvailableAt: start.Add(time.Hour),
		AsOf:           start.Add(2 * time.Hour), State: "active",
		ReplayGeneration: 1,
		Payload:          json.RawMessage(`{"state":"active","generation":1}`),
	}
	normalizeAgentIntradayCreate(&valid)
	if !validAgentIntradayCreate(valid) {
		t.Fatal("valid intraday Session contract rejected")
	}
	unsafe := valid
	unsafe.Environment = "live-now"
	if validAgentIntradayCreate(unsafe) {
		t.Fatal("unknown environment accepted")
	}
	unsafe = valid
	unsafe.Payload = json.RawMessage(`["raw-provider-payload"]`)
	if validAgentIntradayCreate(unsafe) {
		t.Fatal("non-object projection payload accepted")
	}
	unsafe = valid
	unsafe.EndAvailableAt = unsafe.StartAvailableAt.Add(-time.Second)
	if validAgentIntradayCreate(unsafe) {
		t.Fatal("reversed temporal boundary accepted")
	}
}

func TestAgentIntradayFrameRequiresBoundedWakeReference(t *testing.T) {
	frame := AgentIntradaySessionFrame{
		Subject:  "owner",
		ReplayID: "11111111-1111-4111-8111-111111111111",
		State:    "active", ReplayGeneration: 2,
		LatestWakeRunID: "22222222-2222-4222-8222-222222222222",
		Payload: json.RawMessage(
			`{"state":"active","generation":2}`,
		),
	}
	if !validAgentIntradayFrame(frame) {
		t.Fatal("valid intraday frame rejected")
	}
	frame.LatestWakeRunID = "not-a-run"
	if validAgentIntradayFrame(frame) {
		t.Fatal("invalid Wake Run reference accepted")
	}
}
