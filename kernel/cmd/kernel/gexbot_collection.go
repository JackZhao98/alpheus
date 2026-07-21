package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"alpheus/kernel/internal/store"
)

const gexbotCollectionSecret = "gexbot"
const gexbotCollectionSymbol = "SPX"
const gexbotCollectionCadence = 30 * time.Second

var gexbotClassicCategories = []string{"gex_full", "gex_zero", "gex_one"}

type gexbotCollectionStore interface {
	RecordGEXBotObservation(store.GEXBotObservationInput) error
}

type gexbotClassicSnapshot struct {
	Symbol          string
	Category        string
	SourceTimestamp time.Time
	Spot            *float64
	ZeroGamma       *float64
	MajorPosVol     *float64
	MajorPosOI      *float64
	MajorNegVol     *float64
	MajorNegOI      *float64
	Payload         json.RawMessage
}

func (s *server) gexbotStore() (gexbotCollectionStore, bool) {
	value, ok := s.store.(gexbotCollectionStore)
	return value, ok
}

// postGEXBotTest performs one explicit read-only provider request. It neither
// enables the scheduler nor writes an observation, so testing a Key cannot
// change the collection history or the trading path.
func (s *server) postGEXBotTest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Symbol string `json:"symbol"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	symbol, err := normalizeGEXBotSymbol(input.Symbol)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBot symbol"})
		return
	}
	snapshot, err := s.fetchGEXBotClassic(r.Context(), symbol, "gex_full")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBot test request failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"symbol":           snapshot.Symbol,
		"source_timestamp": snapshot.SourceTimestamp,
		"spot":             snapshot.Spot,
		"zero_gamma":       snapshot.ZeroGamma,
	})
}

func startGEXBotCollector(s *server) error {
	if _, ok := s.gexbotStore(); !ok {
		return nil
	}
	go func() {
		last := map[string]time.Time{}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for now := range ticker.C {
			if !gexbotCollectionWindow(now) {
				continue
			}
			if now.Second()%int(gexbotCollectionCadence/time.Second) > 2 {
				continue
			}
			data, ok := s.gexbotStore()
			if !ok {
				continue
			}
			bucket := now.UTC().Truncate(gexbotCollectionCadence)
			var waits sync.WaitGroup
			for _, category := range gexbotClassicCategories {
				duplicate := last[category].Equal(bucket)
				if duplicate {
					continue
				}
				last[category] = bucket
				waits.Add(1)
				go func(category string) {
					defer waits.Done()
					if err := s.collectGEXBotClassic(context.Background(), data, gexbotCollectionSymbol, category, bucket); err != nil {
						log.Printf("gexbot collector %s/%s: %v", gexbotCollectionSymbol, category, err)
					}
				}(category)
			}
			waits.Wait()
		}
	}()
	return nil
}

func gexbotCollectionWindow(now time.Time) bool {
	market, err := time.LoadLocation("America/New_York")
	if err != nil {
		return false
	}
	local := now.In(market)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	return minute >= 9*60 && minute < 16*60
}

func (s *server) collectGEXBotClassic(ctx context.Context, data gexbotCollectionStore, symbol, category string, observedAt time.Time) error {
	snapshot, err := s.fetchGEXBotClassic(ctx, symbol, category)
	if err != nil {
		return err
	}
	return data.RecordGEXBotObservation(store.GEXBotObservationInput{
		ID:              store.NewID(),
		Symbol:          snapshot.Symbol,
		Category:        snapshot.Category,
		ObservedAt:      observedAt,
		SourceTimestamp: snapshot.SourceTimestamp,
		Spot:            snapshot.Spot,
		ZeroGamma:       snapshot.ZeroGamma,
		MajorPosVol:     snapshot.MajorPosVol,
		MajorPosOI:      snapshot.MajorPosOI,
		MajorNegVol:     snapshot.MajorNegVol,
		MajorNegOI:      snapshot.MajorNegOI,
		Payload:         snapshot.Payload,
	})
}

func (s *server) fetchGEXBotClassic(ctx context.Context, symbol, category string) (gexbotClassicSnapshot, error) {
	symbol, err := normalizeGEXBotSymbol(symbol)
	if err != nil {
		return gexbotClassicSnapshot{}, err
	}
	if !validGEXBotClassicCategory(category) {
		return gexbotClassicSnapshot{}, fmt.Errorf("invalid GEXBot Classic category")
	}
	key, err := s.loadAgentSecret(gexbotCollectionSecret)
	if err != nil {
		return gexbotClassicSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.gex.bot/v2/"+symbol+"/classic/"+category, nil)
	if err != nil {
		return gexbotClassicSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "Alpheus-GEXCollector/1.0")
	req.Header.Set("Accept", "application/json")
	client := s.gexHTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return gexbotClassicSnapshot{}, fmt.Errorf("request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil || response.StatusCode != http.StatusOK || !json.Valid(raw) {
		return gexbotClassicSnapshot{}, fmt.Errorf("invalid response")
	}
	var payload struct {
		Timestamp   int64    `json:"timestamp"`
		Ticker      string   `json:"ticker"`
		Spot        *float64 `json:"spot"`
		ZeroGamma   *float64 `json:"zero_gamma"`
		MajorPosVol *float64 `json:"major_pos_vol"`
		MajorPosOI  *float64 `json:"major_pos_oi"`
		MajorNegVol *float64 `json:"major_neg_vol"`
		MajorNegOI  *float64 `json:"major_neg_oi"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil || payload.Timestamp <= 0 || strings.ToUpper(payload.Ticker) != symbol {
		return gexbotClassicSnapshot{}, fmt.Errorf("response schema mismatch")
	}
	return gexbotClassicSnapshot{Symbol: symbol, Category: category, SourceTimestamp: time.Unix(payload.Timestamp, 0).UTC(), Spot: payload.Spot,
		ZeroGamma: payload.ZeroGamma, MajorPosVol: payload.MajorPosVol, MajorPosOI: payload.MajorPosOI,
		MajorNegVol: payload.MajorNegVol, MajorNegOI: payload.MajorNegOI, Payload: raw}, nil
}

func validGEXBotClassicCategory(category string) bool {
	return category == "gex_full" || category == "gex_zero" || category == "gex_one"
}

func normalizeGEXBotSymbol(raw string) (string, error) {
	symbol := strings.ToUpper(strings.TrimSpace(raw))
	if len(symbol) == 0 || len(symbol) > 16 {
		return "", fmt.Errorf("invalid GEXBot symbol")
	}
	for _, char := range symbol {
		if !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') {
			return "", fmt.Errorf("invalid GEXBot symbol")
		}
	}
	return symbol, nil
}
