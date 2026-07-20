package assemble

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"alpheus/agentruntime/internal/roles"
)

func TestAssembleAuthenticatesKernelReads(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})

	client := New("http://kernel.test", "runtime-secret")
	client.HTTP.Transport = transport
	role := roles.Role{InjectedContext: []string{"state", "limits"}}
	context, err := client.Assemble(role)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || context["state"] == nil || context["limits"] == nil {
		t.Fatalf("requests=%d context=%v", requests, context)
	}
}

func TestAssembleQueryAddsMarketContext(t *testing.T) {
	var mu sync.Mutex
	tools := map[string]map[string]any{}
	toolCounts := map[string]int{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatal("missing runtime authorization")
		}
		body := `{}`
		switch r.URL.Path {
		case "/state":
			body = `{"mode":"read_only"}`
		case "/market/quote/SOFI":
			body = `{"symbol":"SOFI","bid":"22.10","ask":"22.12"}`
		case "/market/bars/SOFI":
			if r.URL.Query().Get("days") != "30" {
				t.Fatalf("bars query=%s", r.URL.RawQuery)
			}
			body = `{"bars":[]}`
		case "/research/news/SOFI":
			body = `{"available":true,"source":"robinhood-private-api","symbol":"SOFI","items":[]}`
		case "/research/search":
			if !strings.Contains(r.URL.Query().Get("q"), "SOFI stock") {
				t.Fatalf("search query=%s", r.URL.RawQuery)
			}
			body = `{"available":true,"source":"brave-web","query":"SOFI stock 现在值得研究吗？","items":[]}`
		case "/mcp/read-query":
			var input struct {
				Tool string         `json:"tool"`
				Args map[string]any `json:"args"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode MCP query: %v", err)
			}
			mu.Lock()
			tools[input.Tool] = input.Args
			toolCounts[input.Tool]++
			mu.Unlock()
			if input.Tool == "get_earnings_results" {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}
			body = `{"tool":"` + input.Tool + `","source":"robinhood-mcp","result":{}}`
		default:
			t.Fatalf("unexpected path %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	client := New("http://kernel.test", "runtime-secret")
	client.HTTP.Transport = transport
	context, err := client.AssembleQuery(roles.Role{InjectedContext: []string{
		"state", "equity_fundamentals", "company_financials", "earnings_results",
		"technical_rsi", "technical_macd", "technical_atr", "news_headlines", "web_search", "web_page",
	}}, "sofi", "现在值得研究吗？")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"state", "market_quote", "market_bars", "symbol", "user_query",
		"equity_fundamentals", "company_financials", "earnings_results",
		"technical_rsi", "technical_macd", "technical_atr", "news_headlines", "web_search", "web_page",
	} {
		if context[key] == nil {
			t.Fatalf("missing context key %q: %v", key, context)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tools) != 4 || toolCounts["get_equity_technical_indicators"] != 3 ||
		tools["get_equity_fundamentals"] == nil || tools["get_financials"] == nil || tools["get_earnings_results"] == nil {
		t.Fatalf("MCP tools=%v counts=%v", tools, toolCounts)
	}
	if !strings.Contains(string(context["earnings_results"]), `"available":false`) {
		t.Fatalf("earnings fallback=%s", context["earnings_results"])
	}
	if !strings.Contains(string(context["web_page"]), "no_url_requested") {
		t.Fatalf("web_page=%s", context["web_page"])
	}
}

func TestFirstQueryURL(t *testing.T) {
	if got := firstQueryURL("请阅读 https://example.com/article?q=1，然后分析"); got != "https://example.com/article?q=1" {
		t.Fatalf("got=%q", got)
	}
	for _, query := range []string{"没有链接", "file:///etc/passwd", "https://user@example.com/private"} {
		if got := firstQueryURL(query); got != "" {
			t.Fatalf("query=%q got=%q", query, got)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
