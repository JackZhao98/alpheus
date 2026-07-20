package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxAgentQueryResponseBytes int64 = 1 << 20

type agentQueryInput struct {
	Symbol string `json:"symbol"`
	Query  string `json:"query"`
}

// postAgentQuery is a non-trading MVP bridge. Kernel authenticates the user,
// Runtime receives only Kernel's service token, and the response cannot emit
// an operation through this path.
func (s *server) postAgentQuery(w http.ResponseWriter, r *http.Request) {
	var input agentQueryInput
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Query = strings.TrimSpace(input.Query)
	if !validAgentQuerySymbol(input.Symbol) || input.Query == "" || len(input.Query) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "symbol and query are required"})
		return
	}

	body, err := json.Marshal(input)
	if err != nil {
		writeInternalError(w, "agent query encode", err)
		return
	}
	runtimeURL := strings.TrimRight(s.runtimeURL, "/")
	if runtimeURL == "" {
		runtimeURL = "http://agent-runtime:8200"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, runtimeURL+"/query", bytes.NewReader(body))
	if err != nil {
		writeInternalError(w, "agent query request", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.mode.KernelToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.mode.KernelToken)
	}
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 130 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent runtime unavailable"})
		return
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes+1))
	if err != nil || int64(len(raw)) > maxAgentQueryResponseBytes {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent query failed"})
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent query failed"})
		return
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent query failed"})
		return
	}
	if s.store != nil {
		s.store.Event("agent_query", map[string]string{"role": "scout", "symbol": input.Symbol, "subject": authenticatedSubject(r)})
	}
	writeJSON(w, http.StatusOK, output)
}

func validAgentQuerySymbol(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 16 {
		return false
	}
	for _, char := range symbol {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}
