package cognition

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestOpenAITransportUsesResponsesAndExactTokenCounter(t *testing.T) {
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("headers=%v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "gpt-5.6-sol" || body["instructions"] != "system" || body["input"] != "user" {
			t.Fatalf("body=%v", body)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 || tools[0].(map[string]any)["strict"] != false {
			t.Fatalf("tools=%v", body["tools"])
		}
		choice := body["tool_choice"].(map[string]any)
		if choice["type"] != "function" || choice["name"] != structuredOutputTool || body["parallel_tool_calls"] != false {
			t.Fatalf("tool contract=%v", body)
		}
		switch r.URL.Path {
		case "/v1/responses/input_tokens":
			return jsonHTTPResponse(http.StatusOK, `{"object":"response.input_tokens","input_tokens":321}`), nil
		case "/v1/responses":
			return jsonHTTPResponse(http.StatusOK, `{"output":[{"type":"function_call","name":"submit_contract","arguments":"{\"action\":\"PASS\"}"}],"usage":{"input_tokens":321,"output_tokens":17}}`), nil
		default:
			return jsonHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})}

	transport := newOpenAITransport("test-key")
	transport.client = client
	request := completionRequest{
		Model: "gpt-5.6-sol", System: "system", User: "user",
		ToolName: structuredOutputTool,
		Schema:   map[string]any{"type": "object"},
	}
	count, err := transport.CountTokens(context.Background(), request)
	if err != nil || count != 321 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	response, err := transport.Complete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.ToolName != structuredOutputTool || string(response.Input) != `{"action":"PASS"}` || response.InputTokens != 321 || response.OutputTokens != 17 {
		t.Fatalf("response=%+v", response)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses/input_tokens" || paths[1] != "/v1/responses" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestOpenAITransportFailsClosedOnHTTPError(t *testing.T) {
	transport := newOpenAITransport("test-key")
	transport.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(http.StatusUnauthorized, `{"error":{"message":"bad key"}}`), nil
	})}
	_, err := transport.CountTokens(context.Background(), completionRequest{Model: "gpt-5.6-sol"})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}
