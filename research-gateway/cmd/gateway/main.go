// Alpheus Research Gateway owns read-only external connector calls and typed
// normalization. Credentials arrive only from the Kernel vault for one call;
// they are never logged, persisted, or returned to Agent Runtime. This first
// connector adapts the proven robinhood-cli news/refresh protocol while
// exposing no generic Robinhood request or mutation primitive.
package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	robinhoodAPIBase   = "https://api.robinhood.com"
	braveAPIBase       = "https://api.search.brave.com"
	robinhoodClientID  = "c82SH0WZOsabOXGP2sxqcj34FxkvfnWRZBKlBjFS"
	robinhoodUserAgent = "Robinhood/8232 (com.robinhood.release.Robinhood; build:8232; iOS 17.5.1)"
	maxRequestBytes    = 8 << 10
	maxUpstreamBytes   = 1 << 20
)

type credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	DeviceToken  string    `json:"device_token"`
}

type newsItem struct {
	Title       string `json:"title"`
	Summary     string `json:"summary,omitempty"`
	URL         string `json:"url"`
	Source      string `json:"source"`
	PublishedAt string `json:"published_at"`
}

type newsDocument struct {
	Available   bool       `json:"available"`
	Source      string     `json:"source"`
	Symbol      string     `json:"symbol"`
	RetrievedAt time.Time  `json:"retrieved_at"`
	Items       []newsItem `json:"items"`
}

type gateway struct {
	token       string
	http        *http.Client
	base        string
	braveBase   string
	lookupIP    lookupIPFunc
	dialContext dialContextFunc
}

func main() {
	token := strings.TrimSpace(os.Getenv("KERNEL_TOKEN"))
	if token == "" {
		log.Fatal("KERNEL_TOKEN is required")
	}
	g := newGateway(token)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/robinhood/news", g.news)
	mux.HandleFunc("POST /v1/web/search", g.webSearch)
	mux.HandleFunc("POST /v1/web/fetch", g.webFetch)
	log.Printf("research-gateway listening on :8300")
	log.Fatal(http.ListenAndServe(":8300", mux))
}

func (g *gateway) news(w http.ResponseWriter, r *http.Request) {
	if !tokenMatches(bearerToken(r), g.token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var input struct {
		Symbol      string          `json:"symbol"`
		Credentials json.RawMessage `json:"credentials"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	if !safeSymbol(input.Symbol) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid symbol"})
		return
	}
	var credential credentials
	if err := json.Unmarshal(input.Credentials, &credential); err != nil || !validCredentials(credential) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid research credential"})
		return
	}
	refreshed := false
	if time.Now().UTC().After(credential.ExpiresAt.Add(-time.Minute)) {
		var err error
		credential, err = g.refresh(r, credential)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "research authentication unavailable"})
			return
		}
		refreshed = true
	}
	document, err := g.fetchNews(r, input.Symbol, credential)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood news unavailable"})
		return
	}
	response := struct {
		News                 newsDocument    `json:"news"`
		RefreshedCredentials json.RawMessage `json:"refreshed_credentials,omitempty"`
	}{News: document}
	if refreshed {
		response.RefreshedCredentials, _ = json.Marshal(credential)
	}
	writeJSON(w, http.StatusOK, response)
}

func (g *gateway) refresh(r *http.Request, current credentials) (credentials, error) {
	form := url.Values{
		"client_id": {robinhoodClientID}, "grant_type": {"refresh_token"},
		"refresh_token": {current.RefreshToken}, "scope": {"internal"},
		"device_token": {current.DeviceToken},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.base+"/oauth2/token/", strings.NewReader(form.Encode()))
	if err != nil {
		return credentials{}, err
	}
	applyRobinhoodHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.http.Do(req)
	if err != nil {
		return credentials{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return credentials{}, fmt.Errorf("refresh rejected")
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBytes)).Decode(&token); err != nil || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return credentials{}, fmt.Errorf("refresh response invalid")
	}
	current.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		current.RefreshToken = token.RefreshToken
	}
	if token.TokenType != "" {
		current.TokenType = token.TokenType
	}
	current.ExpiresAt = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	return current, nil
}

func (g *gateway) fetchNews(r *http.Request, symbol string, credential credentials) (newsDocument, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.base+"/midlands/news/"+url.PathEscape(symbol)+"/", nil)
	if err != nil {
		return newsDocument{}, err
	}
	applyRobinhoodHeaders(req)
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	resp, err := g.http.Do(req)
	if err != nil {
		return newsDocument{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return newsDocument{}, fmt.Errorf("news rejected")
	}
	var raw struct {
		Results []struct {
			Title       string `json:"title"`
			Summary     string `json:"summary"`
			URL         string `json:"url"`
			Source      string `json:"source"`
			PublishedAt string `json:"published_at"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpstreamBytes)).Decode(&raw); err != nil {
		return newsDocument{}, err
	}
	items := make([]newsItem, 0, min(len(raw.Results), 20))
	for _, item := range raw.Results {
		if len(items) == 20 {
			break
		}
		item.Title = bounded(strings.TrimSpace(item.Title), 1000)
		item.Summary = bounded(strings.TrimSpace(item.Summary), 4000)
		item.Source = bounded(strings.TrimSpace(item.Source), 200)
		item.URL = bounded(strings.TrimSpace(item.URL), 4000)
		if item.Title == "" || item.Source == "" || !safeExternalURL(item.URL) {
			continue
		}
		if published, err := time.Parse(time.RFC3339, item.PublishedAt); err == nil {
			item.PublishedAt = published.UTC().Format(time.RFC3339)
		} else {
			item.PublishedAt = ""
		}
		items = append(items, newsItem{Title: item.Title, Summary: item.Summary, URL: item.URL, Source: item.Source, PublishedAt: item.PublishedAt})
	}
	return newsDocument{Available: true, Source: "robinhood-private-api", Symbol: symbol, RetrievedAt: time.Now().UTC(), Items: items}, nil
}

func validCredentials(value credentials) bool {
	return value.AccessToken != "" && value.RefreshToken != "" && value.DeviceToken != "" &&
		len(value.AccessToken) <= 2048 && len(value.RefreshToken) <= 2048 && len(value.DeviceToken) <= 256 && !value.ExpiresAt.IsZero()
}

func safeSymbol(symbol string) bool {
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

func safeExternalURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func bounded(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func applyRobinhoodHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", robinhoodUserAgent)
	req.Header.Set("X-Robinhood-API-Version", "1.431.4")
}

func bearerToken(r *http.Request) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return ""
	}
	return token
}

func tokenMatches(candidate, expected string) bool {
	if candidate == "" || expected == "" {
		return false
	}
	a := sha256.Sum256([]byte(candidate))
	b := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
