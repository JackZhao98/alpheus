package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AgentQueryJob struct {
	ID             string          `json:"id"`
	Subject        string          `json:"-"`
	Workflow       string          `json:"workflow"`
	Symbol         string          `json:"symbol"`
	Query          string          `json:"-"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorCode      string          `json:"error_code,omitempty"`
	Attempt        int             `json:"attempt"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ClaimToken     string          `json:"-"`
	LeaseExpiresAt time.Time       `json:"-"`
}

const agentQueryJobColumns = `id::text,authenticated_subject,workflow,symbol,query,status,
	result::text,error_code,attempt,claim_token::text,lease_expires_at,created_at,updated_at`

func (s *Store) CreateAgentQueryJob(subject, workflow, symbol, query string) (*AgentQueryJob, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	job := &AgentQueryJob{ID: NewID()}
	row := s.DB.QueryRowContext(ctx, `INSERT INTO agent_query_job
		(id,authenticated_subject,workflow,symbol,query,status)
		VALUES ($1,$2,$3,$4,$5,'queued') RETURNING `+agentQueryJobColumns,
		job.ID, subject, workflow, symbol, query)
	if err := scanAgentQueryJob(row, job); err != nil {
		return nil, normalizeDBError(err)
	}
	return job, nil
}

func (s *Store) ClaimAgentQueryJob(id string, leaseDuration time.Duration) (*AgentQueryJob, error) {
	leaseMillis := leaseDuration.Milliseconds()
	if leaseMillis <= 0 {
		return nil, fmt.Errorf("agent query lease must be positive")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	job := &AgentQueryJob{}
	err := scanAgentQueryJob(s.DB.QueryRowContext(ctx, `UPDATE agent_query_job
		SET status='running',attempt=attempt+1,claim_token=$2,
			lease_expires_at=clock_timestamp()+($3 * interval '1 millisecond'),
			updated_at=clock_timestamp()
		WHERE id=$1 AND (status='queued' OR
			(status='running' AND lease_expires_at <= clock_timestamp()))
		RETURNING `+agentQueryJobColumns, id, NewID(), leaseMillis), job)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return job, nil
}

func (s *Store) CompleteClaimedAgentQueryJob(id, claimToken string, result json.RawMessage) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	updated, err := s.DB.ExecContext(ctx, `UPDATE agent_query_job
		SET status='succeeded',result=$3,error_code=NULL,claim_token=NULL,
			lease_expires_at=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND claim_token=$2`, id, claimToken, string(result))
	return changed(updated, err)
}

func (s *Store) FailClaimedAgentQueryJob(id, claimToken, errorCode string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `UPDATE agent_query_job
		SET status='failed',result=NULL,error_code=$3,claim_token=NULL,
			lease_expires_at=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND claim_token=$2`, id, claimToken, errorCode)
	return changed(result, err)
}

func (s *Store) ListRecoverableAgentQueryJobs(limit int) ([]AgentQueryJob, error) {
	if limit <= 0 {
		return []AgentQueryJob{}, nil
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+agentQueryJobColumns+`
		FROM agent_query_job
		WHERE status='queued' OR (status='running' AND lease_expires_at <= clock_timestamp())
		ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	jobs := make([]AgentQueryJob, 0)
	for rows.Next() {
		var job AgentQueryJob
		if err := scanAgentQueryJob(rows, &job); err != nil {
			return nil, normalizeDBError(err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return jobs, nil
}

func (s *Store) GetAgentQueryJob(id string) (*AgentQueryJob, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	job := &AgentQueryJob{}
	err := scanAgentQueryJob(s.DB.QueryRowContext(ctx,
		`SELECT `+agentQueryJobColumns+` FROM agent_query_job WHERE id=$1`, id), job)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return job, nil
}

type agentQueryJobScanner interface {
	Scan(dest ...any) error
}

func scanAgentQueryJob(row agentQueryJobScanner, job *AgentQueryJob) error {
	var result, errorCode, claimToken sql.NullString
	var leaseExpiresAt sql.NullTime
	if err := row.Scan(&job.ID, &job.Subject, &job.Workflow, &job.Symbol, &job.Query, &job.Status,
		&result, &errorCode, &job.Attempt, &claimToken, &leaseExpiresAt,
		&job.CreatedAt, &job.UpdatedAt); err != nil {
		return err
	}
	if result.Valid {
		job.Result = json.RawMessage(result.String)
	}
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
	}
	if claimToken.Valid {
		job.ClaimToken = claimToken.String
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = leaseExpiresAt.Time
	}
	return nil
}

func changed(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, normalizeDBError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, normalizeDBError(err)
	}
	return count == 1, nil
}
