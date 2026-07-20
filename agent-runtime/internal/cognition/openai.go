package cognition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const openAIBaseURL = "https://api.openai.com"

// openAITransport implements the same bounded completion port as Anthropic.
// It intentionally uses only the Responses API and its exact input-token
// counter so provider choice cannot bypass Alpheus' existing budget gate.
type openAITransport struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func newOpenAITransport(apiKey string) *openAITransport {
	return &openAITransport{
		apiKey:  apiKey,
		baseURL: openAIBaseURL,
		client:  http.DefaultClient,
	}
}

func openAIRequestBody(req completionRequest) map[string]any {
	return map[string]any{
		"model":        req.Model,
		"instructions": req.System,
		"input":        req.User,
		"tools": []any{map[string]any{
			"type":        "function",
			"name":        req.ToolName,
			"description": "Return exactly one schema-constrained contract for this role.",
			"parameters":  req.Schema,
			"strict":      false,
		}},
		"tool_choice": map[string]any{
			"type": "function",
			"name": req.ToolName,
		},
		"parallel_tool_calls": false,
	}
}

func (t *openAITransport) CountTokens(ctx context.Context, req completionRequest) (int64, error) {
	var response struct {
		InputTokens int64 `json:"input_tokens"`
	}
	if err := t.postJSON(ctx, "/v1/responses/input_tokens", openAIRequestBody(req), &response); err != nil {
		return 0, err
	}
	if response.InputTokens <= 0 {
		return 0, fmt.Errorf("OpenAI returned invalid input token count %d", response.InputTokens)
	}
	return response.InputTokens, nil
}

func (t *openAITransport) Complete(ctx context.Context, req completionRequest) (completionResponse, error) {
	body := openAIRequestBody(req)
	body["max_output_tokens"] = 2000
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := t.postJSON(ctx, "/v1/responses", body, &response); err != nil {
		return completionResponse{}, err
	}
	var result *completionResponse
	for _, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		if item.Name != req.ToolName {
			return completionResponse{}, fmt.Errorf("model called unexpected tool %q", item.Name)
		}
		if result != nil {
			return completionResponse{}, fmt.Errorf("model called required tool more than once")
		}
		arguments := json.RawMessage(item.Arguments)
		if !json.Valid(arguments) {
			return completionResponse{}, fmt.Errorf("model returned invalid JSON tool arguments")
		}
		result = &completionResponse{
			ToolName:     item.Name,
			Input:        arguments,
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		}
	}
	if result == nil {
		return completionResponse{}, fmt.Errorf("model did not call required tool %q", req.ToolName)
	}
	return *result, nil
}

func (t *openAITransport) postJSON(ctx context.Context, path string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode OpenAI request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create OpenAI request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("call OpenAI API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("OpenAI API %s returned HTTP %d", path, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode OpenAI response: %w", err)
	}
	return nil
}
