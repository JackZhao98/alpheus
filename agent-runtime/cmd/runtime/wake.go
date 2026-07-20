package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

const maxWakeBodyBytes int64 = 1 << 20
const maxQueryBodyBytes int64 = 16 << 10
const maxWakeDedupEntries = 4096

type queryRunner func(roles.Role, string, string) (contracts.Output, error)

type wakeDeduper struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newWakeDeduper() *wakeDeduper {
	return &wakeDeduper{seen: make(map[string]time.Time)}
}

// accept returns true exactly once for a (role, occurrence_id) pair within
// the bounded process-local retry ledger.
func (d *wakeDeduper) accept(role, occurrenceID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := role + "\x00" + occurrenceID
	if _, exists := d.seen[key]; exists {
		return false
	}
	if len(d.seen) >= maxWakeDedupEntries {
		var oldestKey string
		var oldest time.Time
		for candidate, acceptedAt := range d.seen {
			if oldestKey == "" || acceptedAt.Before(oldest) {
				oldestKey, oldest = candidate, acceptedAt
			}
		}
		delete(d.seen, oldestKey)
	}
	d.seen[key] = time.Now().UTC()
	return true
}

func wakeTokenMatches(candidate, expected string) bool {
	if candidate == "" || expected == "" {
		return false
	}
	candidateHash := sha256.Sum256([]byte(candidate))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:]) == 1
}

func wakeBearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return ""
	}
	return token
}

func newWakeHandler(token string, roleByName map[string]roles.Role, run func(roles.Role, string, string)) http.Handler {
	return newRuntimeHandler(token, roleByName, run, nil)
}

func newRuntimeHandler(token string, roleByName map[string]roles.Role, run func(roles.Role, string, string), query queryRunner) http.Handler {
	deduper := newWakeDeduper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /wake", func(w http.ResponseWriter, r *http.Request) {
		if !wakeTokenMatches(wakeBearerToken(r), token) {
			wakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxWakeBodyBytes)
		var in struct {
			Role         string `json:"role"`
			Trigger      string `json:"trigger"`
			OccurrenceID string `json:"occurrence_id"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				wakeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
			} else {
				wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			}
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				wakeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds 1 MiB"})
			} else {
				wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON value"})
			}
			return
		}
		role, ok := roleByName[in.Role]
		if !ok {
			wakeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown role"})
			return
		}
		trigger := strings.TrimSpace(in.Trigger)
		if trigger != "spine" {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "trigger must be spine"})
			return
		}
		occurrenceID := strings.TrimSpace(in.OccurrenceID)
		if !safeOccurrenceID(occurrenceID) {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid occurrence_id"})
			return
		}
		accepted := deduper.accept(role.Role, occurrenceID)
		if accepted {
			run(role, trigger, occurrenceID)
		}
		wakeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": true, "deduplicated": !accepted,
			"role": role.Role, "occurrence_id": occurrenceID,
		})
	})
	mux.HandleFunc("POST /query", func(w http.ResponseWriter, r *http.Request) {
		if !wakeTokenMatches(wakeBearerToken(r), token) {
			wakeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if query == nil {
			wakeJSON(w, http.StatusNotFound, map[string]string{"error": "query unavailable"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "content-type must be application/json"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxQueryBodyBytes)
		var in struct {
			Symbol string `json:"symbol"`
			Query  string `json:"query"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain exactly one JSON value"})
			return
		}
		if len(strings.TrimSpace(in.Query)) == 0 || len(in.Query) > 4000 {
			wakeJSON(w, http.StatusBadRequest, map[string]string{"error": "query must contain 1-4000 bytes"})
			return
		}
		role, ok := roleByName["scout"]
		if !ok {
			wakeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scout unavailable"})
			return
		}
		output, err := query(role, in.Symbol, in.Query)
		if err != nil {
			wakeJSON(w, http.StatusBadGateway, map[string]string{"error": "agent query failed"})
			return
		}
		wakeJSON(w, http.StatusOK, map[string]any{"role": role.Role, "output": output})
	})
	return mux
}

func safeOccurrenceID(occurrenceID string) bool {
	if len(occurrenceID) == 0 || len(occurrenceID) > 128 {
		return false
	}
	for _, char := range occurrenceID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == ':' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func wakeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
