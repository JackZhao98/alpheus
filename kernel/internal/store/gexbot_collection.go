package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type GEXBotCollectionConfig struct {
	Enabled         bool      `json:"enabled"`
	Symbols         []string  `json:"symbols"`
	IntervalMinutes int       `json:"interval_minutes"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type GEXBotObservationInput struct {
	ID              string
	Symbol          string
	Category        string
	ObservedAt      time.Time
	SourceTimestamp time.Time
	Spot            *float64
	ZeroGamma       *float64
	MajorPosVol     *float64
	MajorPosOI      *float64
	MajorNegVol     *float64
	MajorNegOI      *float64
	Payload         json.RawMessage
}

// GEXBotObservation is deliberately a raw-data record, not a trading fact.
// Payload is loaded only for the selected latest profile; history endpoints use
// the compact fields below so a browser never receives an entire day's raw book.
type GEXBotObservation struct {
	Symbol          string
	Category        string
	ObservedAt      time.Time
	SourceTimestamp time.Time
	Spot            *float64
	ZeroGamma       *float64
	MajorPosVol     *float64
	MajorPosOI      *float64
	MajorNegVol     *float64
	MajorNegOI      *float64
	Payload         json.RawMessage
}

func (s *Store) LoadGEXBotCollectionConfig() (GEXBotCollectionConfig, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var value GEXBotCollectionConfig
	err := s.DB.QueryRowContext(ctx, `SELECT enabled,symbols,interval_minutes,updated_at
		FROM gexbot_collection_config WHERE singleton=true`).Scan(&value.Enabled, pq.Array(&value.Symbols), &value.IntervalMinutes, &value.UpdatedAt)
	if err != nil {
		return GEXBotCollectionConfig{}, normalizeDBError(err)
	}
	return normalizeGEXBotCollectionConfig(value)
}

func (s *Store) SaveGEXBotCollectionConfig(value GEXBotCollectionConfig) (GEXBotCollectionConfig, error) {
	value, err := normalizeGEXBotCollectionConfig(value)
	if err != nil {
		return GEXBotCollectionConfig{}, err
	}
	ctx, cancel := s.deadline()
	defer cancel()
	err = s.DB.QueryRowContext(ctx, `UPDATE gexbot_collection_config SET enabled=$1,symbols=$2,interval_minutes=$3,updated_at=clock_timestamp()
		WHERE singleton=true RETURNING updated_at`, value.Enabled, pq.Array(value.Symbols), value.IntervalMinutes).Scan(&value.UpdatedAt)
	if err != nil {
		return GEXBotCollectionConfig{}, normalizeDBError(err)
	}
	return value, nil
}

func normalizeGEXBotCollectionConfig(value GEXBotCollectionConfig) (GEXBotCollectionConfig, error) {
	if value.IntervalMinutes != 1 && value.IntervalMinutes != 5 && value.IntervalMinutes != 10 && value.IntervalMinutes != 15 {
		return GEXBotCollectionConfig{}, fmt.Errorf("invalid GEXBot collection interval")
	}
	seen := map[string]bool{}
	symbols := make([]string, 0, len(value.Symbols))
	for _, raw := range value.Symbols {
		symbol := strings.ToUpper(strings.TrimSpace(raw))
		if len(symbol) == 0 || len(symbol) > 16 || seen[symbol] {
			return GEXBotCollectionConfig{}, fmt.Errorf("invalid GEXBot collection symbol")
		}
		for _, char := range symbol {
			if !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') {
				return GEXBotCollectionConfig{}, fmt.Errorf("invalid GEXBot collection symbol")
			}
		}
		seen[symbol] = true
		symbols = append(symbols, symbol)
	}
	if len(symbols) == 0 || len(symbols) > 32 {
		return GEXBotCollectionConfig{}, fmt.Errorf("invalid GEXBot collection symbols")
	}
	value.Symbols = symbols
	return value, nil
}

func (s *Store) RecordGEXBotObservation(input GEXBotObservationInput) error {
	if input.ID == "" || input.Symbol == "" || !validGEXBotCategory(input.Category) || input.ObservedAt.IsZero() || input.SourceTimestamp.IsZero() || !json.Valid(input.Payload) {
		return fmt.Errorf("invalid GEXBot observation")
	}
	var payload any
	if err := json.Unmarshal(input.Payload, &payload); err != nil {
		return fmt.Errorf("invalid GEXBot payload")
	}
	digest := sha256.Sum256(input.Payload)
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO gexbot_observation(id,symbol,category,observed_at,source_timestamp,payload_digest,spot,zero_gamma,major_pos_vol,major_pos_oi,major_neg_vol,major_neg_oi,payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (symbol,category,observed_at) DO NOTHING`,
		input.ID, input.Symbol, input.Category, input.ObservedAt.UTC(), input.SourceTimestamp.UTC(), digest[:], input.Spot, input.ZeroGamma,
		input.MajorPosVol, input.MajorPosOI, input.MajorNegVol, input.MajorNegOI, input.Payload)
	return normalizeDBError(err)
}

func (s *Store) ListGEXBotObservationHistory(symbol, category string, start, end time.Time) ([]GEXBotObservation, error) {
	if symbol != "SPX" || !validGEXBotCategory(category) || !start.Before(end) {
		return nil, fmt.Errorf("invalid GEXBot history query")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT symbol,category,observed_at,source_timestamp,spot,zero_gamma,major_pos_vol,major_pos_oi,major_neg_vol,major_neg_oi
		FROM gexbot_observation WHERE symbol=$1 AND category=$2 AND observed_at >= $3 AND observed_at < $4 ORDER BY observed_at ASC`,
		symbol, category, start.UTC(), end.UTC())
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	result := make([]GEXBotObservation, 0)
	for rows.Next() {
		var value GEXBotObservation
		if err := rows.Scan(&value.Symbol, &value.Category, &value.ObservedAt, &value.SourceTimestamp, &value.Spot, &value.ZeroGamma,
			&value.MajorPosVol, &value.MajorPosOI, &value.MajorNegVol, &value.MajorNegOI); err != nil {
			return nil, normalizeDBError(err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return result, nil
}

func (s *Store) LoadLatestGEXBotObservation(symbol, category string, start, end time.Time) (*GEXBotObservation, error) {
	if symbol != "SPX" || !validGEXBotCategory(category) || !start.Before(end) {
		return nil, fmt.Errorf("invalid GEXBot latest query")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	value := &GEXBotObservation{}
	err := s.DB.QueryRowContext(ctx, `SELECT symbol,category,observed_at,source_timestamp,spot,zero_gamma,major_pos_vol,major_pos_oi,major_neg_vol,major_neg_oi,payload
		FROM gexbot_observation WHERE symbol=$1 AND category=$2 AND observed_at >= $3 AND observed_at < $4 ORDER BY observed_at DESC LIMIT 1`,
		symbol, category, start.UTC(), end.UTC()).Scan(&value.Symbol, &value.Category, &value.ObservedAt, &value.SourceTimestamp,
		&value.Spot, &value.ZeroGamma, &value.MajorPosVol, &value.MajorPosOI, &value.MajorNegVol, &value.MajorNegOI, &value.Payload)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, normalizeDBError(err)
	}
	return value, nil
}

func validGEXBotCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" || value == "gex_one"
}
