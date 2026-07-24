package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type kernelPaperMode struct {
	SchemaRevision uint16    `json:"schema_revision"`
	Environment    string    `json:"environment"`
	Mode           string    `json:"mode"`
	Generation     int64     `json:"generation"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type paperCandidateExecutionResult struct {
	Status        string                                 `json:"status"`
	ReasonCode    string                                 `json:"reason_code,omitempty"`
	KernelMode    string                                 `json:"kernel_mode"`
	Authorization *inputgateway.PaperEffectAuthorization `json:"authorization,omitempty"`
	Receipt       *inputgateway.PaperEffectReceipt       `json:"receipt,omitempty"`
}

func executePaperCandidate(
	ctx context.Context,
	adapter *inputgateway.PostgresAdapter,
	client *http.Client,
	kernelURL string,
	paperEffectToken string,
	subjectID string,
	candidateID string,
	authorizationKind string,
) (paperCandidateExecutionResult, error) {
	mode, err := readKernelPaperMode(
		ctx, client, kernelURL, paperEffectToken,
	)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	if mode.Mode != authorizationKind {
		return paperCandidateExecutionResult{
			Status:     "skipped",
			ReasonCode: "paper_mode_not_active",
			KernelMode: mode.Mode,
		}, nil
	}
	authorization, err := adapter.AuthorizePaperEffect(
		ctx, subjectID, candidateID, authorizationKind, mode.Generation,
	)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	command := map[string]any{
		"schema_revision":        1,
		"authorization_id":       authorization.AuthorizationID,
		"authorization_kind":     authorization.AuthorizationKind,
		"authorization_digest":   authorization.RecordDigest,
		"kernel_mode_generation": authorization.KernelModeGeneration,
		"candidate_id":           authorization.CandidateID,
		"effect_id":              authorization.EffectID,
		"run_id":                 authorization.RunID,
		"task_id":                authorization.TaskID,
		"symbol":                 authorization.Proposal.Symbol,
		"kind":                   authorization.Proposal.Kind,
		"side":                   authorization.Proposal.Side,
		"multiplier":             1,
		"qty":                    authorization.Proposal.Qty,
	}
	requestRaw, err := json.Marshal(command)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		kernelURL+"/internal/v1/cortex-effects/paper-order",
		bytes.NewReader(requestRaw),
	)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+paperEffectToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	defer response.Body.Close()
	responseRaw, readErr := io.ReadAll(io.LimitReader(
		response.Body, (64<<10)+1,
	))
	if readErr != nil || len(responseRaw) > 64<<10 {
		return paperCandidateExecutionResult{},
			fmt.Errorf("Kernel Paper response unavailable")
	}
	outcome := "failed"
	failureCode := kernelPaperFailureCode(response.StatusCode)
	receiptStatus := response.StatusCode
	kernelResponse := json.RawMessage(nil)
	if validKernelPaperObject(responseRaw) {
		kernelResponse = responseRaw
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		if kernelResponse == nil {
			receiptStatus = http.StatusBadGateway
			failureCode = "kernel_response_invalid"
		} else {
			outcome = "succeeded"
			failureCode = ""
		}
	}
	receipt, err := adapter.RecordPaperEffectReceipt(
		ctx, authorization.AuthorizationID, outcome, receiptStatus,
		kernelResponse, failureCode,
	)
	if err != nil {
		return paperCandidateExecutionResult{}, err
	}
	return paperCandidateExecutionResult{
		Status:        "executed",
		KernelMode:    mode.Mode,
		Authorization: &authorization,
		Receipt:       &receipt,
	}, nil
}

func readKernelPaperMode(
	ctx context.Context,
	client *http.Client,
	kernelURL string,
	paperEffectToken string,
) (kernelPaperMode, error) {
	if client == nil || kernelURL == "" || paperEffectToken == "" {
		return kernelPaperMode{}, fmt.Errorf("Kernel Paper mode unavailable")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		kernelURL+"/internal/v1/cortex-effects/paper-mode", nil,
	)
	if err != nil {
		return kernelPaperMode{}, err
	}
	request.Header.Set("Authorization", "Bearer "+paperEffectToken)
	response, err := client.Do(request)
	if err != nil {
		return kernelPaperMode{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return kernelPaperMode{},
			fmt.Errorf("Kernel Paper mode status %d", response.StatusCode)
	}
	var mode kernelPaperMode
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&mode) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		mode.SchemaRevision != 1 || mode.Environment != "paper" ||
		(mode.Mode != "observe" && mode.Mode != "copilot" &&
			mode.Mode != "agentic") ||
		mode.Generation < 1 || mode.UpdatedAt.IsZero() ||
		mode.UpdatedAt.Location() != time.UTC {
		return kernelPaperMode{}, fmt.Errorf("invalid Kernel Paper mode")
	}
	return mode, nil
}

func startPaperCandidateExecutionRecovery(
	ctx context.Context,
	adapter *inputgateway.PostgresAdapter,
	client *http.Client,
	kernelURL string,
	paperEffectToken string,
	subjectID string,
) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				recoverPaperCandidateExecutions(
					ctx, adapter, client, kernelURL, paperEffectToken,
					subjectID,
				)
			}
		}
	}()
}

func recoverPaperCandidateExecutions(
	ctx context.Context,
	adapter *inputgateway.PostgresAdapter,
	client *http.Client,
	kernelURL string,
	paperEffectToken string,
	subjectID string,
) {
	recoveryContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	mode, err := readKernelPaperMode(
		recoveryContext, client, kernelURL, paperEffectToken,
	)
	if err != nil || mode.Mode == "observe" {
		return
	}
	candidates, err := adapter.ListPaperCandidates(
		recoveryContext, subjectID, 100,
	)
	if err != nil {
		log.Printf("Cortex Paper execution recovery list failed: %v", err)
		return
	}
	for _, candidate := range candidates {
		if !candidate.Eligible ||
			candidate.Execution != nil &&
				candidate.Execution.Outcome != "" ||
			mode.Mode == "agentic" && candidate.Status != "proposed" ||
			mode.Mode == "copilot" && candidate.Status != "approved" {
			continue
		}
		if _, err := executePaperCandidate(
			recoveryContext, adapter, client, kernelURL,
			paperEffectToken, subjectID, candidate.CandidateID,
			mode.Mode,
		); err != nil && !strings.Contains(
			err.Error(), "not authorizable",
		) {
			log.Printf(
				"Cortex Paper execution recovery failed for %s: %v",
				candidate.CandidateID, err,
			)
		}
	}
}

func validKernelPaperObject(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return false
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func kernelPaperFailureCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "kernel_request_rejected"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "kernel_authorization_rejected"
	case http.StatusConflict:
		return "kernel_mode_conflict"
	case http.StatusUnprocessableEntity:
		return "kernel_settlement_rejected"
	case http.StatusBadGateway:
		return "kernel_market_data_unavailable"
	case http.StatusServiceUnavailable:
		return "kernel_unavailable"
	default:
		return "kernel_effect_failed"
	}
}
