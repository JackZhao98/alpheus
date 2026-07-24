package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPostAgentRoomRequestCreatesDurableRoomWithoutFakeSymbol(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user-requests":
			calls.Add(1)
			var input struct {
				ConversationID        string `json:"conversation_id"`
				ConversationCreatedAt string `json:"conversation_created_at"`
				RequestID             string `json:"request_id"`
				Text                  string `json:"text"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				t.Fatal("invalid UserRequest")
			}
			if !strings.HasPrefix(input.ConversationID, "agent-room-") ||
				input.Text != "Find the most important earnings catalyst." ||
				input.RequestID == "" {
				t.Fatalf("unexpected UserRequest: %+v", input)
			}
			writeJSON(w, http.StatusAccepted, map[string]any{
				"run_id":                  "run-1",
				"conversation_id":         input.ConversationID,
				"conversation_created_at": input.ConversationCreatedAt,
			})
		case r.Method == http.MethodPost &&
			strings.HasPrefix(r.URL.Path, "/v1/agent-rooms/") &&
			strings.HasSuffix(r.URL.Path, "/record"):
			calls.Add(1)
			conversationID := strings.TrimSuffix(
				strings.TrimPrefix(r.URL.Path, "/v1/agent-rooms/"), "/record")
			var input map[string]string
			if json.NewDecoder(r.Body).Decode(&input) != nil ||
				input["mode"] != "research" ||
				input["run_id"] != "run-1" {
				t.Fatalf("unexpected room record: %+v", input)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "recorded",
				"room": map[string]any{
					"conversation_id":         conversationID,
					"conversation_created_at": "2026-07-24T03:00:00Z",
					"mode":                    "research",
					"title":                   input["title"],
					"state":                   "active",
					"generation":              1,
					"last_run_id":             "run-1",
				},
			})
		default:
			t.Fatalf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer upstream.Close()

	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(tokenPath, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/room-requests",
		strings.NewReader(`{
			"mode":"research",
			"symbol":"",
			"query":"Find the most important earnings catalyst."
		}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.postAgentRoomRequest(response, request)
	if response.Code != http.StatusAccepted || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d body=%s",
			response.Code, calls.Load(), response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"room"`) ||
		!strings.Contains(response.Body.String(), `"conversation_id":"agent-room-`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestPostAgentRoomRequestRejectsPausedContinuationBeforeAdmission(t *testing.T) {
	const conversationID = "agent-room-existing"
	var userRequestCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-rooms/"+conversationID {
			userRequestCalls.Add(1)
			t.Fatalf("unexpected upstream request: %s", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"room": map[string]any{
				"conversation_id":         conversationID,
				"conversation_created_at": "2026-07-24T03:00:00Z",
				"mode":                    "research",
				"title":                   "Paused room",
				"state":                   "paused",
				"generation":              2,
			},
			"messages": []any{},
		})
	}))
	defer upstream.Close()

	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/room-requests",
		strings.NewReader(`{
			"mode":"research",
			"query":"Continue",
			"conversation_id":"agent-room-existing",
			"conversation_created_at":"2026-07-24T03:00:00Z"
		}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.postAgentRoomRequest(response, request)
	if response.Code != http.StatusConflict || userRequestCalls.Load() != 0 ||
		!strings.Contains(response.Body.String(), "agent_room_paused") {
		t.Fatalf("status=%d calls=%d body=%s",
			response.Code, userRequestCalls.Load(), response.Body.String())
	}
}

func TestAgentRoomTitleIsBoundedAndReadable(t *testing.T) {
	title := agentRoomTitle("TSLA",
		"  Explain   the result and identify the most important risk "+
			strings.Repeat("later ", 20))
	if !strings.HasPrefix(title, "TSLA · Explain the result") ||
		len([]rune(strings.TrimPrefix(title, "TSLA · "))) > 49 ||
		!strings.HasSuffix(title, "…") {
		t.Fatalf("unexpected title: %q", title)
	}
}
