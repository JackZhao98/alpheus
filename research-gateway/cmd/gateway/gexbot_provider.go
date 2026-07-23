package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
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
	if g.moodyBlues == nil {
		g.moodyBlues = newMoodyBluesRegistry()
	}
	if err := g.moodyBlues.register(gexbotMoodyBluesProvider()); err != nil {
		return fmt.Errorf("register GEXBOT with Moody Blues: %w", err)
	}
	return nil
}

func (g *gateway) cortexGEXBOTAsOf(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "as_of") {
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
	asOf, ok := canonicalMoodyBluesTime(input.AsOf, time.Now())
	if !safeSymbol(strings.ToUpper(strings.TrimSpace(input.Symbol))) || !validGEXBOTCategory(strings.TrimSpace(input.Category)) || !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT as_of query"})
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	input.AsOf = asOf.Format(time.RFC3339Nano)
	g.proxyGEXBOT(w, r, "/v1/as-of", input)
}

func (g *gateway) cortexGEXBOTLive(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "live") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Symbol   string `json:"symbol"`
		Category string `json:"category"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	if input.Symbol != "SPX" || !validGEXBOTCategory(input.Category) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT live query"})
		return
	}
	g.proxyGEXBOTLive(w, r, input)
}

func (g *gateway) cortexGEXBOTReplay(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "replay") {
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
	start, startOK := canonicalMoodyBluesTime(input.Start, time.Now())
	end, endOK := canonicalMoodyBluesTime(input.End, time.Now())
	asOf, asOfOK := canonicalMoodyBluesTime(input.AsOf, time.Now())
	if strings.TrimSpace(input.RequestID) == "" || !safeSymbol(strings.ToUpper(strings.TrimSpace(input.Symbol))) || !validGEXBOTCategory(strings.TrimSpace(input.Category)) ||
		!startOK || !endOK || !asOfOK || end.Before(start) || asOf.Before(end) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT replay request"})
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	input.Start = start.Format(time.RFC3339Nano)
	input.End = end.Format(time.RFC3339Nano)
	input.AsOf = asOf.Format(time.RFC3339Nano)
	g.proxyGEXBOT(w, r, "/v1/replays", input)
}

func (g *gateway) cortexGEXBOTReplayNext(w http.ResponseWriter, r *http.Request) {
	if !g.validCortexToolToken(r) || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "replay") || !gatewayIdentifier(strings.TrimSpace(r.PathValue("id"))) {
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
	return g != nil && g.gexbotURL != "" && g.gexbotToken != "" && g.validCortexToken(r)
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

func (g *gateway) proxyGEXBOTLive(w http.ResponseWriter, r *http.Request, input any) {
	body, err := json.Marshal(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.gexbotURL+"/v1/live", bytes.NewReader(body))
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
	if err != nil || response.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > maxGEXBOTProviderResponseBytes {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live response unavailable"})
		return
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value["payload"] != nil || value["available"] != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live response invalid"})
		return
	}
	value["available"] = json.RawMessage("true")
	normalized, err := json.Marshal(value)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live response invalid"})
		return
	}
	compacted, err := compactGEXBOTObservation(normalized)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live transform failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(compacted)
}

func validGEXBOTCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" || value == "gex_one"
}
