package canonical

import (
	"os"
	"strings"
	"testing"
)

func TestGoldenV1(t *testing.T) {
	input, err := os.ReadFile("testdata/v1-input.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/v1-canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	want = []byte(strings.TrimSuffix(string(want), "\n"))

	got, err := JSON(input)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("canonical JSON mismatch\n got: %s\nwant: %s", got, want)
	}
	digest, err := DigestJSON("agent-platform.contract.golden", input)
	if err != nil {
		t.Fatalf("DigestJSON: %v", err)
	}
	wantDigestRaw, err := os.ReadFile("testdata/v1.sha256")
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := strings.TrimSpace(string(wantDigestRaw))
	if digest != wantDigest {
		t.Fatalf("digest=%s want=%s", digest, wantDigest)
	}
}

func TestCanonicalRejectsAmbiguousInput(t *testing.T) {
	tests := map[string][]byte{
		"duplicate key": {123, 34, 97, 34, 58, 49, 44, 34, 97, 34, 58, 50, 125},
		"float":         []byte(`{"n":1.0}`),
		"exponent":      []byte(`{"n":1e2}`),
		"negative zero": []byte(`{"n":-0}`),
		"trailing":      []byte(`{} []`),
		"invalid utf8":  {123, 34, 120, 34, 58, 34, 0xff, 34, 125},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := JSON(input); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDigestRequiresDomain(t *testing.T) {
	if _, err := DigestJSON("", []byte(`{}`)); err == nil {
		t.Fatal("expected empty domain rejection")
	}
	if _, err := DigestJSON("Different.Domain", []byte(`{}`)); err == nil {
		t.Fatal("expected non-profile domain rejection")
	}
}
