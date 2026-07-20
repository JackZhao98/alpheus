package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResearchResponseBytes int64 = 1 << 20

func (s *server) getResearchNews(w http.ResponseWriter, r *http.Request) {
	symbol := strings.ToUpper(strings.TrimSpace(r.PathValue("symbol")))
	if !validAgentQuerySymbol(symbol) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid symbol"})
		return
	}
	credential, err := s.loadAgentSecret("robinhood_research")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood research is not configured"})
		return
	}
	var credentialJSON json.RawMessage
	if err := json.Unmarshal([]byte(credential), &credentialJSON); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood research credential is invalid"})
		return
	}
	body, _ := json.Marshal(map[string]any{"symbol": symbol, "credentials": credentialJSON})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.researchURL, "/")+"/v1/robinhood/news", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "research gateway unavailable"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.mode.KernelToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.mode.KernelToken)
	}
	client := s.researchHTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "research gateway unavailable"})
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResearchResponseBytes+1))
	if err != nil || int64(len(raw)) > maxResearchResponseBytes || response.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood news unavailable"})
		return
	}
	var result struct {
		News struct {
			Available   bool      `json:"available"`
			Source      string    `json:"source"`
			Symbol      string    `json:"symbol"`
			RetrievedAt time.Time `json:"retrieved_at"`
			Items       []struct {
				Title       string `json:"title"`
				Summary     string `json:"summary,omitempty"`
				URL         string `json:"url"`
				Source      string `json:"source"`
				PublishedAt string `json:"published_at"`
			} `json:"items"`
		} `json:"news"`
		RefreshedCredentials json.RawMessage `json:"refreshed_credentials"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || !validResearchNews(symbol, result.News.Available, result.News.Source, result.News.Symbol, result.News.RetrievedAt, len(result.News.Items)) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood news response invalid"})
		return
	}
	if len(result.RefreshedCredentials) != 0 && string(result.RefreshedCredentials) != "null" {
		compact, err := compactRobinhoodResearchCredential(result.RefreshedCredentials)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood credential refresh invalid"})
			return
		}
		ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, "robinhood_research", compact)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood credential refresh unavailable"})
			return
		}
		if err := s.store.PutAgentSecret("robinhood_research", ciphertext); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood credential refresh unavailable"})
			return
		}
		s.store.Event("agent_secret_refreshed", map[string]string{"provider": "robinhood_research"})
	}
	writeJSON(w, http.StatusOK, result.News)
}

func validResearchNews(symbol string, available bool, source, responseSymbol string, retrievedAt time.Time, items int) bool {
	return available && source == "robinhood-private-api" && responseSymbol == symbol && !retrievedAt.IsZero() && items >= 0 && items <= 20
}

func compactRobinhoodResearchCredential(raw json.RawMessage) (string, error) {
	var value struct {
		AccessToken  string    `json:"access_token"`
		RefreshToken string    `json:"refresh_token"`
		TokenType    string    `json:"token_type"`
		ExpiresAt    time.Time `json:"expires_at"`
		DeviceToken  string    `json:"device_token"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || value.AccessToken == "" || value.RefreshToken == "" || value.DeviceToken == "" || value.ExpiresAt.IsZero() ||
		len(value.AccessToken) > 2048 || len(value.RefreshToken) > 2048 || len(value.DeviceToken) > 256 {
		return "", errInvalidResearchCredential
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

var errInvalidResearchCredential = errors.New("invalid Robinhood research credential")
