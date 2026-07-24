// GEXBOT Provider is a credential-isolated, append-only Research Plane
// collector. It owns raw snapshot retention, point-in-time lookup and a
// deterministic replay cursor. Research Gateway consumes this API; Cortex
// never connects to the provider database or receives raw provider payloads.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	_ "github.com/lib/pq"
)

const (
	providerRole       = "alpheus_gexbot_provider"
	maxPayloadBytes    = 2 << 20
	stageTTLSeconds    = 300
	providerAPITimeout = 20 * time.Second
)

var gexbotCategories = []string{"gex_full", "gex_zero", "gex_one"}

type provider struct {
	db                         *sql.DB
	store                      *blob.LocalStore
	principal, ingest, readKey string
	apiKey, apiBaseURL         string
	http                       *http.Client
}

type observationInput struct {
	ObservationID   string          `json:"observation_id,omitempty"`
	SourceKind      string          `json:"source_kind"`
	Symbol          string          `json:"symbol"`
	Category        string          `json:"category"`
	SourceTimestamp time.Time       `json:"source_timestamp"`
	ObservedAt      time.Time       `json:"observed_at"`
	FetchedAt       time.Time       `json:"fetched_at"`
	AvailableAt     time.Time       `json:"available_at,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Spot            *float64        `json:"spot,omitempty"`
	ZeroGamma       *float64        `json:"zero_gamma,omitempty"`
	MajorPosVol     *float64        `json:"major_pos_vol,omitempty"`
	MajorPosOI      *float64        `json:"major_pos_oi,omitempty"`
	MajorNegVol     *float64        `json:"major_neg_vol,omitempty"`
	MajorNegOI      *float64        `json:"major_neg_oi,omitempty"`
}

type gexbotPayload struct {
	Timestamp   int64    `json:"timestamp"`
	Ticker      string   `json:"ticker"`
	Spot        *float64 `json:"spot"`
	ZeroGamma   *float64 `json:"zero_gamma"`
	MajorPosVol *float64 `json:"major_pos_vol"`
	MajorPosOI  *float64 `json:"major_pos_oi"`
	MajorNegVol *float64 `json:"major_neg_vol"`
	MajorNegOI  *float64 `json:"major_neg_oi"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	databaseURL, err := readRequiredSecret("GEXBOT_PROVIDER_DATABASE_URL_FILE")
	if err != nil {
		return err
	}
	ingest, err := readRequiredSecret("GEXBOT_PROVIDER_INGEST_TOKEN_FILE")
	if err != nil {
		return err
	}
	readKey, err := readRequiredSecret("GEXBOT_PROVIDER_READ_TOKEN_FILE")
	if err != nil {
		return err
	}
	store, err := blob.NewLocalStore(env("GEXBOT_BLOB_ROOT", "/var/lib/alpheus/cortex-blobs"))
	if err != nil {
		return fmt.Errorf("open GEXBOT raw BlobStore: %w", err)
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("open GEXBOT database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping GEXBOT database: %w", err)
	}
	apiKey, _ := readOptionalSecret("GEXBOT_API_KEY_FILE")
	p := &provider{db: db, store: store, principal: env("GEXBOT_PROVIDER_PRINCIPAL_ID", "gexbot-provider-1"), ingest: ingest, readKey: readKey,
		apiKey: apiKey, apiBaseURL: strings.TrimRight(env("GEXBOT_API_BASE_URL", "https://api.gex.bot"), "/"), http: &http.Client{Timeout: providerAPITimeout}}
	if err := p.assertIdentity(ctx); err != nil {
		_ = db.Close()
		return err
	}
	if apiKey != "" {
		if _, err := time.LoadLocation("America/New_York"); err != nil {
			_ = db.Close()
			return fmt.Errorf("load GEXBOT collection timezone: %w", err)
		}
		p.startCollector(context.Background())
		log.Printf("GEXBOT Provider collector enabled for SPX 09:00-16:00 America/New_York")
	} else {
		log.Printf("GEXBOT Provider started in push-only mode; no provider API key is mounted")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", p.health)
	mux.HandleFunc("POST /v1/observations", p.ingestObservation)
	mux.HandleFunc("POST /v1/live", p.live)
	mux.HandleFunc("POST /v1/as-of", p.asOf)
	mux.HandleFunc("POST /v1/replays", p.createReplay)
	mux.HandleFunc("POST /v1/replays/{id}/next", p.nextReplay)
	server := &http.Server{Addr: env("GEXBOT_PROVIDER_ADDR", ":8500"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("GEXBOT Research Provider listening on %s as %s", server.Addr, p.principal)
	return server.ListenAndServe()
}

// live performs one credential-isolated official API read and then appends the
// same observation to the archive. The response is still a normalized Provider
// record with a raw Blob reference; raw source bytes never cross this boundary.
func (p *provider) live(w http.ResponseWriter, r *http.Request) {
	if !matchesBearer(r, p.readKey) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if p.apiKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GEXBOT live source is not configured"})
		return
	}
	var input struct {
		Symbol   string `json:"symbol"`
		Category string `json:"category"`
	}
	if !decodeJSON(w, r, 4<<10, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	if input.Symbol != "SPX" || !validCategory(input.Category) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEXBOT live query"})
		return
	}
	observation, err := p.fetchClassic(r.Context(), input.Symbol, input.Category, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		log.Printf("GEXBOT live fetch failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live source unavailable"})
		return
	}
	result, err := p.record(r.Context(), observation)
	if err != nil {
		log.Printf("GEXBOT live archive failed: %v", err)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "GEXBOT live observation rejected"})
		return
	}
	writeRawJSON(w, result)
}

func (p *provider) assertIdentity(ctx context.Context) error {
	return p.withRole(ctx, func(tx *sql.Tx) error {
		var principal string
		return tx.QueryRowContext(ctx, `SELECT principal_id FROM platform_security.gexbot_provider_identity()`).Scan(&principal)
	})
}

func (p *provider) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status, err := p.collectionStatus(ctx)
	if err != nil {
		log.Printf("GEXBOT collection status unavailable: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "collection status unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                   true,
		"provider":             "gexbot_classic",
		"collector_configured": p.apiKey != "",
		"collection":           status,
	})
}

func (p *provider) collectionStatus(ctx context.Context) (json.RawMessage, error) {
	var raw []byte
	err := p.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT research.gexbot_collection_status()::TEXT`).Scan(&raw)
	})
	if err != nil {
		return nil, fmt.Errorf("collection status unavailable: %w", err)
	}
	if len(raw) == 0 || len(raw) > 16<<10 || !json.Valid(raw) {
		return nil, errors.New("collection status invalid")
	}
	return json.RawMessage(raw), nil
}

func (p *provider) ingestObservation(w http.ResponseWriter, r *http.Request) {
	if !matchesBearer(r, p.ingest) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input observationInput
	if !decodeJSON(w, r, maxPayloadBytes+16<<10, &input) {
		return
	}
	if input.SourceKind == "" {
		input.SourceKind = "collector_push"
	}
	if input.SourceKind != "collector_push" && input.SourceKind != "legacy_kernel_import" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid source kind"})
		return
	}
	result, err := p.record(r.Context(), input)
	if err != nil {
		log.Printf("GEXBOT ingest failed: %v", err)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "observation rejected"})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (p *provider) asOf(w http.ResponseWriter, r *http.Request) {
	if !matchesBearer(r, p.readKey) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Symbol   string    `json:"symbol"`
		Category string    `json:"category"`
		AsOf     time.Time `json:"as_of"`
	}
	if !decodeJSON(w, r, 8<<10, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	var raw []byte
	err := p.withRole(r.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(r.Context(), `SELECT research.gexbot_as_of($1,$2,$3)::TEXT`, input.Symbol, input.Category, input.AsOf.UTC()).Scan(&raw)
	})
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid as_of query"})
		return
	}
	writeRawJSON(w, raw)
}

func (p *provider) createReplay(w http.ResponseWriter, r *http.Request) {
	if !matchesBearer(r, p.readKey) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		RequestID string    `json:"request_id"`
		Symbol    string    `json:"symbol"`
		Category  string    `json:"category"`
		Start     time.Time `json:"start_available_at"`
		End       time.Time `json:"end_available_at"`
		AsOf      time.Time `json:"as_of"`
	}
	if !decodeJSON(w, r, 8<<10, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	requestDigest := digestText("gexbot-replay-request-v1\n" + strings.TrimSpace(input.RequestID) + "\n" + input.Symbol + "\n" + input.Category + "\n" + input.Start.UTC().Format(time.RFC3339Nano) + "\n" + input.End.UTC().Format(time.RFC3339Nano) + "\n" + input.AsOf.UTC().Format(time.RFC3339Nano))
	replayID := uuidFromDigest("gexbot-replay-v1\n" + requestDigest)
	var raw []byte
	err := p.withRole(r.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(r.Context(), `SELECT research.create_gexbot_replay($1::UUID,$2,$3,$4,$5,$6,$7)::TEXT`, replayID, requestDigest, input.Symbol, input.Category, input.Start.UTC(), input.End.UTC(), input.AsOf.UTC()).Scan(&raw)
	})
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid replay request"})
		return
	}
	writeRawJSON(w, raw)
}

func (p *provider) nextReplay(w http.ResponseWriter, r *http.Request) {
	if !matchesBearer(r, p.readKey) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		Generation int64 `json:"generation"`
	}
	if !decodeJSON(w, r, 4<<10, &input) {
		return
	}
	replayID := strings.TrimSpace(r.PathValue("id"))
	var raw []byte
	err := p.withRole(r.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(r.Context(), `SELECT research.consume_gexbot_replay($1::UUID,$2)::TEXT`, replayID, input.Generation).Scan(&raw)
	})
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "replay cursor unavailable"})
		return
	}
	writeRawJSON(w, raw)
}

func (p *provider) record(ctx context.Context, input observationInput) (json.RawMessage, error) {
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	if !safeSymbol(input.Symbol) || !validCategory(input.Category) || (input.SourceKind != "provider_poll" && input.SourceKind != "collector_push" && input.SourceKind != "legacy_kernel_import") ||
		!validUTC(input.SourceTimestamp) || !validUTC(input.ObservedAt) || !validUTC(input.FetchedAt) || input.FetchedAt.Before(input.ObservedAt) ||
		len(input.Payload) == 0 || len(input.Payload) > maxPayloadBytes || !json.Valid(input.Payload) || !jsonObject(input.Payload) {
		return nil, errors.New("invalid observation input")
	}
	payloadDigest := digestBytes(input.Payload)
	if input.ObservationID == "" {
		input.ObservationID = uuidFromDigest(strings.Join([]string{"gexbot-observation-v1", input.SourceKind, input.Symbol, input.Category, input.SourceTimestamp.UTC().Format(time.RFC3339Nano), input.ObservedAt.UTC().Format(time.RFC3339Nano), payloadDigest}, "\n"))
	}
	if !validUUID(input.ObservationID) {
		return nil, errors.New("invalid observation identifier")
	}
	if input.Spot == nil && input.ZeroGamma == nil && input.MajorPosVol == nil && input.MajorPosOI == nil && input.MajorNegVol == nil && input.MajorNegOI == nil {
		var decoded gexbotPayload
		if err := json.Unmarshal(input.Payload, &decoded); err != nil || decoded.Timestamp < 1 || strings.ToUpper(decoded.Ticker) != input.Symbol {
			return nil, errors.New("payload does not match GEXBOT Classic")
		}
		input.Spot, input.ZeroGamma, input.MajorPosVol, input.MajorPosOI, input.MajorNegVol, input.MajorNegOI = decoded.Spot, decoded.ZeroGamma, decoded.MajorPosVol, decoded.MajorPosOI, decoded.MajorNegVol, decoded.MajorNegOI
	}
	rawOriginDigest := digestText(strings.Join([]string{"research.gexbot_raw_observation.v1", input.ObservationID, input.SourceKind, input.Symbol, input.Category,
		input.SourceTimestamp.UTC().Format(time.RFC3339Nano), input.ObservedAt.UTC().Format(time.RFC3339Nano), input.FetchedAt.UTC().Format(time.RFC3339Nano), payloadDigest}, "\n"))
	rawRef, err := p.commitRaw(ctx, input.ObservationID, input.Payload, payloadDigest, rawOriginDigest)
	if err != nil {
		return nil, err
	}
	var result []byte
	var legacyAvailable any
	if input.SourceKind == "legacy_kernel_import" {
		legacyAvailable = input.AvailableAt.UTC()
	}
	err = p.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT research.record_gexbot_observation($1::UUID,$2,$3,$4,$5,$6,$7,$8::UUID,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)::TEXT`,
			input.ObservationID, input.SourceKind, input.Symbol, input.Category, input.SourceTimestamp.UTC(), input.ObservedAt.UTC(), input.FetchedAt.UTC(), rawRef.BlobID, rawRef.ContentDigest, rawRef.SizeBytes, rawOriginDigest,
			input.Spot, input.ZeroGamma, input.MajorPosVol, input.MajorPosOI, input.MajorNegVol, input.MajorNegOI, legacyAvailable).Scan(&result)
	})
	if err != nil || len(result) == 0 || !json.Valid(result) {
		return nil, fmt.Errorf("persist GEXBOT observation: %w", err)
	}
	return json.RawMessage(result), nil
}

func (p *provider) commitRaw(ctx context.Context, observationID string, raw []byte, contentDigest, originDigest string) (blob.BlobRef, error) {
	size := int64(len(raw))
	stageID := uuidFromDigest("gexbot-raw-stage-v1\n" + p.principal + "\n" + observationID)
	if resumed, found, err := p.resumeRawCommit(ctx, stageID, contentDigest, size, observationID, originDigest); err != nil {
		return blob.BlobRef{}, err
	} else if found {
		return resumed, nil
	}
	grant, err := p.beginStage(ctx, stageID, contentDigest, size)
	if err != nil {
		return blob.BlobRef{}, err
	}
	staged, err := p.store.Stage(ctx, grant, bytes.NewReader(raw), p)
	if err != nil {
		return blob.BlobRef{}, fmt.Errorf("stage GEXBOT raw payload: %w", err)
	}
	if err := p.store.Materialize(ctx, staged, p); err != nil {
		if resumed, resumeErr := p.finishRawCommit(ctx, stageID, staged, observationID, originDigest); resumeErr == nil {
			// A prior process may have committed Blob metadata before crashing
			// before the immutable observation row. Resume that exact Blob.
			return resumed, nil
		}
		return blob.BlobRef{}, fmt.Errorf("materialize GEXBOT raw payload: %w", err)
	}
	return p.finishRawCommit(ctx, stageID, staged, observationID, originDigest)
}

func (p *provider) resumeRawCommit(ctx context.Context, stageID, contentDigest string, size int64, observationID, originDigest string) (blob.BlobRef, bool, error) {
	result := blob.BlobRef{SchemaRevision: 1, Origin: contracts.RecordRef{Owner: contracts.OwnerGEXBOTProvider, RecordType: "gexbot_raw_observation", RecordID: observationID, SchemaRevision: 1, RecordDigest: originDigest}}
	err := p.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT blob_id::TEXT,content_digest,media_type,size_bytes,committed_at FROM blob.gexbot_resume_committed_stage($1::UUID,$2,$3,$4,$5,$6,$7,$8)`,
			stageID, p.principal, contentDigest, size, "gexbot_raw_observation", observationID, originDigest, p.principal).
			Scan(&result.BlobID, &result.ContentDigest, &result.MediaType, &result.SizeBytes, &result.CommittedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return blob.BlobRef{}, false, nil
	}
	result.CommittedAt = result.CommittedAt.UTC()
	if err != nil || result.Validate() != nil {
		return blob.BlobRef{}, false, fmt.Errorf("resume GEXBOT raw payload: %w", errors.Join(err, result.Validate()))
	}
	return result, true, nil
}

func (p *provider) finishRawCommit(ctx context.Context, stageID string, staged blob.StagedBlob, observationID, originDigest string) (blob.BlobRef, error) {
	result := blob.BlobRef{SchemaRevision: 1, Origin: contracts.RecordRef{Owner: contracts.OwnerGEXBOTProvider, RecordType: "gexbot_raw_observation", RecordID: observationID, SchemaRevision: 1, RecordDigest: originDigest}}
	err := p.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT blob_id::TEXT,content_digest,media_type,size_bytes,committed_at FROM blob.gexbot_commit_stage($1::UUID,$2,$3,$4,$5,$6,$7,$8)`,
			stageID, p.principal, staged.ContentDigest, staged.SizeBytes, "gexbot_raw_observation", observationID, originDigest, p.principal).
			Scan(&result.BlobID, &result.ContentDigest, &result.MediaType, &result.SizeBytes, &result.CommittedAt)
	})
	result.CommittedAt = result.CommittedAt.UTC()
	if err != nil || result.Validate() != nil {
		return blob.BlobRef{}, fmt.Errorf("commit GEXBOT raw payload: %w", errors.Join(err, result.Validate()))
	}
	return result, nil
}

func (p *provider) beginStage(ctx context.Context, stageID, digest string, size int64) (blob.StageGrant, error) {
	var returnedID string
	var maxBytes int64
	var issuedAt, expiresAt time.Time
	err := p.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT stage_id::TEXT,max_bytes,issued_at,expires_at FROM blob.gexbot_begin_stage($1::UUID,$2,$3,$4,$5,$6,$7,$8)`,
			stageID, p.principal, "application/json", size, digest, size, stageTTLSeconds, p.principal).Scan(&returnedID, &maxBytes, &issuedAt, &expiresAt)
	})
	grant := blob.StageGrant{SchemaRevision: 1, StageID: returnedID, PrincipalID: p.principal, MediaType: "application/json", MaxBytes: maxBytes, ExpectedDigest: digest, ExpectedSizeBytes: &size, IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC()}
	if err != nil || grant.Validate() != nil {
		return blob.StageGrant{}, fmt.Errorf("begin GEXBOT raw stage: %w", errors.Join(err, grant.Validate()))
	}
	return grant, nil
}

func (p *provider) AuthorizeBlobStage(ctx context.Context, grant blob.StageGrant) error {
	if grant.Validate() != nil || grant.PrincipalID != p.principal || grant.ExpectedSizeBytes == nil {
		return errors.New("invalid GEXBOT Blob stage grant")
	}
	again, err := p.beginStage(ctx, grant.StageID, grant.ExpectedDigest, *grant.ExpectedSizeBytes)
	if err != nil || !sameGrant(again, grant) {
		if err != nil {
			log.Printf("GEXBOT Blob stage authorization: %v", err)
		}
		return errors.New("GEXBOT Blob stage authorization denied")
	}
	return nil
}

func sameGrant(left, right blob.StageGrant) bool {
	if left.SchemaRevision != right.SchemaRevision || left.StageID != right.StageID || left.PrincipalID != right.PrincipalID ||
		left.MediaType != right.MediaType || left.MaxBytes != right.MaxBytes || left.ExpectedDigest != right.ExpectedDigest ||
		left.IssuedAt != right.IssuedAt || left.ExpiresAt != right.ExpiresAt {
		return false
	}
	if left.ExpectedSizeBytes == nil || right.ExpectedSizeBytes == nil {
		return left.ExpectedSizeBytes == nil && right.ExpectedSizeBytes == nil
	}
	return *left.ExpectedSizeBytes == *right.ExpectedSizeBytes
}

func (p *provider) AuthorizeBlobMaterialize(ctx context.Context, staged blob.StagedBlob) error {
	if staged.Validate() != nil || staged.Grant.PrincipalID != p.principal {
		return errors.New("invalid GEXBOT materialization")
	}
	err := p.withRole(ctx, func(tx *sql.Tx) error {
		var accepted bool
		return tx.QueryRowContext(ctx, `SELECT blob.gexbot_record_stage_facts($1::UUID,$2,$3,$4,$5)`, staged.Grant.StageID, p.principal, staged.ContentDigest, staged.SizeBytes, p.principal).Scan(&accepted)
	})
	if err != nil {
		log.Printf("GEXBOT Blob materialization authorization: %v", err)
	}
	return err
}

func (p *provider) startCollector(ctx context.Context) {
	go func() {
		last := map[string]time.Time{}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for now := range ticker.C {
			if !collectionWindow(now) || now.Second()%30 > 2 {
				continue
			}
			bucket := now.UTC().Truncate(30 * time.Second)
			for _, category := range gexbotCategories {
				if last[category].Equal(bucket) {
					continue
				}
				last[category] = bucket
				go func(category string, observed time.Time) {
					input, err := p.fetchClassic(ctx, "SPX", category, observed)
					if err == nil {
						_, err = p.record(ctx, input)
					}
					if err != nil {
						log.Printf("GEXBOT provider collector SPX/%s: %v", category, err)
					}
				}(category, bucket)
			}
		}
	}()
}

func (p *provider) fetchClassic(ctx context.Context, symbol, category string, observed time.Time) (observationInput, error) {
	requestCtx, cancel := context.WithTimeout(ctx, providerAPITimeout)
	defer cancel()
	baseURL := strings.TrimRight(p.apiBaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.gex.bot"
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/v2/"+symbol+"/classic/"+category, nil)
	if err != nil {
		return observationInput{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Alpheus-GEXBOT-ResearchProvider/1.0")
	response, err := p.http.Do(req)
	if err != nil {
		return observationInput{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxPayloadBytes+1))
	if err != nil || response.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > maxPayloadBytes || !json.Valid(raw) {
		return observationInput{}, errors.New("GEXBOT Classic response unavailable")
	}
	var payload gexbotPayload
	if json.Unmarshal(raw, &payload) != nil || payload.Timestamp < 1 || strings.ToUpper(payload.Ticker) != symbol {
		return observationInput{}, errors.New("GEXBOT Classic response schema mismatch")
	}
	fetched := time.Now().UTC()
	return observationInput{SourceKind: "provider_poll", Symbol: symbol, Category: category, SourceTimestamp: time.Unix(payload.Timestamp, 0).UTC(), ObservedAt: observed.UTC(), FetchedAt: fetched, Payload: raw,
		Spot: payload.Spot, ZeroGamma: payload.ZeroGamma, MajorPosVol: payload.MajorPosVol, MajorPosOI: payload.MajorPosOI, MajorNegVol: payload.MajorNegVol, MajorNegOI: payload.MajorNegOI}, nil
}

func (p *provider) withRole(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+providerRole); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func collectionWindow(now time.Time) bool {
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

func decodeJSON(w http.ResponseWriter, r *http.Request, max int64, target any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSON(w http.ResponseWriter, raw []byte) {
	if !json.Valid(raw) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "provider response invalid"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func readRequiredSecret(name string) (string, error) {
	value, err := readOptionalSecret(name)
	if err != nil || value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func readOptionalSecret(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return "", fmt.Errorf("secret unavailable")
	}
	return strings.TrimSpace(string(data)), nil
}

func matchesBearer(r *http.Request, token string) bool {
	expected := []byte("Bearer " + token)
	values := r.Header.Values("Authorization")
	return token != "" && len(values) == 1 && len(values[0]) == len(expected) && subtle.ConstantTimeCompare([]byte(values[0]), expected) == 1
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func digestText(value string) string { return digestBytes([]byte(value)) }

func uuidFromDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && !value.After(time.Now().UTC())
}

func validCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" || value == "gex_one"
}

func safeSymbol(value string) bool {
	if len(value) == 0 || len(value) > 16 {
		return false
	}
	for _, char := range value {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func jsonObject(raw []byte) bool {
	var value any
	return json.Unmarshal(raw, &value) == nil && func() bool { _, ok := value.(map[string]any); return ok }()
}
