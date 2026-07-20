package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestNewsConnectorNormalizesAndBoundsRobinhoodResponse(t *testing.T) {
	credential := credentials{AccessToken: "read-token", RefreshToken: "refresh-token", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour), DeviceToken: "device"}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/midlands/news/SOFI/" || r.Header.Get("Authorization") != "Bearer read-token" {
			t.Fatalf("upstream request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		return response(http.StatusOK, `{"results":[{"title":"Headline","summary":"Claim","url":"https://example.com/a","source":"Wire","published_at":"2026-07-20T12:00:00Z"},{"title":"bad url","url":"file:///etc/passwd","source":"bad"}]}`), nil
	})}
	g := &gateway{token: "kernel-secret", http: client, base: "https://api.test"}
	body, _ := json.Marshal(map[string]any{"symbol": "sofi", "credentials": credential})
	req := httptest.NewRequest(http.MethodPost, "/v1/robinhood/news", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer kernel-secret")
	w := httptest.NewRecorder()
	g.news(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "read-token") || strings.Contains(w.Body.String(), "refresh-token") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		News newsDocument `json:"news"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.News.Symbol != "SOFI" || len(result.News.Items) != 1 || result.News.Items[0].Title != "Headline" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestExpiredCredentialRefreshesBeforeNews(t *testing.T) {
	credential := credentials{AccessToken: "old-access", RefreshToken: "old-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour), DeviceToken: "device"}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/oauth2/token/":
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))
			if r.Method != http.MethodPost || values.Get("refresh_token") != "old-refresh" || values.Get("device_token") != "device" {
				t.Fatalf("refresh request=%s values=%v", r.Method, values)
			}
			return response(http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":86400}`), nil
		case "/midlands/news/SOFI/":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Fatalf("news auth=%q", r.Header.Get("Authorization"))
			}
			return response(http.StatusOK, `{"results":[]}`), nil
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
			return nil, nil
		}
	})}
	g := &gateway{token: "kernel-secret", http: client, base: "https://api.test"}
	body, _ := json.Marshal(map[string]any{"symbol": "SOFI", "credentials": credential})
	req := httptest.NewRequest(http.MethodPost, "/v1/robinhood/news", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer kernel-secret")
	w := httptest.NewRecorder()
	g.news(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"access_token":"new-access"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestNewsConnectorRejectsUnauthorizedCallerBeforeUpstream(t *testing.T) {
	g := &gateway{token: "kernel-secret", http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unauthorized request reached upstream")
		return nil, nil
	})}}
	req := httptest.NewRequest(http.MethodPost, "/v1/robinhood/news", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	g.news(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
