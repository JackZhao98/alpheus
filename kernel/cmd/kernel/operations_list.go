package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"alpheus/kernel/internal/store"
)

const (
	defaultOperationPageSize = 25
	maxOperationPageSize     = 100
	maxOperationCursorBytes  = 512
)

var operationStatuses = map[string]bool{
	"auto_approved":  true,
	"pending_review": true,
	"approved":       true,
	"rejected":       true,
	"executed":       true,
	"failed":         true,
	"expired":        true,
}

func encodeOperationCursor(row store.OperationRow) string {
	raw, err := json.Marshal(store.OperationCursor{TS: row.TS.UTC(), ID: row.ID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOperationCursor(encoded string) (*store.OperationCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > maxOperationCursorBytes {
		return nil, fmt.Errorf("cursor is too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("cursor is invalid")
	}
	var cursor store.OperationCursor
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return nil, fmt.Errorf("cursor is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("cursor is invalid")
	}
	if cursor.TS.IsZero() || !validUUID(cursor.ID) {
		return nil, fmt.Errorf("cursor is invalid")
	}
	cursor.TS = cursor.TS.UTC()
	return &cursor, nil
}

func parseOperationPage(r *http.Request) (string, int, *store.OperationCursor, error) {
	query := r.URL.Query()
	for key, values := range query {
		if key != "status" && key != "limit" && key != "cursor" {
			return "", 0, nil, fmt.Errorf("unknown query parameter")
		}
		if len(values) != 1 {
			return "", 0, nil, fmt.Errorf("query parameter must appear once")
		}
	}
	status := query.Get("status")
	if status != "" && !operationStatuses[status] {
		return "", 0, nil, fmt.Errorf("status is invalid")
	}
	limit := defaultOperationPageSize
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return "", 0, nil, fmt.Errorf("limit must be a positive integer")
		}
		limit = min(parsed, maxOperationPageSize)
	}
	cursor, err := decodeOperationCursor(query.Get("cursor"))
	if err != nil {
		return "", 0, nil, err
	}
	return status, limit, cursor, nil
}

func (s *server) listOperations(w http.ResponseWriter, r *http.Request) {
	status, limit, cursor, err := parseOperationPage(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rows, err := s.store.ListOperations(status, limit+1, cursor)
	if err != nil {
		writeInternalError(w, "list operations", err)
		return
	}
	nextCursor := ""
	if len(rows) > limit {
		rows = rows[:limit]
		nextCursor = encodeOperationCursor(rows[len(rows)-1])
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"operations":  rows,
		"next_cursor": nextCursor,
		"as_of":       time.Now().UTC(),
		"source":      "kernel_db",
	})
}
