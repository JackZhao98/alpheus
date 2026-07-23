package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Moody Blues is Research Gateway's time-data control surface. It does not
// fetch market data, decide what evidence means, or grant a Cortex Tool. It
// makes a Provider's collection and temporal-retrieval guarantees explicit so
// callers cannot mistake a current read for a point-in-time reconstruction.
const moodyBluesSystemID = "moody_blues"

type moodyBluesCapabilities struct {
	Live   bool `json:"live"`
	AsOf   bool `json:"as_of"`
	Replay bool `json:"replay"`
}

type moodyBluesTemporalContract struct {
	// QueryPrecision is deliberately distinct from ObservationResolution: a
	// 10:05:17 cutoff may be exact even when the latest sample is 10:05:00.
	QueryPrecision        string `json:"query_precision"`
	ObservationResolution string `json:"observation_resolution"`
	AsOfSemantics         string `json:"as_of_semantics"`
	ReplayOrder           string `json:"replay_order"`
}

type moodyBluesCollectionPolicy struct {
	Owner      string   `json:"owner"`
	Revision   string   `json:"revision"`
	Cadence    string   `json:"cadence"`
	Timezone   string   `json:"timezone"`
	Coverage   []string `json:"coverage"`
	Categories []string `json:"categories"`
}

// moodyBluesProvider is a safe, operator-facing declaration. It contains no
// credential, URL, raw bytes, or provider-specific request primitive.
type moodyBluesProvider struct {
	SchemaRevision uint16                     `json:"schema_revision"`
	ID             string                     `json:"id"`
	DisplayName    string                     `json:"display_name"`
	DataClass      string                     `json:"data_class"`
	Capabilities   moodyBluesCapabilities     `json:"capabilities"`
	Temporal       moodyBluesTemporalContract `json:"temporal"`
	Collection     moodyBluesCollectionPolicy `json:"collection"`
}

type moodyBluesRegistry struct {
	mu        sync.RWMutex
	providers map[string]moodyBluesProvider
}

func newMoodyBluesRegistry() *moodyBluesRegistry {
	return &moodyBluesRegistry{providers: make(map[string]moodyBluesProvider)}
}

func (r *moodyBluesRegistry) register(provider moodyBluesProvider) error {
	provider = copyMoodyBluesProvider(provider)
	if err := validateMoodyBluesProvider(provider); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, found := r.providers[provider.ID]; found && !sameMoodyBluesProvider(existing, provider) {
		return fmt.Errorf("Moody Blues Provider %q has a conflicting declaration", provider.ID)
	}
	r.providers[provider.ID] = provider
	return nil
}

func (r *moodyBluesRegistry) provider(id string) (moodyBluesProvider, bool) {
	if r == nil {
		return moodyBluesProvider{}, false
	}
	r.mu.RLock()
	provider, found := r.providers[id]
	r.mu.RUnlock()
	return copyMoodyBluesProvider(provider), found
}

func (r *moodyBluesRegistry) catalog() []moodyBluesProvider {
	if r == nil {
		return []moodyBluesProvider{}
	}
	r.mu.RLock()
	providers := make([]moodyBluesProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		providers = append(providers, copyMoodyBluesProvider(provider))
	}
	r.mu.RUnlock()
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers
}

func (r *moodyBluesRegistry) supports(id string, capability string) bool {
	provider, found := r.provider(id)
	if !found {
		return false
	}
	switch capability {
	case "live":
		return provider.Capabilities.Live
	case "as_of":
		return provider.Capabilities.AsOf
	case "replay":
		return provider.Capabilities.Replay
	default:
		return false
	}
}

func (g *gateway) moodyBluesProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !g.validCortexToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_revision": 1,
		"system":          moodyBluesSystemID,
		"providers":       g.moodyBlues.catalog(),
	})
}

func (g *gateway) validCortexToken(r *http.Request) bool {
	return g != nil && g.cortexToken != "" && tokenMatches(bearerToken(r), g.cortexToken)
}

// moodyBluesGEXBOTStatus exposes only collection coverage from the Provider's
// own health surface. It is intentionally not a generic Provider proxy and
// rejects a response that could contain raw source bytes.
func (g *gateway) moodyBluesGEXBOTStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !g.validCortexToolToken(r) || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "as_of") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, g.gexbotURL+"/health", nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider unavailable"})
		return
	}
	request.Header.Set("Authorization", "Bearer "+g.gexbotToken)
	client := g.http
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider unavailable"})
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<10+1))
	if err != nil || response.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > 16<<10 || !json.Valid(raw) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider status unavailable"})
		return
	}
	var status struct {
		OK         bool            `json:"ok"`
		Provider   string          `json:"provider"`
		Collection json.RawMessage `json:"collection"`
		Payload    json.RawMessage `json:"payload"`
		Raw        json.RawMessage `json:"raw"`
	}
	if json.Unmarshal(raw, &status) != nil || !status.OK || status.Provider != "gexbot_classic" || !json.Valid(status.Collection) || status.Payload != nil || status.Raw != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider status invalid"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// canonicalMoodyBluesTime accepts RFC3339/RFC3339Nano, normalizes it to the
// PostgreSQL storage precision, and rejects a future observation fence.
func canonicalMoodyBluesTime(value string, now time.Time) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	parsed = parsed.UTC().Truncate(time.Microsecond)
	if parsed.IsZero() || parsed.After(now.UTC()) {
		return time.Time{}, false
	}
	return parsed, true
}

func gexbotMoodyBluesProvider() moodyBluesProvider {
	return moodyBluesProvider{
		SchemaRevision: 1,
		ID:             "gexbot_classic",
		DisplayName:    "GEXBOT Classic Archive",
		DataClass:      "options_gamma_snapshot",
		Capabilities:   moodyBluesCapabilities{AsOf: true, Replay: true},
		Temporal: moodyBluesTemporalContract{
			QueryPrecision:        "microsecond",
			ObservationResolution: "30s",
			AsOfSemantics:         "latest_available_at_lte_as_of",
			ReplayOrder:           "available_at_ascending_then_observation_id",
		},
		Collection: moodyBluesCollectionPolicy{
			Owner:      "gexbot_provider",
			Revision:   "gexbot_spx_30s_et_v1",
			Cadence:    "30s during America/New_York weekdays 09:00-16:00",
			Timezone:   "America/New_York",
			Coverage:   []string{"SPX"},
			Categories: []string{"gex_full", "gex_zero", "gex_one"},
		},
	}
}

func validateMoodyBluesProvider(provider moodyBluesProvider) error {
	if provider.SchemaRevision != 1 || !gatewayIdentifier(provider.ID) || strings.TrimSpace(provider.DisplayName) == "" ||
		!gatewayIdentifier(provider.DataClass) || strings.TrimSpace(provider.Temporal.QueryPrecision) == "" ||
		strings.TrimSpace(provider.Temporal.ObservationResolution) == "" || strings.TrimSpace(provider.Temporal.AsOfSemantics) == "" ||
		strings.TrimSpace(provider.Temporal.ReplayOrder) == "" || !gatewayIdentifier(provider.Collection.Owner) ||
		!gatewayIdentifier(provider.Collection.Revision) || strings.TrimSpace(provider.Collection.Cadence) == "" ||
		strings.TrimSpace(provider.Collection.Timezone) == "" || len(provider.Collection.Coverage) == 0 || len(provider.Collection.Categories) == 0 ||
		(!provider.Capabilities.Live && !provider.Capabilities.AsOf && !provider.Capabilities.Replay) ||
		(provider.Capabilities.Replay && !provider.Capabilities.AsOf) {
		return fmt.Errorf("invalid Moody Blues Provider %q", provider.ID)
	}
	return nil
}

func copyMoodyBluesProvider(provider moodyBluesProvider) moodyBluesProvider {
	provider.Collection.Coverage = append([]string(nil), provider.Collection.Coverage...)
	provider.Collection.Categories = append([]string(nil), provider.Collection.Categories...)
	return provider
}

func sameMoodyBluesProvider(left, right moodyBluesProvider) bool {
	if left.SchemaRevision != right.SchemaRevision || left.ID != right.ID || left.DisplayName != right.DisplayName || left.DataClass != right.DataClass ||
		left.Capabilities != right.Capabilities || left.Temporal != right.Temporal || left.Collection.Owner != right.Collection.Owner ||
		left.Collection.Revision != right.Collection.Revision || left.Collection.Cadence != right.Collection.Cadence || left.Collection.Timezone != right.Collection.Timezone ||
		len(left.Collection.Coverage) != len(right.Collection.Coverage) || len(left.Collection.Categories) != len(right.Collection.Categories) {
		return false
	}
	for index := range left.Collection.Coverage {
		if left.Collection.Coverage[index] != right.Collection.Coverage[index] {
			return false
		}
	}
	for index := range left.Collection.Categories {
		if left.Collection.Categories[index] != right.Collection.Categories[index] {
			return false
		}
	}
	return true
}
