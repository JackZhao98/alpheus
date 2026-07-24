package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxMoodyBluesStreamResponseBytes = 64 << 10

type moodyBluesReplayCreateRequest struct {
	RequestID string `json:"request_id"`
	Symbol    string `json:"symbol"`
	Category  string `json:"category"`
	Start     string `json:"start_available_at"`
	End       string `json:"end_available_at"`
	AsOf      string `json:"as_of"`
}

type moodyBluesReplayStepRequest struct {
	Generation int64 `json:"generation"`
}

func registerMoodyBluesStreamHandlers(
	mux *http.ServeMux,
	serviceToken string,
	client *http.Client,
	researchURL string,
	researchToken string,
) {
	if mux == nil {
		return
	}
	mux.HandleFunc(
		"POST /v1/data-streams/gexbot/replays",
		func(w http.ResponseWriter, r *http.Request) {
			if !validBearer(r, serviceToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var input moodyBluesReplayCreateRequest
			if !decodeMoodyBluesStreamJSON(w, r, 8<<10, &input) {
				return
			}
			input.RequestID = strings.TrimSpace(input.RequestID)
			input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
			input.Category = strings.TrimSpace(input.Category)
			start, startErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.Start),
			)
			end, endErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.End),
			)
			asOf, asOfErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.AsOf),
			)
			if input.RequestID == "" || len(input.RequestID) > 200 ||
				input.Symbol != "SPX" ||
				!validMoodyBluesCategory(input.Category) ||
				startErr != nil || endErr != nil || asOfErr != nil ||
				end.Before(start) || asOf.Before(end) ||
				asOf.After(time.Now().UTC()) {
				writeMoodyBluesStreamError(
					w, http.StatusBadRequest, "moody_blues_replay_invalid",
				)
				return
			}
			input.Start = start.UTC().Format(time.RFC3339Nano)
			input.End = end.UTC().Format(time.RFC3339Nano)
			input.AsOf = asOf.UTC().Format(time.RFC3339Nano)
			proxyMoodyBluesStream(
				w, r, client, researchURL, researchToken,
				"/internal/v1/moody-blues/providers/gexbot-classic/replays",
				input,
			)
		},
	)
	mux.HandleFunc(
		"POST /v1/data-streams/gexbot/replays/{id}/next",
		func(w http.ResponseWriter, r *http.Request) {
			if !validBearer(r, serviceToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			replayID := strings.TrimSpace(r.PathValue("id"))
			var input moodyBluesReplayStepRequest
			if !decodeMoodyBluesStreamJSON(w, r, 4<<10, &input) {
				return
			}
			if !validMoodyBluesReplayID(replayID) ||
				input.Generation < 1 {
				writeMoodyBluesStreamError(
					w, http.StatusBadRequest, "moody_blues_cursor_invalid",
				)
				return
			}
			proxyMoodyBluesStream(
				w, r, client, researchURL, researchToken,
				"/internal/v1/moody-blues/providers/gexbot-classic/replays/"+
					replayID+"/next",
				input,
			)
		},
	)
}

func proxyMoodyBluesStream(
	w http.ResponseWriter,
	incoming *http.Request,
	client *http.Client,
	researchURL string,
	researchToken string,
	path string,
	input any,
) {
	if client == nil || strings.TrimSpace(researchURL) == "" ||
		strings.TrimSpace(researchToken) == "" {
		writeMoodyBluesStreamError(
			w, http.StatusServiceUnavailable, "moody_blues_unavailable",
		)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		writeMoodyBluesStreamError(
			w, http.StatusBadRequest, "moody_blues_request_invalid",
		)
		return
	}
	request, err := http.NewRequestWithContext(
		incoming.Context(), http.MethodPost,
		strings.TrimRight(researchURL, "/")+path,
		bytes.NewReader(body),
	)
	if err != nil {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, "moody_blues_unavailable",
		)
		return
	}
	request.Header.Set("Authorization", "Bearer "+researchToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, "moody_blues_unavailable",
		)
		return
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(
		response.Body, maxMoodyBluesStreamResponseBytes+1,
	))
	if err != nil || len(raw) == 0 ||
		len(raw) > maxMoodyBluesStreamResponseBytes ||
		!json.Valid(raw) {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, "moody_blues_response_invalid",
		)
		return
	}
	if response.StatusCode == http.StatusConflict {
		writeMoodyBluesStreamError(
			w, http.StatusConflict, "moody_blues_generation_conflict",
		)
		return
	}
	if response.StatusCode/100 != 2 {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, "moody_blues_unavailable",
		)
		return
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil ||
		envelope["payload"] != nil ||
		envelope["raw"] != nil {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, "moody_blues_response_invalid",
		)
		return
	}
	if observation := envelope["observation"]; observation != nil &&
		string(observation) != "null" {
		var value map[string]json.RawMessage
		if json.Unmarshal(observation, &value) != nil ||
			value["payload"] != nil {
			writeMoodyBluesStreamError(
				w, http.StatusBadGateway, "moody_blues_response_invalid",
			)
			return
		}
		delete(value, "raw")
		envelope["observation"], err = json.Marshal(value)
		if err != nil {
			writeMoodyBluesStreamError(
				w, http.StatusBadGateway, "moody_blues_response_invalid",
			)
			return
		}
		raw, err = json.Marshal(envelope)
		if err != nil {
			writeMoodyBluesStreamError(
				w, http.StatusBadGateway, "moody_blues_response_invalid",
			)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func decodeMoodyBluesStreamJSON(
	w http.ResponseWriter,
	r *http.Request,
	limit int64,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF {
		writeMoodyBluesStreamError(
			w, http.StatusBadRequest, "moody_blues_request_invalid",
		)
		return false
	}
	return true
}

func validMoodyBluesCategory(value string) bool {
	return value == "gex_full" ||
		value == "gex_zero" ||
		value == "gex_one"
}

func validMoodyBluesReplayID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func writeMoodyBluesStreamError(
	w http.ResponseWriter, status int, code string,
) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      http.StatusText(status),
		"error_code": code,
	})
}
