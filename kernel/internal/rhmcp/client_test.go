package rhmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestClientCachesCallsAndReconnects(t *testing.T) {
	if os.Getenv("ALPHEUS_MCP_INTEGRATION") != "1" {
		t.Skip("set ALPHEUS_MCP_INTEGRATION=1 to run loopback transport tests")
	}
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "get_accounts"}, func(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		calls.Add(1)
		return nil, map[string]any{"data": map[string]any{"accounts": []any{}}}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "slow_read"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
			return nil, map[string]any{"data": map[string]any{}}, nil
		}
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()

	client, err := New(Config{
		Endpoint: httpServer.URL, HTTPClient: httpServer.Client(),
		CacheTTL: time.Minute, CallTimeout: time.Second, ConnectWait: time.Second,
		AllowedTools: []string{"get_accounts", "slow_read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for range 2 {
		raw, err := client.Call(context.Background(), "get_accounts", map[string]any{})
		if err != nil || !json.Valid(raw) {
			t.Fatalf("call raw=%s err=%v", raw, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want one cached transport call", calls.Load())
	}
	// Leave the closed session pointer installed to model a transport that
	// disappears between calls. Call must fail once at the sole CallTool
	// boundary, reconnect, and then succeed.
	client.mu.Lock()
	if err := client.session.Close(); err != nil {
		client.mu.Unlock()
		t.Fatal(err)
	}
	client.mu.Unlock()
	client.cacheMu.Lock()
	client.cache = map[string]cacheEntry{}
	client.cacheMu.Unlock()
	if _, err := client.Call(context.Background(), "get_accounts", map[string]any{}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want reconnect call", calls.Load())
	}
	client.cfg.CallTimeout = 25 * time.Millisecond
	started := time.Now()
	if _, err := client.Call(context.Background(), "slow_read", map[string]any{}); err == nil {
		t.Fatal("provider call ignored configured timeout")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("provider timeout returned too late: %s", elapsed)
	}
}

func TestRateLimiterHonorsContextDeadline(t *testing.T) {
	limiter := newTokenBucket(1, 1)
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := limiter.Wait(ctx); err == nil {
		t.Fatal("rate limiter ignored context deadline")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("rate limiter returned too late: %s", elapsed)
	}
}

func TestProtectedOAuthStateIsReusableAcrossTransportRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.json")
	token := tokenFile{
		Version: 1, AccessToken: "restart-secret", TokenType: "Bearer",
		ExpiresAt: time.Now().UTC().Add(time.Hour), ClientID: "client",
	}
	if err := saveTokenFile(path, token); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer restart-secret" {
			t.Fatalf("authorization header=%q", got)
		}
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    request,
		}, nil
	})
	for range 2 {
		transport, err := NewFileTokenTransport(path, base)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "https://provider.invalid/read", nil)
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d, want two independent transports", requests.Load())
	}
}

func TestDecodeDataRejectsEnvelopeDrift(t *testing.T) {
	var dst struct {
		Value string `json:"value"`
	}
	if err := DecodeData(json.RawMessage(`{"data":{"value":"ok"},"guide":"fixture"}`), &dst); err != nil || dst.Value != "ok" {
		t.Fatalf("decode dst=%+v err=%v", dst, err)
	}
	for _, raw := range []string{
		`{"result":{"value":"moved"}}`,
		`{"data":{"value":"ok"},"guide":"fixture","raw":"unexpected"}`,
		`{"data":null,"guide":"fixture"}`,
		`{"data":{"value":"ok"}}`,
	} {
		if err := DecodeData(json.RawMessage(raw), &dst); err == nil {
			t.Fatalf("accepted drift: %s", raw)
		}
	}
}

func TestDecodeExactWholeAcceptsZeroFractionOnly(t *testing.T) {
	for _, raw := range []string{`100`, `"100.0000"`, `-2.0`} {
		if _, err := DecodeExactWhole([]byte(raw)); err != nil {
			t.Fatalf("rejected exact whole %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`"100.01"`, `"1e2"`, `" 100"`, `null`, `""`, `"+100"`} {
		if _, err := DecodeExactWhole([]byte(raw)); err == nil {
			t.Fatalf("accepted inexact whole %s", raw)
		}
	}
}

func TestTokenFileMustBePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.json")
	raw := []byte(`{"version":1,"access_token":"secret","client_id":"client"}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileTokenTransport(path, nil); err == nil {
		t.Fatal("accepted group/world-readable OAuth state")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileTokenTransport(path, nil); err != nil {
		t.Fatalf("private state rejected: %v", err)
	}
	if err := os.WriteFile(path, append(raw, []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileTokenTransport(path, nil); err == nil {
		t.Fatal("accepted trailing OAuth JSON value")
	}
}

func TestMissingTokenErrorDoesNotLeakSecretPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensitive-account-name.json")
	_, err := NewFileTokenTransport(path, nil)
	if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "sensitive-account-name") {
		t.Fatalf("unsafe missing-token error: %v", err)
	}
}
