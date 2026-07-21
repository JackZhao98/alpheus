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

type queryResult struct {
	Role              string
	Workflow          string
	RequestedWorkflow string
	Output            contracts.Output
	IntentOutput      contracts.Output
	ScoutOutput       contracts.Output
	Cognition         string
	Provider          string
	Model             string
}

type queryRunner func(string, string, string, string) (queryResult, error)

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
			wakeError(w, http.StatusUnauthorized, "runtime_auth_invalid", "unauthorized")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			wakeError(w, http.StatusBadRequest, "runtime_content_type_invalid", "content-type must be application/json")
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
				wakeError(w, http.StatusRequestEntityTooLarge, "runtime_wake_body_too_large", "request body exceeds 1 MiB")
			} else {
				wakeError(w, http.StatusBadRequest, "runtime_wake_json_invalid", "invalid JSON body")
			}
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				wakeError(w, http.StatusRequestEntityTooLarge, "runtime_wake_body_too_large", "request body exceeds 1 MiB")
			} else {
				wakeError(w, http.StatusBadRequest, "runtime_wake_json_multiple_values", "request body must contain exactly one JSON value")
			}
			return
		}
		role, ok := roleByName[in.Role]
		if !ok {
			wakeError(w, http.StatusNotFound, "runtime_role_unknown", "unknown role")
			return
		}
		trigger := strings.TrimSpace(in.Trigger)
		if trigger != "spine" {
			wakeError(w, http.StatusBadRequest, "runtime_wake_trigger_invalid", "trigger must be spine")
			return
		}
		occurrenceID := strings.TrimSpace(in.OccurrenceID)
		if !safeOccurrenceID(occurrenceID) {
			wakeError(w, http.StatusBadRequest, "runtime_occurrence_id_invalid", "invalid occurrence_id")
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
			wakeError(w, http.StatusUnauthorized, "runtime_auth_invalid", "unauthorized")
			return
		}
		if query == nil {
			wakeError(w, http.StatusNotFound, "runtime_query_unavailable", "query unavailable")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			wakeError(w, http.StatusBadRequest, "runtime_content_type_invalid", "content-type must be application/json")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxQueryBodyBytes)
		var in struct {
			Workflow     string `json:"workflow"`
			Symbol       string `json:"symbol"`
			Query        string `json:"query"`
			OpenAIAPIKey string `json:"openai_api_key"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&in); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				wakeError(w, http.StatusRequestEntityTooLarge, "runtime_query_body_too_large", "request body exceeds 16 KiB")
			} else {
				wakeError(w, http.StatusBadRequest, "runtime_query_json_invalid", "invalid JSON body")
			}
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			wakeError(w, http.StatusBadRequest, "runtime_query_json_multiple_values", "request body must contain exactly one JSON value")
			return
		}
		if len(strings.TrimSpace(in.Query)) == 0 || len(in.Query) > 4000 {
			wakeError(w, http.StatusBadRequest, "runtime_query_invalid", "query must contain 1-4000 bytes")
			return
		}
		in.OpenAIAPIKey = strings.TrimSpace(in.OpenAIAPIKey)
		if !validOptionalAPIKey(in.OpenAIAPIKey) {
			wakeError(w, http.StatusBadRequest, "runtime_query_api_key_invalid", "invalid OpenAI API token")
			return
		}
		in.Workflow = strings.TrimSpace(in.Workflow)
		if in.Workflow == "" {
			in.Workflow = "scout"
		}
		if in.Workflow != "auto" && in.Workflow != "scout" && in.Workflow != "team" {
			wakeError(w, http.StatusBadRequest, "runtime_query_workflow_invalid", "workflow must be auto, scout or team")
			return
		}
		result, err := query(in.Workflow, in.Symbol, in.Query, in.OpenAIAPIKey)
		if err != nil {
			wakeError(w, http.StatusBadGateway, "runtime_query_execution_failed", "agent query failed")
			return
		}
		response := map[string]any{
			"role":      result.Role,
			"workflow":  result.Workflow,
			"cognition": result.Cognition,
			"provider":  result.Provider,
			"model":     result.Model,
			"output":    result.Output,
		}
		if result.RequestedWorkflow != "" {
			response["requested_workflow"] = result.RequestedWorkflow
		}
		if result.IntentOutput != nil {
			response["intent_output"] = result.IntentOutput
		}
		if result.ScoutOutput != nil {
			response["scout_output"] = result.ScoutOutput
		}
		wakeJSON(w, http.StatusOK, response)
	})
	return mux
}

func validOptionalAPIKey(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
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

// wakeError keeps a machine-stable diagnosis next to the user-safe message.
// Do not pass provider responses, credentials, or untrusted content here.
func wakeError(w http.ResponseWriter, status int, code, message string) {
	wakeJSON(w, status, map[string]string{"error_code": code, "error": message})
}
