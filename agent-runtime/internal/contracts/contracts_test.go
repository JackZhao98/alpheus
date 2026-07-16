package contracts

import "testing"

func TestCloseContractDoesNotTrustSide(t *testing.T) {
	valid := ProposedOperation{Action: "close", Symbol: "SPY", Qty: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("close without side: %v", err)
	}
	valid.Side = "buy"
	if err := valid.Validate(); err != nil {
		t.Fatalf("close with legacy side: %v", err)
	}
	valid.Side = "XXXX"
	if err := valid.Validate(); err == nil {
		t.Fatal("close with garbage side passed validation")
	}
}

func TestTradingContractRejectsNonPositiveQuantity(t *testing.T) {
	for _, qty := range []float64{0, -1} {
		op := ProposedOperation{Action: "close", Symbol: "SPY", Qty: qty}
		if err := op.Validate(); err == nil {
			t.Fatalf("close qty %v passed validation", qty)
		}
	}
}

func TestTightenStopContractRejectsWhitespace(t *testing.T) {
	op := ProposedOperation{Action: "tighten_stop", Symbol: "SPY", Plan: &ExitPlan{Stop: "   "}}
	if err := op.Validate(); err == nil {
		t.Fatal("whitespace stop passed validation")
	}
}
