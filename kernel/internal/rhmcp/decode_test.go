package rhmcp

import "testing"

func TestDecodeExactDecimal(t *testing.T) {
	for input, want := range map[string]string{
		`"0.65000000"`: "0.650000",
		`"133.779900"`: "133.779900",
		`-1.25`:        "-1.25",
		`1`:            "1",
	} {
		got, err := DecodeExactDecimal([]byte(input), 6)
		if err != nil || got != want {
			t.Fatalf("DecodeExactDecimal(%s)=%q,%v want %q", input, got, err, want)
		}
	}
	for _, input := range []string{`"0.65000001"`, `""`, `"1e2"`, `"1."`, `" 1"`} {
		if _, err := DecodeExactDecimal([]byte(input), 6); err == nil {
			t.Fatalf("DecodeExactDecimal(%s) accepted inexact/invalid value", input)
		}
	}
}
