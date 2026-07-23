package capability

import (
	"strings"
	"testing"
	"time"
)

func TestKernelEarningsObservationFailsClosed(t *testing.T) {
	actual := "0.50"
	reportDate := "2026-07-22"
	timing := "pm"
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	observation := KernelEarningsObservation{
		SchemaRevision: 1, ToolCallID: "tool-1", ToolID: ToolKernelEarningsResults,
		RequestDigest: strings.Repeat("a", 64), Provider: "kernel_robinhood_mcp", Symbol: "TSLA", Found: true,
		Results:    []KernelEarningsItem{{Symbol: "TSLA", Year: 2026, Quarter: 2, EPS: KernelEarningsEPS{Actual: &actual}, Report: &KernelEarningsReportTime{Date: &reportDate, Timing: &timing, Verified: true}}},
		ObservedAt: now, AvailableAt: now,
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("valid Kernel earnings observation rejected: %v", err)
	}
	observation.Results[0].Symbol = "AAPL"
	if observation.Validate() == nil {
		t.Fatal("mismatched earnings symbol accepted")
	}
}
