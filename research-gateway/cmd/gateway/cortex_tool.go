package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxCortexToolResponseBytes = 64 << 10

type cortexWebFetchAuthorization struct {
	ToolCallID    string `json:"tool_call_id"`
	ToolID        string `json:"tool_id"`
	RequestDigest string `json:"request_digest"`
	URL           string `json:"url"`
	MaxChars      int    `json:"max_chars"`
}

type cortexToolResult struct {
	Receipt struct {
		SchemaRevision int    `json:"schema_revision"`
		ReceiptID      string `json:"receipt_id"`
		ToolCallID     string `json:"tool_call_id"`
		ToolID         string `json:"tool_id"`
		RequestDigest  string `json:"request_digest"`
		State          string `json:"state"`
		Evidence       struct {
			Owner        string `json:"owner"`
			RecordType   string `json:"record_type"`
			RecordID     string `json:"record_id"`
			Schema       int    `json:"schema_revision"`
			RecordDigest string `json:"record_digest"`
		} `json:"evidence"`
		Executor struct {
			PrincipalID string `json:"principal_id"`
			Kind        string `json:"kind"`
			Audience    string `json:"audience"`
		} `json:"executor"`
		CompletedAt time.Time `json:"completed_at"`
	} `json:"receipt"`
	Evidence struct {
		SchemaRevision int       `json:"schema_revision"`
		EvidenceID     string    `json:"evidence_id"`
		ToolCallID     string    `json:"tool_call_id"`
		Source         string    `json:"source"`
		URL            string    `json:"url"`
		Title          string    `json:"title"`
		ContentType    string    `json:"content_type"`
		Text           string    `json:"text"`
		Truncated      bool      `json:"truncated"`
		ContentDigest  string    `json:"content_digest"`
		ObservedAt     time.Time `json:"observed_at"`
		AvailableAt    time.Time `json:"available_at"`
		ArchivedAt     time.Time `json:"archived_at"`
	} `json:"evidence"`
}

func (g *gateway) configureCortexTool() error {
	tokenPath := strings.TrimSpace(os.Getenv("CORTEX_RESEARCH_TOKEN_FILE"))
	dbPath := strings.TrimSpace(os.Getenv("RESEARCH_DATABASE_URL_FILE"))
	if tokenPath == "" && dbPath == "" {
		return nil // Existing Kernel-only compatibility endpoints remain usable in tests and isolated deployments.
	}
	if tokenPath == "" || dbPath == "" {
		return fmt.Errorf("CORTEX_RESEARCH_TOKEN_FILE and RESEARCH_DATABASE_URL_FILE must be configured together")
	}
	token, err := readGatewaySecret(tokenPath)
	if err != nil {
		return fmt.Errorf("load Cortex tool token: %w", err)
	}
	databaseURL, err := readGatewaySecret(dbPath)
	if err != nil {
		return fmt.Errorf("load Research database URL: %w", err)
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("open Research database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping Research database: %w", err)
	}
	g.cortexToken = token
	g.db = db
	g.principal = env("RESEARCH_PRINCIPAL_ID", "research-gateway-1")
	return nil
}

func readGatewaySecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 64<<10 {
		return "", fmt.Errorf("secret unavailable")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("secret unavailable")
	}
	return value, nil
}

func (g *gateway) cortexWebFetch(w http.ResponseWriter, r *http.Request) {
	if g == nil || g.db == nil || g.cortexToken == "" || !tokenMatches(bearerToken(r), g.cortexToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		ToolCallID string `json:"tool_call_id"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	input.ToolCallID = strings.TrimSpace(input.ToolCallID)
	if !gatewayIdentifier(input.ToolCallID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tool call"})
		return
	}
	auth, err := g.authorizeCortexWebFetch(r.Context(), input.ToolCallID)
	if err != nil {
		log.Printf("Cortex web fetch authorization failed: %v", err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tool authorization unavailable"})
		return
	}
	document, err := g.fetchPublicPage(r.Context(), auth.URL, auth.MaxChars)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "web page unavailable"})
		return
	}
	result, err := g.recordCortexWebFetchReceipt(r.Context(), auth.ToolCallID, document)
	if err != nil {
		log.Printf("Cortex web fetch receipt persistence failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	}
	if !validCortexToolResult(result, auth) {
		log.Printf("Cortex web fetch receipt validation failed")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (g *gateway) authorizeCortexWebFetch(ctx context.Context, toolCallID string) (cortexWebFetchAuthorization, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.get_cortex_web_fetch_authorization($1)::TEXT`, toolCallID).Scan(&raw)
	})
	if err != nil || len(raw) == 0 || len(raw) > maxCortexToolResponseBytes {
		return cortexWebFetchAuthorization{}, fmt.Errorf("authorization query unavailable: %w", err)
	}
	var auth cortexWebFetchAuthorization
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&auth) != nil || decoder.Decode(&struct{}{}) != io.EOF || auth.ToolCallID != toolCallID ||
		auth.ToolID != "research_web_fetch" || !gatewayDigest(auth.RequestDigest) || auth.MaxChars < 1 || auth.MaxChars > 12000 ||
		!safeExternalURL(auth.URL) {
		return cortexWebFetchAuthorization{}, fmt.Errorf("authorization response invalid")
	}
	return auth, nil
}

func (g *gateway) recordCortexWebFetchReceipt(ctx context.Context, toolCallID string, document webPageDocument) (cortexToolResult, error) {
	documentRaw, err := json.Marshal(document)
	if err != nil {
		return cortexToolResult{}, err
	}
	var raw []byte
	err = g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.record_research_web_fetch_receipt($1,$2::JSONB)::TEXT`, toolCallID, string(documentRaw)).Scan(&raw)
	})
	if err != nil || len(raw) == 0 || len(raw) > maxCortexToolResponseBytes {
		return cortexToolResult{}, fmt.Errorf("receipt unavailable")
	}
	var result cortexToolResult
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return cortexToolResult{}, fmt.Errorf("receipt decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cortexToolResult{}, fmt.Errorf("receipt trailing data: %w", err)
	}
	return result, nil
}

func (g *gateway) withResearchRole(ctx context.Context, fn func(*sql.Tx) error) error {
	if g == nil || g.db == nil || fn == nil {
		return fmt.Errorf("research database unavailable")
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE alpheus_research_gateway"); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func validCortexToolResult(result cortexToolResult, auth cortexWebFetchAuthorization) bool {
	return result.Receipt.SchemaRevision == 1 && gatewayIdentifier(result.Receipt.ReceiptID) && result.Receipt.ToolCallID == auth.ToolCallID && result.Receipt.ToolID == auth.ToolID &&
		result.Receipt.RequestDigest == auth.RequestDigest && result.Receipt.State == "succeeded" &&
		result.Receipt.Evidence.Owner == "research_gateway" && result.Receipt.Evidence.RecordType == "web_fetch_evidence" &&
		gatewayIdentifier(result.Receipt.Evidence.RecordID) && result.Receipt.Evidence.Schema == 1 && gatewayDigest(result.Receipt.Evidence.RecordDigest) &&
		result.Receipt.Executor.PrincipalID == "research-gateway-1" && result.Receipt.Executor.Kind == "workload" && result.Receipt.Executor.Audience == "research_gateway" &&
		!result.Receipt.CompletedAt.IsZero() && result.Evidence.SchemaRevision == 1 && result.Evidence.EvidenceID == result.Receipt.Evidence.RecordID && result.Evidence.ToolCallID == auth.ToolCallID &&
		result.Evidence.Source == "web-page-untrusted" && safeExternalURL(result.Evidence.URL) && len(result.Evidence.Title) <= 1000 &&
		allowedWebMediaType(result.Evidence.ContentType) && strings.TrimSpace(result.Evidence.Text) != "" && len(result.Evidence.Text) <= auth.MaxChars &&
		gatewayDigest(result.Evidence.ContentDigest) && !result.Evidence.ObservedAt.IsZero() && !result.Evidence.AvailableAt.IsZero() && !result.Evidence.ArchivedAt.IsZero() &&
		!result.Evidence.AvailableAt.Before(result.Evidence.ObservedAt) && !result.Evidence.ArchivedAt.Before(result.Evidence.AvailableAt)
}

func gatewayIdentifier(value string) bool {
	return len(value) > 0 && len(value) <= 200 && !strings.ContainsAny(value, " \t\r\n\x00")
}

func gatewayDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
