package inputgateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputcontract"
)

// NewHandler exposes Cortex intake independently from Kernel.  It deliberately
// admits immutable user facts only; scheduling cognition is a later Cortex
// Control responsibility and cannot occur as an HTTP side effect here.
func NewHandler(gateway *Gateway, actor contracts.AuditActor, subjectFromRequest func(*http.Request) (contracts.AuditActor, error)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeHTTPJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "cortex-input-gateway"})
	})
	mux.HandleFunc("POST /v1/user-requests", func(w http.ResponseWriter, request *http.Request) {
		if gateway == nil || subjectFromRequest == nil {
			writeHTTPError(w, http.StatusServiceUnavailable, "input_gateway_unavailable", "Cortex input gateway is unavailable")
			return
		}
		var body struct {
			ConversationID        string `json:"conversation_id"`
			ConversationCreatedAt string `json:"conversation_created_at"`
			RequestID             string `json:"request_id"`
			Kind                  string `json:"kind"`
			Text                  string `json:"text"`
			IdempotencyKey        string `json:"idempotency_key"`
			CausationID           string `json:"causation_id"`
			CorrelationID         string `json:"correlation_id"`
			Deadline              string `json:"deadline"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeHTTPError(w, http.StatusBadRequest, "input_gateway_json_invalid", "request body must be one valid JSON object")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeHTTPError(w, http.StatusBadRequest, "input_gateway_json_invalid", "request body must contain exactly one JSON object")
			return
		}
		subject, err := subjectFromRequest(request)
		if err != nil {
			writeHTTPError(w, http.StatusUnauthorized, "input_gateway_subject_unverified", "authenticated user identity is required")
			return
		}
		deadline, err := time.Parse(time.RFC3339Nano, body.Deadline)
		if err != nil || deadline.Location() != time.UTC {
			writeHTTPError(w, http.StatusBadRequest, "input_gateway_deadline_invalid", "deadline must be a UTC RFC3339 timestamp")
			return
		}
		conversationCreatedAt, err := time.Parse(time.RFC3339Nano, body.ConversationCreatedAt)
		if err != nil || conversationCreatedAt.Location() != time.UTC {
			writeHTTPError(w, http.StatusBadRequest, "input_gateway_conversation_time_invalid", "conversation_created_at must be a UTC RFC3339 timestamp")
			return
		}
		result, err := gateway.Admit(request.Context(), Request{Actor: actor, Subject: subject,
			ConversationID: strings.TrimSpace(body.ConversationID), ConversationCreatedAt: conversationCreatedAt,
			RequestID: strings.TrimSpace(body.RequestID),
			Kind:      inputcontract.RequestKind(body.Kind), Text: []byte(body.Text), IdempotencyKey: strings.TrimSpace(body.IdempotencyKey),
			CausationID: strings.TrimSpace(body.CausationID), CorrelationID: strings.TrimSpace(body.CorrelationID), Deadline: deadline})
		if err != nil {
			if coded, ok := err.(*Error); ok {
				log.Printf("Cortex input admission failed: code=%s cause=%v", coded.Code, coded.Cause)
				status := http.StatusBadRequest
				if coded.Code == CodeBlobCommit || coded.Code == CodeAdmission {
					status = http.StatusServiceUnavailable
				}
				writeHTTPError(w, status, coded.Code, coded.Message)
				return
			}
			log.Printf("Cortex input admission failed: code=input_gateway_internal_error cause=%v", err)
			writeHTTPError(w, http.StatusInternalServerError, "input_gateway_internal_error", "Cortex input admission failed")
			return
		}
		writeHTTPJSON(w, http.StatusAccepted, map[string]any{"status": "accepted",
			"conversation_id": result.Command.Conversation.ConversationID, "conversation_created_at": result.Command.Conversation.CreatedAt,
			"request_id": result.Command.Request.RequestID, "request_created_at": result.Command.Request.CreatedAt,
			"request_digest": result.Command.Envelope.RequestDigest})
	})
	return mux
}

func writeHTTPError(w http.ResponseWriter, status int, code, message string) {
	writeHTTPJSON(w, status, map[string]string{"error_code": code, "error": message})
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
