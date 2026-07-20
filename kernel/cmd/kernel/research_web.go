package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type researchWebSearch struct {
	Available   bool      `json:"available"`
	Source      string    `json:"source"`
	Query       string    `json:"query"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Items       []struct {
		Title       string   `json:"title"`
		URL         string   `json:"url"`
		Description string   `json:"description,omitempty"`
		PageAge     string   `json:"page_age,omitempty"`
		Snippets    []string `json:"snippets,omitempty"`
	} `json:"items"`
}

type researchWebPage struct {
	Available   bool      `json:"available"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	ContentType string    `json:"content_type"`
	Text        string    `json:"text"`
	Truncated   bool      `json:"truncated"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

func (s *server) getResearchWebSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	count := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid search count"})
			return
		}
		count = parsed
	}
	if query == "" || len(query) > 500 || count < 1 || count > 10 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid web search"})
		return
	}
	apiKey, err := s.loadAgentSecret("brave")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Brave search is not configured"})
		return
	}
	var document researchWebSearch
	if err := s.callResearchGateway(r.Context(), "/v1/web/search", map[string]any{"query": query, "count": count, "api_key": apiKey}, &document); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search unavailable"})
		return
	}
	if !validResearchWebSearch(query, count, document) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search response invalid"})
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *server) getResearchWebFetch(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
	maxChars := 12_000
	if raw := strings.TrimSpace(r.URL.Query().Get("max_chars")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid max_chars"})
			return
		}
		maxChars = parsed
	}
	if len(rawURL) == 0 || len(rawURL) > 4000 || maxChars < 1 || maxChars > 20_000 || !validResearchExternalURL(rawURL) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid web URL"})
		return
	}
	var document researchWebPage
	if err := s.callResearchGateway(r.Context(), "/v1/web/fetch", map[string]any{"url": rawURL, "max_chars": maxChars}, &document); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web page unavailable"})
		return
	}
	if !document.Available || document.Source != "web-page-untrusted" || document.RetrievedAt.IsZero() || !validResearchExternalURL(document.URL) ||
		len(document.URL) > 4000 || len(document.Title) > 1000 || len(document.Text) > maxChars || !validResearchWebMediaType(document.ContentType) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web page response invalid"})
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *server) callResearchGateway(parent context.Context, path string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.researchURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if s.mode.KernelToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.mode.KernelToken)
	}
	client := s.researchHTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("research gateway HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResearchResponseBytes+1))
	if err != nil || int64(len(raw)) > maxResearchResponseBytes {
		return fmt.Errorf("research gateway response too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid research gateway response")
	}
	return nil
}

func validResearchWebSearch(query string, count int, document researchWebSearch) bool {
	if !document.Available || document.Source != "brave-web" || document.Query != query || document.RetrievedAt.IsZero() || len(document.Items) > count {
		return false
	}
	for _, item := range document.Items {
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > 1000 || !validResearchExternalURL(item.URL) || len(item.URL) > 4000 ||
			len(item.Description) > 2000 || len(item.PageAge) > 100 || len(item.Snippets) > 3 {
			return false
		}
		for _, snippet := range item.Snippets {
			if len(snippet) > 1500 {
				return false
			}
		}
	}
	return true
}

func validResearchExternalURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func validResearchWebMediaType(value string) bool {
	switch value {
	case "text/html", "application/xhtml+xml", "text/plain", "application/json":
		return true
	default:
		return false
	}
}
