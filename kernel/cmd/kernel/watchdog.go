package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/watchdog"

	"github.com/robfig/cron/v3"
)

func startWatchdog(s *server) (*cron.Cron, error) {
	tz := config.Env("TZ_MARKET", "America/New_York")
	return watchdog.Start(tz, func(role, occurrenceID string) {
		s.fireSpineTick(role, occurrenceID)
	})
}

func (s *server) fireSpineTick(role, occurrenceID string) {
	payload := map[string]string{"role": role, "occurrence_id": occurrenceID}
	s.store.Event("spine_tick", payload)
	if err := s.postRuntimeWake(role, occurrenceID); err != nil {
		log.Printf("spine wake role=%s occurrence_id=%s: %v", role, occurrenceID, err)
		s.store.Event("spine_wake_failed", map[string]string{
			"role": role, "occurrence_id": occurrenceID, "error": err.Error(),
		})
		// TODO(M6 repair): compare expected slots with runtime heartbeats and
		// retry a missed occurrence using this same occurrence_id.
	}
}

func (s *server) postRuntimeWake(role, occurrenceID string) error {
	body, err := json.Marshal(map[string]string{
		"role": role, "trigger": "spine", "occurrence_id": occurrenceID,
	})
	if err != nil {
		return err
	}
	runtimeURL := strings.TrimRight(s.runtimeURL, "/")
	if runtimeURL == "" {
		runtimeURL = "http://agent-runtime:8200"
	}
	req, err := http.NewRequest(http.MethodPost, runtimeURL+"/wake", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.mode.KernelToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.mode.KernelToken)
	}
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("runtime wake returned HTTP %d", resp.StatusCode)
	}
	return nil
}
