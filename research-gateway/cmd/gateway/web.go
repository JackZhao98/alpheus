package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxWebQueryBytes  = 500
	maxFetchRawBytes  = 1 << 20
	maxFetchTextBytes = 20_000
	maxWebRedirects   = 3
)

type lookupIPFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type webSearchItem struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	PageAge     string   `json:"page_age,omitempty"`
	Snippets    []string `json:"snippets,omitempty"`
}

type webSearchDocument struct {
	Available   bool            `json:"available"`
	Source      string          `json:"source"`
	Query       string          `json:"query"`
	RetrievedAt time.Time       `json:"retrieved_at"`
	Items       []webSearchItem `json:"items"`
}

type webPageDocument struct {
	Available   bool      `json:"available"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	ContentType string    `json:"content_type"`
	Text        string    `json:"text"`
	Truncated   bool      `json:"truncated"`
	RetrievedAt time.Time `json:"retrieved_at"`
}

func newGateway(token string) *gateway {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}
	return &gateway{
		token: token, http: &http.Client{Timeout: 15 * time.Second}, base: robinhoodAPIBase,
		braveBase: braveAPIBase, lookupIP: net.DefaultResolver.LookupIPAddr, dialContext: dialer.DialContext,
	}
}

func (g *gateway) webSearch(w http.ResponseWriter, r *http.Request) {
	if !tokenMatches(bearerToken(r), g.token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Query  string `json:"query"`
		Count  int    `json:"count"`
		APIKey string `json:"api_key"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	input.APIKey = strings.TrimSpace(input.APIKey)
	if input.Query == "" || len(input.Query) > maxWebQueryBytes || input.Count < 1 || input.Count > 10 || input.APIKey == "" || len(input.APIKey) > 512 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid web search request"})
		return
	}
	base := strings.TrimRight(g.braveBase, "/")
	params := url.Values{
		"q": {input.Query}, "count": {strconv.Itoa(input.Count)}, "extra_snippets": {"true"},
		"text_decorations": {"false"}, "search_lang": {"en"},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/res/v1/web/search?"+params.Encode(), nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search unavailable"})
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", input.APIKey)
	client := g.http
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search unavailable"})
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search unavailable"})
		return
	}
	var raw struct {
		Web struct {
			Results []struct {
				Title         string   `json:"title"`
				URL           string   `json:"url"`
				Description   string   `json:"description"`
				PageAge       string   `json:"page_age"`
				ExtraSnippets []string `json:"extra_snippets"`
			} `json:"results"`
		} `json:"web"`
	}
	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamBytes+1))
	if err != nil || len(responseBytes) > maxUpstreamBytes {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search response invalid"})
		return
	}
	if err := json.Unmarshal(responseBytes, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web search response invalid"})
		return
	}
	items := make([]webSearchItem, 0, min(input.Count, len(raw.Web.Results)))
	for _, candidate := range raw.Web.Results {
		if len(items) == input.Count {
			break
		}
		candidate.Title = bounded(strings.TrimSpace(candidate.Title), 1000)
		candidate.URL = bounded(strings.TrimSpace(candidate.URL), 4000)
		if candidate.Title == "" || !safeExternalURL(candidate.URL) {
			continue
		}
		snippets := make([]string, 0, min(3, len(candidate.ExtraSnippets)))
		for _, snippet := range candidate.ExtraSnippets {
			snippet = bounded(strings.TrimSpace(snippet), 1500)
			if snippet != "" && len(snippets) < 3 {
				snippets = append(snippets, snippet)
			}
		}
		items = append(items, webSearchItem{
			Title: candidate.Title, URL: candidate.URL,
			Description: bounded(strings.TrimSpace(candidate.Description), 2000),
			PageAge:     bounded(strings.TrimSpace(candidate.PageAge), 100), Snippets: snippets,
		})
	}
	writeJSON(w, http.StatusOK, webSearchDocument{Available: true, Source: "brave-web", Query: input.Query, RetrievedAt: time.Now().UTC(), Items: items})
}

func (g *gateway) webFetch(w http.ResponseWriter, r *http.Request) {
	if !tokenMatches(bearerToken(r), g.token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	input.URL = strings.TrimSpace(input.URL)
	if input.MaxChars == 0 {
		input.MaxChars = 12_000
	}
	if len(input.URL) == 0 || len(input.URL) > 4000 || input.MaxChars < 1 || input.MaxChars > maxFetchTextBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid web fetch request"})
		return
	}
	document, err := g.fetchPublicPage(r.Context(), input.URL, input.MaxChars)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web page unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func decodeGatewayJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

func (g *gateway) fetchPublicPage(ctx context.Context, rawURL string, maxChars int) (webPageDocument, error) {
	current, err := url.Parse(rawURL)
	if err != nil {
		return webPageDocument{}, err
	}
	current.Fragment = ""
	for redirects := 0; redirects <= maxWebRedirects; redirects++ {
		ip, port, err := g.resolvePublicTarget(ctx, current)
		if err != nil {
			return webPageDocument{}, err
		}
		transport := &http.Transport{
			Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 8 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, ServerName: current.Hostname()},
			DialContext: func(dialCtx context.Context, network, _ string) (net.Conn, error) {
				return g.dialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
			},
		}
		client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return webPageDocument{}, err
		}
		req.Header.Set("User-Agent", "Alpheus-Research/1.0")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.8")
		response, err := client.Do(req)
		if err != nil {
			transport.CloseIdleConnections()
			return webPageDocument{}, err
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			if location == "" || redirects == maxWebRedirects {
				return webPageDocument{}, fmt.Errorf("invalid redirect")
			}
			next, err := current.Parse(location)
			if err != nil {
				return webPageDocument{}, err
			}
			next.Fragment = ""
			current = next
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			return webPageDocument{}, fmt.Errorf("HTTP %d", response.StatusCode)
		}
		mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if err != nil || !allowedWebMediaType(mediaType) {
			_ = response.Body.Close()
			transport.CloseIdleConnections()
			return webPageDocument{}, fmt.Errorf("unsupported content type")
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, maxFetchRawBytes+1))
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || len(raw) > maxFetchRawBytes || !utf8.Valid(raw) {
			return webPageDocument{}, fmt.Errorf("invalid web page body")
		}
		text, title := extractWebText(string(raw), mediaType)
		text, truncated := truncateUTF8(text, maxChars)
		return webPageDocument{
			Available: true, Source: "web-page-untrusted", URL: current.String(), Title: title,
			ContentType: mediaType, Text: text, Truncated: truncated, RetrievedAt: time.Now().UTC(),
		}, nil
	}
	return webPageDocument{}, fmt.Errorf("too many redirects")
}

func (g *gateway) resolvePublicTarget(ctx context.Context, target *url.URL) (net.IP, string, error) {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Hostname() == "" {
		return nil, "", fmt.Errorf("invalid URL")
	}
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if (target.Scheme == "https" && port != "443") || (target.Scheme == "http" && port != "80") {
		return nil, "", fmt.Errorf("nonstandard port")
	}
	lookup := g.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	addresses, err := lookup(ctx, target.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, "", fmt.Errorf("host resolution failed")
	}
	for _, address := range addresses {
		if !publicWebIP(address.IP) {
			return nil, "", fmt.Errorf("private or special address denied")
		}
	}
	return addresses[0].IP, port, nil
}

func publicWebIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		return !(v4[0] == 0 || (v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) || (v4[0] == 192 && v4[1] == 0 && v4[2] == 0) || (v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)))
	}
	return true
}

func allowedWebMediaType(value string) bool {
	switch strings.ToLower(value) {
	case "text/html", "application/xhtml+xml", "text/plain", "application/json":
		return true
	default:
		return false
	}
}

var (
	titlePattern  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	mainPattern   = regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`)
	bodyPattern   = regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body>`)
	commentBlock  = regexp.MustCompile(`(?s)<!--.*?-->`)
	scriptBlock   = regexp.MustCompile(`(?is)<(?:script|style|noscript|svg)\b[^>]*>.*?</(?:script|style|noscript|svg)>`)
	blockTag      = regexp.MustCompile(`(?is)<(?:p|div|h[1-6]|li|tr|br|blockquote|pre|section|article|main)\b[^>]*>`)
	remainingTags = regexp.MustCompile(`(?s)<[^>]+>`)
)

func extractWebText(raw, mediaType string) (string, string) {
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return strings.TrimSpace(raw), ""
	}
	title := ""
	if match := titlePattern.FindStringSubmatch(raw); len(match) == 2 {
		title = bounded(strings.TrimSpace(html.UnescapeString(match[1])), 1000)
	}
	content := raw
	if match := mainPattern.FindStringSubmatch(raw); len(match) == 2 {
		content = match[1]
	} else if match := bodyPattern.FindStringSubmatch(raw); len(match) == 2 {
		content = match[1]
	}
	content = commentBlock.ReplaceAllString(content, " ")
	content = scriptBlock.ReplaceAllString(content, " ")
	content = blockTag.ReplaceAllString(content, "\n")
	content = remainingTags.ReplaceAllString(content, " ")
	content = html.UnescapeString(content)
	lines := strings.Split(content, "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			clean = append(clean, line)
		}
	}
	return strings.Join(clean, "\n"), title
}

func truncateUTF8(value string, max int) (string, bool) {
	if len(value) <= max {
		return value, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut]), true
}
