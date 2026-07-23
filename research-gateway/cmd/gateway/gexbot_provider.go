package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const maxGEXBOTProviderResponseBytes = 64 << 10

// configureGEXBOTProvider attaches the separate raw-data Provider underneath
// Research Gateway. The Gateway gets only a read token and returns bounded,
// normalized observation records; it has neither the upstream GEXBOT API key
// nor direct write access to the Provider archive.
func (g *gateway) configureGEXBOTProvider() error {
	url := strings.TrimRight(strings.TrimSpace(os.Getenv("GEXBOT_PROVIDER_URL")), "/")
	tokenPath := strings.TrimSpace(os.Getenv("GEXBOT_PROVIDER_READ_TOKEN_FILE"))
	if url == "" && tokenPath == "" {
		return nil
	}
	if url == "" || tokenPath == "" || g.cortexToken == "" {
		return fmt.Errorf("GEXBOT Provider and Cortex tool configuration must be configured together")
	}
	token, err := readGatewaySecret(tokenPath)
	if err != nil {
		return fmt.Errorf("load GEXBOT Provider read token: %w", err)
	}
	g.gexbotURL, g.gexbotToken = url, token
	return nil
}

func (g *gateway) cortexGEXBOTAsOf(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Symbol   string `json:"symbol"`
		Category string `json:"category"`
		AsOf     string `json:"as_of"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	if !safeSymbol(strings.ToUpper(strings.TrimSpace(input.Symbol))) || !validGEXBOTCategory(strings.TrimSpace(input.Category)) || strings.TrimSpace(input.AsOf) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT as_of query"})
		return
	}
	g.proxyGEXBOT(w, r, "/v1/as-of", input)
}

func (g *gateway) cortexGEXBOTReplay(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
		Symbol    string `json:"symbol"`
		Category  string `json:"category"`
		Start     string `json:"start_available_at"`
		End       string `json:"end_available_at"`
		AsOf      string `json:"as_of"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.RequestID) == "" || !safeSymbol(strings.ToUpper(strings.TrimSpace(input.Symbol))) || !validGEXBOTCategory(strings.TrimSpace(input.Category)) ||
		strings.TrimSpace(input.Start) == "" || strings.TrimSpace(input.End) == "" || strings.TrimSpace(input.AsOf) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT replay request"})
		return
	}
	g.proxyGEXBOT(w, r, "/v1/replays", input)
}

func (g *gateway) cortexGEXBOTReplayNext(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) || !gatewayIdentifier(strings.TrimSpace(r.PathValue("id"))) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Generation int64 `json:"generation"`
	}
	if !decodeGatewayJSON(w, r, &input) || input.Generation < 1 {
		return
	}
	g.proxyGEXBOT(w, r, "/v1/replays/"+strings.TrimSpace(r.PathValue("id"))+"/next", input)
}

func (g *gateway) validCortexToolToken(r *http.Request) bool {
	return g != nil && g.gexbotURL != "" && g.gexbotToken != "" && g.cortexToken != "" && tokenMatches(bearerToken(r), g.cortexToken)
}

func (g *gateway) proxyGEXBOT(w http.ResponseWriter, r *http.Request, path string, input any) {
	body, err := json.Marshal(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.gexbotURL+path, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider unavailable"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+g.gexbotToken)
	req.Header.Set("Content-Type", "application/json")
	response, err := g.http.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider unavailable"})
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxGEXBOTProviderResponseBytes+1))
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusConflict {
		writeJSON(w, response.StatusCode, map[string]string{"error": "GEXBOT Provider rejected the query"})
		return
	}
	if err != nil || len(raw) == 0 || len(raw) > maxGEXBOTProviderResponseBytes || !json.Valid(raw) || response.StatusCode/100 != 2 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider response unavailable"})
		return
	}
	// Provider responses deliberately contain only compact typed observation
	// fields and Blob metadata. Reject a raw-payload leak at this boundary.
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value["payload"] != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider response invalid"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func validGEXBOTCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" || value == "gex_one"
}
