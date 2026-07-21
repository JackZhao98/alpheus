package store

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentQueryJobLeaseRecoveryAndFencingPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	if _, err := s.DB.Exec(`DELETE FROM agent_query_job`); err != nil {
		t.Fatal(err)
	}
	job, err := s.CreateAgentQueryJob("owner", "scout", "SOFI", "recover me")
	if err != nil {
		t.Fatal(err)
	}

	const workers = 20
	start := make(chan struct{})
	claims := make(chan *AgentQueryJob, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claim, claimErr := s.ClaimAgentQueryJob(job.ID, time.Minute)
			claims <- claim
			errorsCh <- claimErr
		}()
	}
	close(start)
	wait.Wait()
	close(claims)
	close(errorsCh)
	for claimErr := range errorsCh {
		if claimErr != nil {
			t.Fatal(claimErr)
		}
	}
	var first *AgentQueryJob
	for claim := range claims {
		if claim != nil {
			if first != nil {
				t.Fatalf("multiple first claims: %+v and %+v", first, claim)
			}
			first = claim
		}
	}
	if first == nil || first.Attempt != 1 || first.ClaimToken == "" {
		t.Fatalf("first claim=%+v", first)
	}
	if updated, err := s.RecordAgentQueryJobTrace(job.ID, first.ClaimToken, "credential_loaded", ""); err != nil || !updated {
		t.Fatalf("first trace updated=%v err=%v", updated, err)
	}
	if _, err := s.DB.Exec(`UPDATE agent_query_job
		SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	recoverable, err := s.ListRecoverableAgentQueryJobs(10)
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != job.ID {
		t.Fatalf("recoverable=%+v err=%v", recoverable, err)
	}
	second, err := s.ClaimAgentQueryJob(job.ID, time.Minute)
	if err != nil || second == nil || second.Attempt != 2 || second.ClaimToken == first.ClaimToken {
		t.Fatalf("second claim=%+v err=%v", second, err)
	}
	if updated, err := s.CompleteClaimedAgentQueryJob(job.ID, first.ClaimToken, json.RawMessage(`{"stale":true}`)); err != nil || updated {
		t.Fatalf("stale completion updated=%v err=%v", updated, err)
	}
	if updated, err := s.CompleteClaimedAgentQueryJob(job.ID, second.ClaimToken, json.RawMessage(`{"ok":true}`)); err != nil || !updated {
		t.Fatalf("winner completion updated=%v err=%v", updated, err)
	}
	completed, err := s.GetAgentQueryJob(job.ID)
	if err != nil || completed == nil || completed.Status != "succeeded" || completed.Attempt != 2 ||
		completed.ClaimToken != "" || !completed.LeaseExpiresAt.IsZero() {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	stages := make([]string, 0, len(completed.Trace))
	for _, event := range completed.Trace {
		stages = append(stages, event.Stage)
	}
	if got, want := strings.Join(stages, ","), "submitted,claimed,credential_loaded,claimed,completed"; got != want {
		t.Fatalf("trace stages=%q want=%q", got, want)
	}
}
