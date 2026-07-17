package units

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMicrosJSONRoundTripIsExact(t *testing.T) {
	for _, input := range []string{"0.000001", "0.1", "10.00", "105", "123456.654321"} {
		var got Micros
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal %s: %v", input, err)
		}
		var roundTrip Micros
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("round trip %s: %v", input, err)
		}
		if roundTrip != got {
			t.Fatalf("%s => %d => %d", input, got, roundTrip)
		}
	}
}

func TestExactDecimalRejectsExponentAndExtraPrecision(t *testing.T) {
	for _, input := range []string{"1e-6", "1E2", "0.0000001"} {
		var got Micros
		if err := json.Unmarshal([]byte(input), &got); err == nil {
			t.Fatalf("%s was accepted", input)
		}
	}
}

func TestQtyWireUnitIsHumanUnit(t *testing.T) {
	var qty Qty
	if err := json.Unmarshal([]byte("1"), &qty); err != nil {
		t.Fatal(err)
	}
	if qty != Qty(Scale) {
		t.Fatalf("qty=%d, want %d", qty, Scale)
	}
}

func TestYAMLUsesExactScalarText(t *testing.T) {
	var value struct {
		Percent PercentMicros `yaml:"percent"`
		Ratio   RatioMicros   `yaml:"ratio"`
		Money   Micros        `yaml:"money"`
	}
	if err := yaml.Unmarshal([]byte("percent: 35\nratio: 0.15\nmoney: 0.01\n"), &value); err != nil {
		t.Fatal(err)
	}
	if value.Percent != 35*PercentMicros(Scale) || value.Ratio != 150_000 || value.Money != 10_000 {
		t.Fatalf("decoded=%+v", value)
	}
}

func TestMulQtyPriceRoundingAndOverflow(t *testing.T) {
	down, err := MulQtyPrice(MustQty("0.333333"), MustMicros("1"), 1, false)
	if err != nil {
		t.Fatal(err)
	}
	up, err := MulQtyPrice(MustQty("0.333333"), MustMicros("1"), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if down != MustMicros("0.333333") || up != MustMicros("0.333333") {
		t.Fatalf("down=%s up=%s", down, up)
	}
	if _, err := MulQtyPrice(Qty(^uint64(0)>>1), Micros(^uint64(0)>>1), 100, true); err == nil {
		t.Fatal("overflow was accepted")
	}
}

func TestExactRiskComparisons(t *testing.T) {
	equity := MustMicros("123.456789")
	perTradeCap, err := PercentFloor(equity, MustPercent("35"))
	if err != nil {
		t.Fatal(err)
	}
	if perTradeCap != MustMicros("43.209876") {
		t.Fatalf("35%% cap=%s, want 43.209876", perTradeCap)
	}
	if !SumLessOrEqual(0, MustMicros("43.209876"), perTradeCap) ||
		SumLessOrEqual(0, MustMicros("43.209877"), perTradeCap) {
		t.Fatal("35% cap is not exact at the one-micro boundary")
	}

	totalCap, err := PercentFloor(equity, MustPercent("80"))
	if err != nil {
		t.Fatal(err)
	}
	if totalCap != MustMicros("98.765431") {
		t.Fatalf("80%% cap=%s, want 98.765431", totalCap)
	}
	if !SumLessOrEqual(0, MustMicros("98.765431"), totalCap) ||
		SumLessOrEqual(0, MustMicros("98.765432"), totalCap) {
		t.Fatal("80% cap is not exact at the one-micro boundary")
	}

	maximum := MustRatio("0.15")
	if !SpreadWithin(MustMicros("37"), MustMicros("43"), maximum) {
		t.Fatal("exact 0.15 spread boundary was rejected")
	}
	if !SpreadWithin(MustMicros("37"), MustMicros("42.999999"), maximum) {
		t.Fatal("one micro inside spread boundary was rejected")
	}
	if SpreadWithin(MustMicros("37"), MustMicros("43.000001"), maximum) {
		t.Fatal("one micro outside spread boundary was accepted")
	}
}
