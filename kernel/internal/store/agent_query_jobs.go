package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AgentQueryJob struct {
	ID             string                 `json:"id"`
	Subject        string                 `json:"-"`
	Workflow       string                 `json:"workflow"`
	Symbol         string                 `json:"symbol"`
	Query          string                 `json:"-"`
	Status         string                 `json:"status"`
	Result         json.RawMessage        `json:"result,omitempty"`
	ErrorCode      string                 `json:"error_code,omitempty"`
	Attempt        int                    `json:"attempt"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	ClaimToken     string                 `json:"-"`
	LeaseExpiresAt time.Time              `json:"-"`
	Trace          []AgentQueryTraceEvent `json:"trace"`
}

// AgentQueryTraceEvent is a durable, secret-free dispatcher event for Agent
// Lab debugging. It intentionally contains no input prompt or runtime body.
type AgentQueryTraceEvent struct {
	Sequence  int64     `json:"sequence"`
	Attempt   int       `json:"attempt"`
	Stage     string    `json:"stage"`
	ErrorCode string    `json:"error_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const agentQueryJobColumns = `id::text,authenticated_subject,workflow,symbol,query,status,
	result::text,error_code,attempt,claim_token::text,lease_expires_at,created_at,updated_at`

func (s *Store) CreateAgentQueryJob(subject, workflow, symbol, query string) (*AgentQueryJob, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	job := &AgentQueryJob{ID: NewID()}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `INSERT INTO agent_query_job
		(id,authenticated_subject,workflow,symbol,query,status)
		VALUES ($1,$2,$3,$4,$5,'queued') RETURNING `+agentQueryJobColumns,
		job.ID, subject, workflow, symbol, query)
	if err := scanAgentQueryJob(row, job); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := insertAgentQueryTrace(ctx, tx, job.ID, 0, "submitted", ""); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := loadAgentQueryTrace(ctx, tx, job); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	job := &AgentQueryJob{}
	err = scanAgentQueryJob(tx.QueryRowContext(ctx, `UPDATE agent_query_job
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
	if err := insertAgentQueryTrace(ctx, tx, job.ID, job.Attempt, "claimed", ""); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := loadAgentQueryTrace(ctx, tx, job); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return job, nil
}

func (s *Store) CompleteClaimedAgentQueryJob(id, claimToken string, result json.RawMessage) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	var attempt int
	err = tx.QueryRowContext(ctx, `UPDATE agent_query_job
		SET status='succeeded',result=$3,error_code=NULL,claim_token=NULL,
			lease_expires_at=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND claim_token=$2 RETURNING attempt`, id, claimToken, string(result)).Scan(&attempt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	if err := insertAgentQueryTrace(ctx, tx, id, attempt, "completed", ""); err != nil {
		return false, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

func (s *Store) FailClaimedAgentQueryJob(id, claimToken, errorCode string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	var attempt int
	err = tx.QueryRowContext(ctx, `UPDATE agent_query_job
		SET status='failed',result=NULL,error_code=$3,claim_token=NULL,
			lease_expires_at=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running' AND claim_token=$2 RETURNING attempt`, id, claimToken, errorCode).Scan(&attempt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	if err := insertAgentQueryTrace(ctx, tx, id, attempt, "failed", errorCode); err != nil {
		return false, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

// RecordAgentQueryJobTrace appends a lifecycle event only while the current
// worker still owns this job. This prevents a timed-out worker from writing a
// misleading trace after another worker reclaimed the lease.
func (s *Store) RecordAgentQueryJobTrace(id, claimToken, stage, errorCode string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var sequence int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO agent_query_job_trace (job_id,attempt,stage,error_code)
		SELECT id,attempt,$3,NULLIF($4,'') FROM agent_query_job
		WHERE id=$1 AND status='running' AND claim_token=$2
		RETURNING sequence`, id, claimToken, stage, errorCode).Scan(&sequence)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
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
	if err := loadAgentQueryTrace(ctx, s.DB, job); err != nil {
		return nil, normalizeDBError(err)
	}
	return job, nil
}

type agentQueryTraceQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func insertAgentQueryTrace(ctx context.Context, tx *sql.Tx, jobID string, attempt int, stage, errorCode string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_query_job_trace (job_id,attempt,stage,error_code)
		VALUES ($1,$2,$3,NULLIF($4,''))`, jobID, attempt, stage, errorCode)
	return err
}

func loadAgentQueryTrace(ctx context.Context, db agentQueryTraceQueryer, job *AgentQueryJob) error {
	rows, err := db.QueryContext(ctx, `SELECT sequence,attempt,stage,error_code,created_at
		FROM agent_query_job_trace WHERE job_id=$1 ORDER BY sequence`, job.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	job.Trace = make([]AgentQueryTraceEvent, 0)
	for rows.Next() {
		var event AgentQueryTraceEvent
		var errorCode sql.NullString
		if err := rows.Scan(&event.Sequence, &event.Attempt, &event.Stage, &errorCode, &event.CreatedAt); err != nil {
			return err
		}
		if errorCode.Valid {
			event.ErrorCode = errorCode.String
		}
		job.Trace = append(job.Trace, event)
	}
	return rows.Err()
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
