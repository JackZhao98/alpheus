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

type gexbotCollectionStore interface {
	LoadGEXBotCollectionConfig() (store.GEXBotCollectionConfig, error)
	SaveGEXBotCollectionConfig(store.GEXBotCollectionConfig) (store.GEXBotCollectionConfig, error)
	RecordGEXBotObservation(store.GEXBotObservationInput) error
}

func (s *server) gexbotStore() (gexbotCollectionStore, bool) {
	value, ok := s.store.(gexbotCollectionStore)
	return value, ok
}

func (s *server) getGEXBotCollectionConfig(w http.ResponseWriter, _ *http.Request) {
	data, ok := s.gexbotStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GEXBot collection unavailable"})
		return
	}
	config, err := data.LoadGEXBotCollectionConfig()
	if err != nil {
		writeStoreError(w, "load GEXBot collection config", err)
		return
	}
	_, secretErr := s.loadAgentSecret(gexbotCollectionSecret)
	writeJSON(w, http.StatusOK, map[string]any{"config": config, "credential_configured": secretErr == nil})
}

func (s *server) putGEXBotCollectionConfig(w http.ResponseWriter, r *http.Request) {
	data, ok := s.gexbotStore()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GEXBot collection unavailable"})
		return
	}
	var input store.GEXBotCollectionConfig
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if input.Enabled {
		if _, err := s.loadAgentSecret(gexbotCollectionSecret); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "GEXBot API key is not configured"})
			return
		}
	}
	config, err := data.SaveGEXBotCollectionConfig(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBot collection configuration"})
		return
	}
	s.store.Event("gexbot_collection_configured", map[string]any{"enabled": config.Enabled, "symbols": config.Symbols, "interval_minutes": config.IntervalMinutes})
	writeJSON(w, http.StatusOK, map[string]any{"config": config})
}

func startGEXBotCollector(s *server) error {
	if _, ok := s.gexbotStore(); !ok {
		return nil
	}
	go func() {
		var mu sync.Mutex
		last := map[string]time.Time{}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for now := range ticker.C {
			if !gexbotCollectionWindow(now) {
				continue
			}
			data, ok := s.gexbotStore()
			if !ok {
				continue
			}
			config, err := data.LoadGEXBotCollectionConfig()
			if err != nil || !config.Enabled {
				continue
			}
			bucket := now.UTC().Truncate(time.Minute)
			if now.Minute()%config.IntervalMinutes != 0 || now.Second() > 10 {
				continue
			}
			for _, symbol := range config.Symbols {
				mu.Lock()
				duplicate := last[symbol].Equal(bucket)
				mu.Unlock()
				if duplicate {
					continue
				}
				if err := s.collectGEXBotClassic(context.Background(), data, symbol, bucket); err != nil {
					log.Printf("gexbot collector %s: %v", symbol, err)
					continue
				}
				mu.Lock()
				last[symbol] = bucket
				mu.Unlock()
			}
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
	return minute >= 9*60 && minute < 20*60
}

func (s *server) collectGEXBotClassic(ctx context.Context, data gexbotCollectionStore, symbol string, observedAt time.Time) error {
	key, err := s.loadAgentSecret(gexbotCollectionSecret)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.gex.bot/v2/"+symbol+"/classic/gex_full", nil)
	if err != nil {
		return err
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
		return fmt.Errorf("request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil || response.StatusCode != http.StatusOK || !json.Valid(raw) {
		return fmt.Errorf("invalid response")
	}
	var payload struct {
		Timestamp int64    `json:"timestamp"`
		Ticker    string   `json:"ticker"`
		Spot      *float64 `json:"spot"`
		ZeroGamma *float64 `json:"zero_gamma"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil || payload.Timestamp <= 0 || strings.ToUpper(payload.Ticker) != symbol {
		return fmt.Errorf("response schema mismatch")
	}
	return data.RecordGEXBotObservation(store.GEXBotObservationInput{ID: store.NewID(), Symbol: symbol, ObservedAt: observedAt, SourceTimestamp: time.Unix(payload.Timestamp, 0).UTC(), Spot: payload.Spot, ZeroGamma: payload.ZeroGamma, Payload: raw})
}
