package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/units"
)

func normalizedSymbol(raw string) (string, error) {
	symbol := strings.ToUpper(strings.TrimSpace(raw))
	if symbol == "" || len(symbol) > 128 {
		return "", fmt.Errorf("invalid symbol")
	}
	for _, r := range symbol {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(".-_", r) {
			continue
		}
		return "", fmt.Errorf("invalid symbol")
	}
	return symbol, nil
}

func parseBoundedInt(raw string, defaultValue, maxValue int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	if value > maxValue {
		value = maxValue
	}
	return value, nil
}

func parseWindow(raw string) (units.PercentMicros, error) {
	if raw == "" {
		return units.MustPercent("15"), nil
	}
	var window units.PercentMicros
	if err := json.Unmarshal([]byte(raw), &window); err != nil || window < 0 {
		return 0, fmt.Errorf("window_pct must be a non-negative decimal")
	}
	max := units.MustPercent("15")
	if window > max {
		window = max
	}
	return window, nil
}

func (s *server) availableMarketProvider(w http.ResponseWriter) marketdata.Provider {
	provider := s.marketProvider()
	if provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "market data unavailable"})
	}
	return provider
}

func (s *server) getMarketQuote(w http.ResponseWriter, r *http.Request) {
	symbol, err := normalizedSymbol(r.PathValue("symbol"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	quote, err := provider.Quote(r.Context(), symbol)
	if err != nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

// getAuthorityMarketQuote is a diagnostic-only, read-only view of the same
// forced-fresh market provider used by live admission. It deliberately returns
// a decoded quote even when its venue timestamp is too old for a trade, so an
// operator can distinguish a fetch failure from freshness rejection without
// sending an order.
func (s *server) getAuthorityMarketQuote(w http.ResponseWriter, r *http.Request) {
	symbol, err := normalizedSymbol(r.PathValue("symbol"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider := s.authorityMarketProvider()
	if provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authority market data unavailable"})
		return
	}
	quote, err := provider.Quote(r.Context(), symbol)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "authority market data unavailable"})
		return
	}
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_view": "authority",
		"quote":         quote,
		"age_ms":        now.Sub(quote.AsOf).Milliseconds(),
		"usable":        quote.Usable(s.limits.QuoteMaxAgeSec, now),
		"max_age_sec":   s.limits.QuoteMaxAgeSec,
	})
}

func (s *server) getMarketChain(w http.ResponseWriter, r *http.Request) {
	underlying, err := normalizedSymbol(r.PathValue("underlying"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
	if parsed, err := time.Parse(time.DateOnly, expiry); err != nil || parsed.Format(time.DateOnly) != expiry {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expiry must be YYYY-MM-DD"})
		return
	}
	window, err := parseWindow(r.URL.Query().Get("window_pct"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	chain, err := provider.Chain(r.Context(), underlying, expiry, window)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": chain, "window_pct": window})
}

func (s *server) getMarketExpirations(w http.ResponseWriter, r *http.Request) {
	underlying, err := normalizedSymbol(r.PathValue("underlying"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	expirations, err := provider.Expirations(r.Context(), underlying)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": expirations})
}

func (s *server) getMarketBars(w http.ResponseWriter, r *http.Request) {
	symbol, err := normalizedSymbol(r.PathValue("symbol"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	days, err := parseBoundedInt(r.URL.Query().Get("days"), 5, marketdata.MaxBarDays)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "days " + err.Error()})
		return
	}
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	bars, err := provider.Bars(r.Context(), symbol, days)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": bars, "days": days})
}

func (s *server) getMarketMovers(w http.ResponseWriter, r *http.Request) {
	direction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("dir")))
	if direction != "up" && direction != "down" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dir must be up or down"})
		return
	}
	n, err := parseBoundedInt(r.URL.Query().Get("n"), 10, marketdata.MaxMovers)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "n " + err.Error()})
		return
	}
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	movers, err := provider.Movers(r.Context(), direction, n)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": movers, "n": n})
}

func (s *server) getMarketHours(w http.ResponseWriter, r *http.Request) {
	provider := s.availableMarketProvider(w)
	if provider == nil {
		return
	}
	hours, err := provider.Hours(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "market data unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, hours)
}

func (s *server) getProviderStatus(w http.ResponseWriter, _ *http.Request) {
	provider := s.marketProvider()
	statusProvider, ok := provider.(marketdata.StatusProvider)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"connected": false, "source": "unavailable", "last_error": "provider status unavailable",
			"schema_drift": true, "mode": s.tradingMode(), "account": maskedAccountID(s.mode.LiveAccountID),
		})
		return
	}
	status := statusProvider.ProviderStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"connected": status.Connected, "source": status.Source,
		"snapshot_version": status.SnapshotVersion, "last_successful_read": status.LastSuccessfulRead,
		"last_error": status.LastError, "schema_drift": status.SchemaDrift,
		"mode": s.tradingMode(), "account": maskedAccountID(s.mode.LiveAccountID),
	})
}
