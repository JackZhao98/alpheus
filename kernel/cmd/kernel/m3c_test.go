package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type pnlAccountProvider struct {
	broker.AccountProvider
	pnl    units.Micros
	onRead func()
}

func (p *pnlAccountProvider) RealizedPnL(context.Context, time.Time, string) (broker.RealizedPnLSnapshot, error) {
	if p.onRead != nil {
		p.onRead()
	}
	return broker.RealizedPnLSnapshot{Total: p.pnl, Source: "test", AsOf: time.Now().UTC()}, nil
}

func TestStateRejectsProviderReadThatCrossesMarketDay(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	st := newMemoryStore()
	databaseTime := time.Date(2026, 1, 16, 4, 59, 59, 0, time.UTC)
	st.databaseNow = func() time.Time { return databaseTime }
	account := &pnlAccountProvider{AccountProvider: venue}
	account.onRead = func() {
		databaseTime = time.Date(2026, 1, 16, 5, 0, 0, 0, time.UTC)
	}
	s := &server{limits: dualLedgerLimits(), account: account, broker: venue, store: st}
	w := routeRequest(s.routes(), http.MethodGet, "/state", "", "kernel-secret")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "market day advanced") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if st.breakerStates["live"].Halted {
		t.Fatalf("cross-day provider read changed breaker: %+v", st.breakerStates["live"])
	}
}

func TestDailyLossBreakerRejectsOpenButNotCloseOrShadow(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "LOSS", "9.90", "10", 1_000)
	setQuote(venue, "SHADOW", "9.90", "10", 1_000)
	if result, err := placeOrder(venue, "LOSS", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed close position: result=%+v err=%v", result, err)
	}
	st := newMemoryStore()
	st.m3aActive = true
	st.realizedPnL["live"] = units.MustMicros("-120")
	st.exposureQty[memoryExposureKey("live", "LOSS", "equity")] = units.MustQty("1")
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		account: venue, broker: venue, store: st,
	}
	handler := s.routes()
	plan := `"plan":{"stop":"9","invalidation":"x","time_stop":"15:45","target":"12"}`
	open := `{"action":"open","kind":"equity","underlying":"LOSS","symbol":"LOSS","side":"buy","qty":1,` + plan + `}`
	w := routeRequestWithKey(handler, http.MethodPost, "/operations", open, "runtime-secret", "daily-loss-open")
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), `"class":"REJECT"`, `breaker halted: daily_loss`) {
		t.Fatalf("halted open status=%d body=%s", w.Code, w.Body.String())
	}

	w = routeRequestWithKey(handler, http.MethodPost, "/operations",
		`{"action":"close","symbol":"LOSS","qty":1}`, "runtime-secret", "daily-loss-close")
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), `"class":"A"`, `"status":"executed"`) {
		t.Fatalf("halted close status=%d body=%s", w.Code, w.Body.String())
	}

	shadow := `{"action":"open","kind":"equity","underlying":"SHADOW","symbol":"SHADOW","side":"buy","qty":1,"shadow":true,` + plan + `}`
	w = routeRequestWithKey(handler, http.MethodPost, "/operations", shadow, "runtime-secret", "daily-loss-shadow")
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), `"class":"B"`, `"status":"executed"`) {
		t.Fatalf("shadow status=%d body=%s", w.Code, w.Body.String())
	}
	if st.breakerStates["shadow"].Halted {
		t.Fatalf("live loss halted shadow: %+v", st.breakerStates["shadow"])
	}
}

func TestBreakerResumeSuppressesSameDayAndExpiresNextDay(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "RESUME", "9.90", "10", 1_000)
	st := newMemoryStore()
	st.realizedPnL["live"] = units.MustMicros("-120")
	dayOne := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	st.databaseNow = func() time.Time { return dayOne }
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		account: venue, broker: venue, store: st,
	}
	handler := s.routes()
	plan := `"plan":{"stop":"9","invalidation":"x","time_stop":"15:45","target":"12"}`
	open := `{"action":"open","kind":"equity","underlying":"RESUME","symbol":"RESUME","side":"buy","qty":1,` + plan + `}`
	if w := routeRequest(handler, http.MethodPost, "/breaker/resume",
		`{"ledger":"live","reason":"daily_loss"}`, "admin-secret"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "breaker_not_active") {
		t.Fatalf("preemptive resume status=%d body=%s", w.Code, w.Body.String())
	}
	if w := routeRequestWithKey(handler, http.MethodPost, "/operations", open, "runtime-secret", "resume-trigger"); w.Code != http.StatusOK || !containsAll(w.Body.String(), `breaker halted: daily_loss`) {
		t.Fatalf("trigger status=%d body=%s", w.Code, w.Body.String())
	}
	if w := routeRequest(handler, http.MethodPost, "/breaker/resume",
		`{"ledger":"live","reason":"daily_loss"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized resume status=%d body=%s", w.Code, w.Body.String())
	}
	w := routeRequest(handler, http.MethodPost, "/breaker/resume",
		`{"ledger":"live","reason":"daily_loss"}`, "admin-secret")
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), `"halted":false`, `"override_reason":"daily_loss"`) {
		t.Fatalf("resume status=%d body=%s", w.Code, w.Body.String())
	}
	w = routeRequest(handler, http.MethodGet, "/state", "", "kernel-secret")
	if w.Code != http.StatusOK || containsAll(w.Body.String(), `"live"`, `"halted":true`) {
		t.Fatalf("same-day state status=%d body=%s", w.Code, w.Body.String())
	}

	st.databaseNow = func() time.Time { return dayOne.Add(24 * time.Hour) }
	w = routeRequestWithKey(handler, http.MethodPost, "/operations", open, "runtime-secret", "resume-next-day")
	if w.Code != http.StatusOK || !containsAll(w.Body.String(), `breaker halted: daily_loss`) {
		t.Fatalf("next-day override leaked status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBreakerResumeRejectsStalePriorDayDailyLoss(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	st := newMemoryStore()
	dayOne := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	dayTwo := dayOne.AddDate(0, 0, 1)
	st.databaseNow = func() time.Time { return dayTwo }
	st.breakerStates["live"] = store.BreakerState{
		Ledger: "live", Halted: true, Reason: "daily_loss", UpdatedAt: dayOne,
	}
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		account: venue, broker: venue, store: st,
	}
	w := routeRequest(s.routes(), http.MethodPost, "/breaker/resume",
		`{"ledger":"live","reason":"daily_loss"}`, "admin-secret")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "breaker_not_active") {
		t.Fatalf("stale resume status=%d body=%s", w.Code, w.Body.String())
	}
	if len(st.breakerOverrides) != 0 {
		t.Fatalf("stale resume created override: %v", st.breakerOverrides)
	}
	if state := st.breakerStates["live"]; !state.Halted || state.Reason != "daily_loss" || !state.UpdatedAt.Equal(dayOne) {
		t.Fatalf("stale resume changed breaker: %+v", state)
	}
	w = routeRequest(s.routes(), http.MethodGet, "/state", "", "kernel-secret")
	if w.Code != http.StatusOK || st.breakerStates["live"].Halted {
		t.Fatalf("next-day evaluation did not clear stale daily halt: status=%d body=%s state=%+v",
			w.Code, w.Body.String(), st.breakerStates["live"])
	}
}

func TestProviderPnLUsesMoreLossMakingValueAndDivergenceDoesNotBlockClose(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "DIVERGE", "9.90", "10", 1_000)
	if result, err := placeOrder(venue, "DIVERGE", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed position: result=%+v err=%v", result, err)
	}
	account := &pnlAccountProvider{AccountProvider: venue, pnl: units.MustMicros("-5")}
	st := newMemoryStore()
	st.m3aActive = true
	st.realizedPnL["live"] = units.MustMicros("-10")
	st.exposureQty[memoryExposureKey("live", "DIVERGE", "equity")] = units.MustQty("1")
	s := &server{limits: dualLedgerLimits(), account: account, broker: venue, store: st}
	window, err := s.databaseMarketWindow(st)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := venue.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var day risk.DayState
	if err := st.WithLedgerLock(false, func(gate store.OperationGate) error {
		var err error
		day, err = s.dayStateAtAccount(context.Background(), gate, false, acct, window, false, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if day.RealizedPnL != units.MustMicros("-10") || !day.Halted || day.HaltReason != "pnl_divergence" {
		t.Fatalf("provider lag day=%+v", day)
	}
	account.pnl = units.MustMicros("-20")
	if err := st.WithLedgerLock(false, func(gate store.OperationGate) error {
		var err error
		day, err = s.dayStateAtAccount(context.Background(), gate, false, acct, window, false, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if day.RealizedPnL != units.MustMicros("-20") || day.HaltReason != "pnl_divergence" {
		t.Fatalf("provider-only loss day=%+v", day)
	}
	response, body := postOperation(t, s, `{"action":"close","symbol":"DIVERGE","qty":1}`)
	if response.Code != http.StatusOK || body["class"] != "A" || body["status"] != "executed" {
		t.Fatalf("divergence close status=%d body=%v", response.Code, body)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
