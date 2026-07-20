package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchNormalizesWithoutReturningCredential(t *testing.T) {
	g := newGateway("kernel-secret")
	g.braveBase = "https://search.test"
	g.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/res/v1/web/search" || request.URL.Query().Get("q") != "SOFI latest" || request.Header.Get("X-Subscription-Token") != "brave-secret" {
			t.Fatalf("request=%s?%s key=%q", request.URL.Path, request.URL.RawQuery, request.Header.Get("X-Subscription-Token"))
		}
		return response(http.StatusOK, `{"web":{"results":[{"title":"Source","url":"https://example.com/a","description":"Claim","page_age":"2 hours ago","extra_snippets":["One","Two"]},{"title":"Bad","url":"file:///etc/passwd"}]}}`), nil
	})}
	body, _ := json.Marshal(map[string]any{"query": "SOFI latest", "count": 5, "api_key": "brave-secret"})
	request := httptest.NewRequest(http.MethodPost, "/v1/web/search", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer kernel-secret")
	w := httptest.NewRecorder()
	g.webSearch(w, request)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "brave-secret") || !strings.Contains(w.Body.String(), `"source":"brave-web"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var document webSearchDocument
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil || len(document.Items) != 1 || document.Items[0].Description != "Claim" {
		t.Fatalf("document=%+v err=%v", document, err)
	}
}

func TestFetchPublicPagePinsValidatedAddressAndExtractsText(t *testing.T) {
	g := newGateway("kernel-secret")
	g.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	g.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return pipeHTTPResponse(http.StatusOK, "Content-Type: text/html; charset=utf-8\r\n", `<html><head><title>Example</title><script>ignore()</script></head><body><main><h1>SOFI</h1><p>Observed &amp; dated.</p></main></body></html>`), nil
	}
	document, err := g.fetchPublicPage(context.Background(), "http://example.com/article#fragment", 1000)
	if err != nil || document.URL != "http://example.com/article" || document.Title != "Example" || document.Text != "SOFI\nObserved & dated." || document.Truncated {
		t.Fatalf("document=%+v err=%v", document, err)
	}
}

func TestFetchRejectsRedirectToPrivateAddressBeforeSecondDial(t *testing.T) {
	g := newGateway("kernel-secret")
	g.lookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "127.0.0.1" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	dials := 0
	g.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		dials++
		return pipeHTTPResponse(http.StatusFound, "Location: http://127.0.0.1/private\r\n", ""), nil
	}
	_, err := g.fetchPublicPage(context.Background(), "http://example.com/start", 1000)
	if err == nil || dials != 1 {
		t.Fatalf("err=%v dials=%d", err, dials)
	}
}

func pipeHTTPResponse(status int, headers, body string) net.Conn {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		for {
			line, err := reader.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_, _ = fmt.Fprintf(server, "HTTP/1.1 %d %s\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, http.StatusText(status), headers, len(body), body)
	}()
	return client
}

func TestPublicWebIPRejectsLocalAndSpecialRanges(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "::1", "fc00::1", "fe80::1"} {
		if publicWebIP(net.ParseIP(raw)) {
			t.Fatalf("special address allowed: %s", raw)
		}
	}
	if !publicWebIP(net.ParseIP("93.184.216.34")) || !publicWebIP(net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")) {
		t.Fatal("public address rejected")
	}
}

func TestWebFetchHandlerRejectsUnauthorizedBeforeResolution(t *testing.T) {
	g := newGateway("kernel-secret")
	g.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("unauthorized request reached resolver")
		return nil, nil
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/web/fetch", strings.NewReader(`{"url":"https://example.com","max_chars":100}`))
	w := httptest.NewRecorder()
	g.webFetch(w, request)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestTruncateUTF8KeepsRuneBoundary(t *testing.T) {
	value, truncated := truncateUTF8("ab你cd", 4)
	if value != "ab" || !truncated {
		t.Fatalf("value=%q truncated=%v", value, truncated)
	}
}
