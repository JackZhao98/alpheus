package inputgateway

import (
	"testing"
	"time"

	"alpheus/agentplatform/blob"
)

func TestDeterministicStageIDIsStableAndScoped(t *testing.T) {
	first := deterministicStageID("cortex-control-1", "request-1")
	if first != deterministicStageID("cortex-control-1", "request-1") || first == deterministicStageID("cortex-control-1", "request-2") {
		t.Fatalf("stage identity is not stable and scoped: %s", first)
	}
	if len(first) != 36 || first[14] != '5' {
		t.Fatalf("not a deterministic version-5 UUID: %s", first)
	}
}

func TestSameStageGrantComparesExpectedSizeValue(t *testing.T) {
	leftSize, rightSize := int64(4), int64(4)
	issued := time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)
	left := blob.StageGrant{SchemaRevision: 1, StageID: "11111111-1111-4111-8111-111111111111",
		PrincipalID: "cortex-control-1", MediaType: "text/plain; charset=utf-8", MaxBytes: 4,
		ExpectedDigest: "digest", ExpectedSizeBytes: &leftSize, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute)}
	right := left
	right.ExpectedSizeBytes = &rightSize
	if !sameStageGrant(left, right) {
		t.Fatal("equal stage grants with distinct pointer storage did not compare equal")
	}
	rightSize++
	if sameStageGrant(left, right) {
		t.Fatal("different expected sizes compared equal")
	}
}
