package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

type gexbotRoundTripper func(*http.Request) (*http.Response, error)

func (f gexbotRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFetchGEXBotClassicReadsOnceWithoutRecording(t *testing.T) {
	st := newMemoryStore()
	key := "gexbot_test_0123456789abcdef"
	ciphertext, err := sealAgentSecret(strings.Repeat("k", 32), gexbotCollectionSecret, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret(gexbotCollectionSecret, ciphertext); err != nil {
		t.Fatal(err)
	}
	called := 0
	s := &server{
		mode: config.ModeConfig{AgentWebSessionKey: strings.Repeat("k", 32)}, store: st,
		gexHTTP: &http.Client{Transport: gexbotRoundTripper(func(request *http.Request) (*http.Response, error) {
			called++
			if request.URL.Path != "/v2/SPX/classic/gex_full" || request.Header.Get("Authorization") != "Bearer "+key {
				t.Fatalf("unexpected request path=%s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"timestamp":1784592000,"ticker":"SPX","spot":6300.25,"zero_gamma":6250}`)), Header: make(http.Header)}, nil
		})},
	}
	snapshot, err := s.fetchGEXBotClassic(context.Background(), "spx")
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || snapshot.Symbol != "SPX" || snapshot.Spot == nil || *snapshot.Spot != 6300.25 || snapshot.ZeroGamma == nil || *snapshot.ZeroGamma != 6250 {
		t.Fatalf("called=%d snapshot=%+v", called, snapshot)
	}
}
