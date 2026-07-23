package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMoodyBluesGEXBOTDeclarationIsTemporalAndDefensive(t *testing.T) {
	registry := newMoodyBluesRegistry()
	if err := registry.register(gexbotMoodyBluesProvider()); err != nil {
		t.Fatalf("register GEXBOT: %v", err)
	}
	if !registry.supports("gexbot_classic", "as_of") || !registry.supports("gexbot_classic", "replay") || !registry.supports("gexbot_classic", "live") {
		t.Fatalf("unexpected GEXBOT capabilities")
	}
	catalog := registry.catalog()
	if len(catalog) != 1 || catalog[0].Temporal.QueryPrecision != "microsecond" || catalog[0].Temporal.ObservationResolution != "30s" ||
		catalog[0].Temporal.AsOfSemantics != "latest_available_at_lte_as_of" {
		t.Fatalf("catalog=%+v", catalog)
	}
	catalog[0].Collection.Coverage[0] = "MUTATED"
	again := registry.catalog()
	if again[0].Collection.Coverage[0] != "SPX" {
		t.Fatalf("registry returned mutable provider declaration: %+v", again[0])
	}
}

func TestGEXBOTLiveIsCompactedBeforeLeavingResearchGateway(t *testing.T) {
	registry := newMoodyBluesRegistry()
	if err := registry.register(gexbotMoodyBluesProvider()); err != nil {
		t.Fatal(err)
	}
	providerResponse := `{"raw":{"blob_id":"f380732f-0799-4c56-b321-8f3fb997179c","size_bytes":6850,"content_digest":"af7ece94a862e98a8360817b9d7145435a4185359af17a596500c73090fc01c1"},"symbol":"SPX","metrics":{"spot":7504.8400,"zero_gamma":7501.61,"unreviewed_curve":[1,2,3]},"category":"gex_full","provider":"gexbot_classic","fetched_at":"2026-07-23T09:44:38Z","ingested_at":"2026-07-23T09:44:39Z","observed_at":"2026-07-23T09:44:37Z","source_kind":"provider_poll","available_at":"2026-07-23T09:44:39Z","quality_state":"accepted","record_digest":"4f611e426cb8ec50a3d8f02f1753f54a03567c7b5ace53dae1b7ae8a1eee2f75","observation_id":"be5a050f-8b78-4945-8ee5-b2acd40f2f9d","schema_revision":1,"source_timestamp":"2026-07-22T20:00:00Z","provider_revision":"gexbot_classic_v1"}`
	g := &gateway{
		cortexToken: "cortex-secret", gexbotToken: "provider-secret", gexbotURL: "https://provider.test", moodyBlues: registry,
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/live" || request.Header.Get("Authorization") != "Bearer provider-secret" {
				t.Fatalf("live request=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
			}
			return response(http.StatusOK, providerResponse), nil
		})},
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/moody-blues/providers/gexbot-classic/live", strings.NewReader(`{"symbol":"spx","category":"gex_full"}`))
	req.Header.Set("Authorization", "Bearer cortex-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"available":true`) ||
		!strings.Contains(w.Body.String(), `"spot":7504.84`) || strings.Contains(w.Body.String(), "unreviewed_curve") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMoodyBluesTimestampCanonicalizesToPostgresPrecision(t *testing.T) {
	now := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	value, ok := canonicalMoodyBluesTime("2026-07-23T00:59:59.123456789-07:00", now)
	if !ok || value.Format(time.RFC3339Nano) != "2026-07-23T07:59:59.123456Z" {
		t.Fatalf("value=%s ok=%t", value.Format(time.RFC3339Nano), ok)
	}
	if _, ok := canonicalMoodyBluesTime("2026-07-23T08:00:00.000001Z", now); ok {
		t.Fatal("future as_of fence was accepted")
	}
	if _, ok := canonicalMoodyBluesTime("not-a-time", now); ok {
		t.Fatal("invalid as_of fence was accepted")
	}
}

func TestMoodyBluesCatalogRequiresCortexToken(t *testing.T) {
	registry := newMoodyBluesRegistry()
	if err := registry.register(gexbotMoodyBluesProvider()); err != nil {
		t.Fatal(err)
	}
	g := &gateway{cortexToken: "cortex-secret", moodyBlues: registry}
	unauthorized := httptest.NewRecorder()
	g.moodyBluesProviders(unauthorized, httptest.NewRequest(http.MethodGet, "/internal/v1/moody-blues/providers", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/moody-blues/providers", nil)
	req.Header.Set("Authorization", "Bearer cortex-secret")
	w := httptest.NewRecorder()
	g.moodyBluesProviders(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "gexbotToken") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		System    string               `json:"system"`
		Providers []moodyBluesProvider `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.System != moodyBluesSystemID || len(result.Providers) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestGEXBOTAsOfNormalizesTheMoodyBluesFenceBeforeForwarding(t *testing.T) {
	registry := newMoodyBluesRegistry()
	if err := registry.register(gexbotMoodyBluesProvider()); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(123456789 * time.Nanosecond)
	expected := past.Truncate(time.Microsecond).Format(time.RFC3339Nano)
	g := &gateway{
		cortexToken: "cortex-secret", gexbotToken: "provider-secret", gexbotURL: "https://provider.test", moodyBlues: registry,
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost || request.URL.Path != "/v1/as-of" || request.Header.Get("Authorization") != "Bearer provider-secret" {
				t.Fatalf("forwarded request=%s %s auth=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
			}
			body, _ := io.ReadAll(request.Body)
			var input map[string]string
			if json.Unmarshal(body, &input) != nil || input["symbol"] != "SPX" || input["category"] != "gex_full" || input["as_of"] != expected {
				t.Fatalf("forwarded body=%s expected=%s", body, expected)
			}
			return response(http.StatusOK, `{"available":false,"symbol":"SPX","category":"gex_full","as_of":"`+expected+`"}`), nil
		})},
	}
	body := `{"symbol":"spx","category":"gex_full","as_of":"` + past.Format(time.RFC3339Nano) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/moody-blues/providers/gexbot-classic/as-of", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer cortex-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMoodyBluesGEXBOTStatusForwardsOnlyBoundedCollectionCoverage(t *testing.T) {
	registry := newMoodyBluesRegistry()
	if err := registry.register(gexbotMoodyBluesProvider()); err != nil {
		t.Fatal(err)
	}
	g := &gateway{
		cortexToken: "cortex-secret", gexbotToken: "provider-secret", gexbotURL: "https://provider.test", moodyBlues: registry,
		http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.Path != "/health" || request.Header.Get("Authorization") != "Bearer provider-secret" {
				t.Fatalf("status request=%s %s auth=%q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
			}
			return response(http.StatusOK, `{"ok":true,"provider":"gexbot_classic","collector_configured":true,"collection":{"schema_revision":1,"series":[]}}`), nil
		})},
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/v1/moody-blues/providers/gexbot-classic/status", nil)
	req.Header.Set("Authorization", "Bearer cortex-secret")
	w := httptest.NewRecorder()
	g.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"collection"`) || strings.Contains(w.Body.String(), `"payload"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
