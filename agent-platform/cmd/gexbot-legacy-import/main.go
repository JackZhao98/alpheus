// gexbot-legacy-import is a temporary, one-way migration bridge. It is the
// only process allowed to read the former Kernel-owned GEXBOT table, and it
// delivers those rows through the Provider's ordinary authenticated ingestion
// API. The Provider, not this bridge, creates the canonical Blob and Research
// observation. It is safe to re-run: observation IDs are preserved.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	providerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("GEXBOT_PROVIDER_URL")), "/")
	token, err := secret("GEXBOT_PROVIDER_INGEST_TOKEN_FILE")
	if err != nil || databaseURL == "" || providerURL == "" {
		return fmt.Errorf("legacy import configuration unavailable")
	}
	limit := 2000
	if raw := strings.TrimSpace(os.Getenv("GEXBOT_LEGACY_IMPORT_LIMIT")); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed < 1 || parsed > 50000 {
			return fmt.Errorf("invalid legacy import limit")
		}
		limit = parsed
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT legacy.id::TEXT,legacy.symbol,legacy.category,legacy.source_timestamp,legacy.observed_at,legacy.created_at,legacy.payload,
		legacy.spot,legacy.zero_gamma,legacy.major_pos_vol,legacy.major_pos_oi,legacy.major_neg_vol,legacy.major_neg_oi
		FROM public.gexbot_observation legacy
		LEFT JOIN research.gexbot_observation imported ON imported.observation_id=legacy.id
		WHERE imported.observation_id IS NULL
		ORDER BY legacy.observed_at,legacy.category,legacy.id LIMIT $1`, limit)
	if err != nil {
		return fmt.Errorf("load legacy GEXBOT observations: %w", err)
	}
	defer rows.Close()
	client := &http.Client{Timeout: 30 * time.Second}
	count := 0
	for rows.Next() {
		var input struct {
			SourceKind      string          `json:"source_kind"`
			ObservationID   string          `json:"observation_id"`
			Symbol          string          `json:"symbol"`
			Category        string          `json:"category"`
			SourceTimestamp time.Time       `json:"source_timestamp"`
			ObservedAt      time.Time       `json:"observed_at"`
			FetchedAt       time.Time       `json:"fetched_at"`
			AvailableAt     time.Time       `json:"available_at"`
			Payload         json.RawMessage `json:"payload"`
			Spot            *float64        `json:"spot,omitempty"`
			ZeroGamma       *float64        `json:"zero_gamma,omitempty"`
			MajorPosVol     *float64        `json:"major_pos_vol,omitempty"`
			MajorPosOI      *float64        `json:"major_pos_oi,omitempty"`
			MajorNegVol     *float64        `json:"major_neg_vol,omitempty"`
			MajorNegOI      *float64        `json:"major_neg_oi,omitempty"`
		}
		input.SourceKind = "legacy_kernel_import"
		if err := rows.Scan(&input.ObservationID, &input.Symbol, &input.Category, &input.SourceTimestamp, &input.ObservedAt, &input.FetchedAt, &input.Payload,
			&input.Spot, &input.ZeroGamma, &input.MajorPosVol, &input.MajorPosOI, &input.MajorNegVol, &input.MajorNegOI); err != nil {
			return err
		}
		input.AvailableAt = input.FetchedAt
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, providerURL+"/v1/observations", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("send legacy observation: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusCreated {
			return fmt.Errorf("legacy observation import rejected: status %d", response.StatusCode)
		}
		_ = body
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	log.Printf("GEXBOT legacy import delivered %d observations", count)
	return nil
}

func secret(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return "", fmt.Errorf("secret unavailable")
	}
	return strings.TrimSpace(string(data)), nil
}
