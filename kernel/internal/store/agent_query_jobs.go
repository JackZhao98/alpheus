package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type AgentQueryJob struct {
	ID        string          `json:"id"`
	Subject   string          `json:"-"`
	Workflow  string          `json:"workflow"`
	Symbol    string          `json:"symbol"`
	Query     string          `json:"-"`
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const agentQueryJobColumns = `id::text,authenticated_subject,workflow,symbol,query,status,
	result::text,error_code,created_at,updated_at`

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

func (s *Store) StartAgentQueryJob(id string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `UPDATE agent_query_job
		SET status='running',updated_at=clock_timestamp()
		WHERE id=$1 AND status='queued'`, id)
	return changed(result, err)
}

func (s *Store) CompleteAgentQueryJob(id string, result json.RawMessage) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	updated, err := s.DB.ExecContext(ctx, `UPDATE agent_query_job
		SET status='succeeded',result=$2,error_code=NULL,updated_at=clock_timestamp()
		WHERE id=$1 AND status='running'`, id, string(result))
	return changed(updated, err)
}

func (s *Store) FailAgentQueryJob(id, errorCode string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `UPDATE agent_query_job
		SET status='failed',result=NULL,error_code=$2,updated_at=clock_timestamp()
		WHERE id=$1 AND status IN ('queued','running')`, id, errorCode)
	return changed(result, err)
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
	var result, errorCode sql.NullString
	if err := row.Scan(&job.ID, &job.Subject, &job.Workflow, &job.Symbol, &job.Query, &job.Status,
		&result, &errorCode, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return err
	}
	if result.Valid {
		job.Result = json.RawMessage(result.String)
	}
	if errorCode.Valid {
		job.ErrorCode = errorCode.String
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
