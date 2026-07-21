package main

import "testing"

func TestReservedInputTokensUsesBoundedConservativeEstimate(t *testing.T) {
	if got := reservedInputTokens([]byte("small request")); got != 2074 {
		t.Fatalf("reservedInputTokens=%d", got)
	}
	if got := reservedInputTokens(make([]byte, 600000)); got != 1000000 {
		t.Fatalf("reservation cap=%d", got)
	}
}

func TestExtractOutputRejectsMissingContractOutput(t *testing.T) {
	if _, err := extractOutput(openAIResponse{}); err == nil {
		t.Fatal("missing structured output was accepted")
	}
	response := openAIResponse{}
	response.Output = append(response.Output, struct {
		Type, Role string
		Content    []struct{ Type, Text, Refusal string }
	}{Type: "message", Content: []struct{ Type, Text, Refusal string }{{Type: "output_text", Text: `{"text":"ok"}`}}})
	got, err := extractOutput(response)
	if err != nil || string(got) != `{"text":"ok"}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
}

func TestParseWorkflowOutputEnforcesSemanticRoute(t *testing.T) {
	answer, err := parseWorkflowOutput([]byte(`{"kind":"answer","target":"user","objective":"answer directly","rationale":"simple request","text":"hello"}`))
	if err != nil || answer.Kind != "answer" {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
	handoff, err := parseWorkflowOutput([]byte(`{"kind":"handoff","target":"desk","objective":"assess investment case","rationale":"requires analysis","text":""}`))
	if err != nil || handoff.Kind != "handoff" {
		t.Fatalf("handoff=%+v err=%v", handoff, err)
	}
	if _, err := parseWorkflowOutput([]byte(`{"kind":"handoff","target":"user","objective":"bad","rationale":"bad","text":""}`)); err == nil {
		t.Fatal("invalid handoff route was accepted")
	}
}
