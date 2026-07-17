package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/units"
)

func TestMarketRoutesClampWorkAndFailClosed(t *testing.T) {
	b := newFake("300")
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}

	w := routeRequest(s.routes(), http.MethodGet, "/market/bars/SPY?days=9999", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"days":30`) {
		t.Fatalf("bars status=%d body=%s", w.Code, w.Body.String())
	}
	expiry := time.Now().UTC().AddDate(0, 0, 7).Format(time.DateOnly)
	w = routeRequest(s.routes(), http.MethodGet, "/market/chain/SPY?expiry="+expiry+"&window_pct=999", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"window_pct":15`) {
		t.Fatalf("chain status=%d body=%s", w.Code, w.Body.String())
	}

	s.limits.QuoteMaxAgeSec = 15
	for name, quote := range map[string]broker.Quote{
		"crossed": {Symbol: "BAD", Bid: units.MustMicros("100"), Ask: units.MustMicros("50"), Source: "fake", AsOf: time.Now().UTC()},
		"locked":  {Symbol: "BAD", Bid: units.MustMicros("100"), Ask: units.MustMicros("100"), Source: "fake", AsOf: time.Now().UTC()},
		"zero":    {Symbol: "BAD", Ask: units.MustMicros("1"), Source: "fake", AsOf: time.Now().UTC()},
		"stale":   {Symbol: "BAD", Bid: units.MustMicros("1"), Ask: units.MustMicros("2"), Source: "fake", AsOf: time.Now().UTC().Add(-time.Minute)},
		"future":  {Symbol: "BAD", Bid: units.MustMicros("1"), Ask: units.MustMicros("2"), Source: "fake", AsOf: time.Now().UTC().Add(time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := b.SetQuote(quote); err != nil {
				t.Fatal(err)
			}
			w := routeRequest(s.routes(), http.MethodGet, "/market/quote/BAD", "", "")
			if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "market data unavailable") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	if err := b.SetQuote(broker.Quote{
		Symbol: "BAD", Bid: units.MustMicros("100"), Ask: units.MustMicros("50"), Source: "fake", AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	w, body := postOperation(t, s, `{"action":"open","kind":"equity","underlying":"BAD","symbol":"BAD","side":"buy","qty":1,"max_risk_usd":10,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || !strings.Contains(fmt.Sprint(body["reasons"]), "market_data_unavailable") {
		t.Fatalf("proposal status=%d body=%v", w.Code, body)
	}
}

func TestMissingProviderStatusFailsDiagnosticsClosed(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, store: newMemoryStore()}
	w := routeRequest(s.routes(), http.MethodGet, "/provider/status", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"connected":false`) ||
		!strings.Contains(w.Body.String(), `"schema_drift":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMissingMarketProviderReturnsServiceUnavailable(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, store: newMemoryStore()}
	for _, target := range []string{
		"/market/quote/SPY",
		"/market/chain/SPY?expiry=2026-07-24&window_pct=15",
		"/market/expirations/SPY",
		"/market/bars/SPY?days=5",
		"/market/movers?dir=up&n=5",
		"/market/hours",
	} {
		w := routeRequest(s.routes(), http.MethodGet, target, "", "")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status=%d body=%s", target, w.Code, w.Body.String())
		}
	}
}
