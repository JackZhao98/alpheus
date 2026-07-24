package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
)

type agentRoomRequest struct {
	Mode                  string `json:"mode"`
	Symbol                string `json:"symbol"`
	Query                 string `json:"query"`
	ConversationID        string `json:"conversation_id,omitempty"`
	ConversationCreatedAt string `json:"conversation_created_at,omitempty"`
}

type agentRoom struct {
	ConversationID        string `json:"conversation_id"`
	ConversationCreatedAt string `json:"conversation_created_at"`
	Mode                  string `json:"mode"`
	Title                 string `json:"title"`
	State                 string `json:"state"`
	Generation            int64  `json:"generation"`
	LastRunID             string `json:"last_run_id,omitempty"`
	LastRunState          string `json:"last_run_state,omitempty"`
	MessageCount          int64  `json:"message_count"`
}

type agentRoomUpdate struct {
	ExpectedGeneration int64  `json:"expected_generation"`
	Mode               string `json:"mode"`
	Title              string `json:"title"`
	State              string `json:"state"`
}

func (s *server) postAgentRoomRequest(w http.ResponseWriter, r *http.Request) {
	var input agentRoomRequest
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Mode = strings.TrimSpace(input.Mode)
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Query = strings.TrimSpace(input.Query)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.ConversationCreatedAt = strings.TrimSpace(input.ConversationCreatedAt)
	if input.Mode != "research" {
		writeAgentQueryError(w, http.StatusConflict,
			"agent_room_mode_unavailable",
			"This Agent mode is visible in the roadmap but is not installed")
		return
	}
	if input.Symbol != "" && !validAgentQuerySymbol(input.Symbol) {
		writeAgentQueryError(w, http.StatusBadRequest,
			"agent_room_input_invalid", "Ticker is invalid")
		return
	}
	if input.Query == "" || len(input.Query) > 4000 ||
		(input.ConversationID == "") != (input.ConversationCreatedAt == "") {
		writeAgentQueryError(w, http.StatusBadRequest,
			"agent_room_input_invalid", "Message is invalid")
		return
	}

	title := agentRoomTitle(input.Symbol, input.Query)
	if input.ConversationID != "" {
		room, code := s.fetchAgentRoom(r.Context(), input.ConversationID)
		if code != "" {
			writeAgentQueryError(w, http.StatusServiceUnavailable, code,
				"Agent Room is unavailable")
			return
		}
		if room.State != "active" {
			writeAgentQueryError(w, http.StatusConflict,
				"agent_room_paused", "Resume this Agent Room before sending")
			return
		}
		if room.Mode != input.Mode ||
			room.ConversationCreatedAt != input.ConversationCreatedAt {
			writeAgentQueryError(w, http.StatusConflict,
				"agent_room_changed", "Agent Room state changed")
			return
		}
		title = room.Title
	}

	text := input.Query
	if input.Symbol != "" {
		text = "Symbol: " + input.Symbol + "\n\n" + input.Query
	}
	accepted, code := s.submitCortexUserRequest(r.Context(),
		agentQueryRequest{
			Symbol: input.Symbol, Query: input.Query,
			ConversationID:        input.ConversationID,
			ConversationCreatedAt: input.ConversationCreatedAt,
		}, "agent-room", text)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code,
			"Cortex request was not accepted")
		return
	}
	room, code := s.recordAgentRoom(r.Context(), accepted, input.Mode, title)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code,
			"Agent Room could not be recorded")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": accepted.RunID, "status": "running",
		"conversation_id":         accepted.ConversationID,
		"conversation_created_at": room.ConversationCreatedAt,
		"room":                    room, "trace": []any{},
	})
}

func (s *server) getAgentRooms(w http.ResponseWriter, r *http.Request) {
	raw, status, code := s.agentRoomUpstream(r.Context(),
		http.MethodGet, "/v1/agent-rooms", nil)
	s.writeAgentRoomUpstream(w, raw, status, code)
}

func (s *server) getAgentRoom(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !validCortexConversationID(id) {
		writeAgentQueryError(w, http.StatusBadRequest,
			"agent_room_invalid", "Agent Room is invalid")
		return
	}
	raw, status, code := s.agentRoomUpstream(r.Context(),
		http.MethodGet, "/v1/agent-rooms/"+id, nil)
	s.writeAgentRoomUpstream(w, raw, status, code)
}

func (s *server) patchAgentRoom(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var input agentRoomUpdate
	if !validCortexConversationID(id) || !decodeJSONBody(w, r, &input) {
		if !validCortexConversationID(id) {
			writeAgentQueryError(w, http.StatusBadRequest,
				"agent_room_invalid", "Agent Room is invalid")
		}
		return
	}
	input.Mode = strings.TrimSpace(input.Mode)
	input.Title = strings.TrimSpace(input.Title)
	input.State = strings.TrimSpace(input.State)
	if input.ExpectedGeneration < 1 || !validAgentRoomMode(input.Mode) ||
		!validAgentRoomTitle(input.Title) ||
		(input.State != "active" && input.State != "paused" &&
			input.State != "archived") {
		writeAgentQueryError(w, http.StatusBadRequest,
			"agent_room_update_invalid", "Agent Room update is invalid")
		return
	}
	raw, _ := json.Marshal(input)
	response, status, code := s.agentRoomUpstream(r.Context(),
		http.MethodPatch, "/v1/agent-rooms/"+id, raw)
	s.writeAgentRoomUpstream(w, response, status, code)
}

func (s *server) writeAgentRoomUpstream(
	w http.ResponseWriter,
	raw json.RawMessage,
	status int,
	code string,
) {
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code,
			"Agent Room service is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (s *server) fetchAgentRoom(
	ctx context.Context,
	conversationID string,
) (agentRoom, string) {
	raw, status, code := s.agentRoomUpstream(ctx, http.MethodGet,
		"/v1/agent-rooms/"+conversationID, nil)
	if code != "" || status != http.StatusOK {
		if code == "" {
			code = "agent_room_not_found"
		}
		return agentRoom{}, code
	}
	var result struct {
		Room agentRoom `json:"room"`
	}
	if json.Unmarshal(raw, &result) != nil ||
		!validAgentRoom(result.Room) ||
		result.Room.ConversationID != conversationID {
		return agentRoom{}, "cortex_response_invalid"
	}
	return result.Room, ""
}

func (s *server) recordAgentRoom(
	ctx context.Context,
	accepted cortexSubmission,
	mode string,
	title string,
) (agentRoom, string) {
	body, _ := json.Marshal(map[string]string{
		"mode": mode, "title": title, "run_id": accepted.RunID,
	})
	raw, status, code := s.agentRoomUpstream(ctx, http.MethodPost,
		"/v1/agent-rooms/"+accepted.ConversationID+"/record", body)
	if code != "" || status != http.StatusOK {
		if code == "" {
			code = "agent_room_record_rejected"
		}
		return agentRoom{}, code
	}
	var result struct {
		Status string    `json:"status"`
		Room   agentRoom `json:"room"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status != "recorded" ||
		!validAgentRoom(result.Room) ||
		result.Room.ConversationID != accepted.ConversationID ||
		result.Room.LastRunID != accepted.RunID {
		return agentRoom{}, "cortex_response_invalid"
	}
	return result.Room, ""
}

func (s *server) agentRoomUpstream(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (json.RawMessage, int, string) {
	token, err := s.cortexToken()
	if err != nil {
		return nil, 0, "cortex_credential_unavailable"
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method,
		strings.TrimRight(s.cortexURL, "/")+path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "cortex_unavailable"
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(
		resp.Body, maxAgentQueryResponseBytes+1))
	if err != nil || int64(len(raw)) > maxAgentQueryResponseBytes ||
		!json.Valid(raw) {
		return nil, 0, "cortex_response_invalid"
	}
	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusAccepted &&
		resp.StatusCode != http.StatusConflict &&
		resp.StatusCode != http.StatusNotFound {
		return nil, 0, "cortex_agent_room_unavailable"
	}
	return json.RawMessage(raw), resp.StatusCode, ""
}

func validAgentRoom(room agentRoom) bool {
	created, err := time.Parse(time.RFC3339Nano, room.ConversationCreatedAt)
	return err == nil && created.Location() == time.UTC &&
		validCortexConversationID(room.ConversationID) &&
		validAgentRoomMode(room.Mode) && validAgentRoomTitle(room.Title) &&
		(room.State == "active" || room.State == "paused") &&
		room.Generation > 0 &&
		(room.LastRunID == "" || validCortexConversationID(room.LastRunID))
}

func validAgentRoomMode(mode string) bool {
	return mode == "research" || mode == "spx_gamma" ||
		mode == "equity_discovery" || mode == "watchlist_monitor"
}

func validAgentRoomTitle(title string) bool {
	if title == "" || title != strings.TrimSpace(title) || len(title) > 240 {
		return false
	}
	for _, char := range title {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func agentRoomTitle(symbol string, query string) string {
	title := strings.Join(strings.Fields(query), " ")
	runes := []rune(title)
	if len(runes) > 48 {
		title = string(runes[:48]) + "…"
	}
	if symbol != "" {
		title = symbol + " · " + title
	}
	return title
}
