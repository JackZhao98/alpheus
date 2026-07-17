package contracts

import (
	"encoding/json"
	"testing"
)

func number(value string) json.Number { return json.Number(value) }

func TestCloseContractDoesNotTrustSide(t *testing.T) {
	valid := ProposedOperation{Action: "close", Symbol: "SPY", Qty: number("1")}
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

func TestTradingContractRejectsInvalidExactQuantity(t *testing.T) {
	for _, qty := range []string{"0", "-1", "1e-6", "0.0000001"} {
		op := ProposedOperation{Action: "close", Symbol: "SPY", Qty: number(qty)}
		if err := op.Validate(); err == nil {
			t.Fatalf("close qty %s passed validation", qty)
		}
	}
}

func TestOptionQuantityMustBeWhole(t *testing.T) {
	op := ProposedOperation{
		Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY",
		Side: "buy", Qty: number("1.5"), Plan: &ExitPlan{},
	}
	if err := op.Validate(); err == nil {
		t.Fatal("fractional option quantity passed")
	}
	op.Kind = "equity"
	if err := op.Validate(); err != nil {
		t.Fatalf("fractional equity quantity failed: %v", err)
	}
}

func TestExplicitZeroRiskDeclarationIsPreserved(t *testing.T) {
	zero := number("0")
	op := ProposedOperation{
		Action: "open", Kind: "equity", Underlying: "SPY", Symbol: "SPY",
		Side: "buy", Qty: number("1"), MaxRiskUSD: &zero, Plan: &ExitPlan{},
	}
	if err := op.Validate(); err != nil {
		t.Fatalf("explicit zero declaration rejected by contract: %v", err)
	}
	encoded, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || !json.Valid(encoded) {
		t.Fatalf("invalid JSON: %s", encoded)
	}
}

func TestTightenStopContractRejectsWhitespace(t *testing.T) {
	op := ProposedOperation{Action: "tighten_stop", Symbol: "SPY", Plan: &ExitPlan{Stop: "   "}}
	if err := op.Validate(); err == nil {
		t.Fatal("whitespace stop passed validation")
	}
}

func TestNonQuantityOperationMarshalsWithoutInvalidNumber(t *testing.T) {
	op := ProposedOperation{Action: "cancel", BrokerOrderID: "broker-1"}
	encoded, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("invalid JSON: %s", encoded)
	}
}
