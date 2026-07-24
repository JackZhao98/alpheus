package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	ErrAgentAutonomyInvalid            = errors.New("agent autonomy profile is invalid")
	ErrAgentAutonomyGenerationConflict = errors.New("agent autonomy generation conflict")
)

type AgentAutonomyProfile struct {
	Environment string    `json:"environment"`
	Mode        string    `json:"mode"`
	Generation  int64     `json:"generation"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) AgentAutonomyProfile(
	environment string,
) (AgentAutonomyProfile, error) {
	if !validAgentEnvironment(environment) {
		return AgentAutonomyProfile{}, ErrAgentAutonomyInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	var profile AgentAutonomyProfile
	err := s.DB.QueryRowContext(ctx, `SELECT
		environment,mode,generation,updated_by,created_at,updated_at
		FROM agent_autonomy_profile WHERE environment=$1`,
		environment,
	).Scan(
		&profile.Environment, &profile.Mode, &profile.Generation,
		&profile.UpdatedBy, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	return profile, nil
}

func (s *Store) SetAgentAutonomy(
	environment string,
	mode string,
	expectedGeneration int64,
	updatedBy string,
) (AgentAutonomyProfile, error) {
	updatedBy = strings.TrimSpace(updatedBy)
	if !validAgentEnvironment(environment) ||
		!validAgentAutonomyMode(mode) ||
		expectedGeneration < 1 ||
		updatedBy == "" || len(updatedBy) > 200 {
		return AgentAutonomyProfile{}, ErrAgentAutonomyInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var profile AgentAutonomyProfile
	err = tx.QueryRowContext(ctx, `SELECT
		environment,mode,generation,updated_by,created_at,updated_at
		FROM agent_autonomy_profile WHERE environment=$1 FOR UPDATE`,
		environment,
	).Scan(
		&profile.Environment, &profile.Mode, &profile.Generation,
		&profile.UpdatedBy, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	if profile.Generation != expectedGeneration {
		return AgentAutonomyProfile{}, ErrAgentAutonomyGenerationConflict
	}
	if profile.Mode == mode {
		if err := tx.Commit(); err != nil {
			return AgentAutonomyProfile{}, normalizeDBError(err)
		}
		committed = true
		return profile, nil
	}
	previousMode := profile.Mode
	profile.Mode = mode
	profile.Generation++
	profile.UpdatedBy = updatedBy
	if err := tx.QueryRowContext(ctx, `UPDATE agent_autonomy_profile SET
		mode=$2,generation=$3,updated_by=$4,updated_at=clock_timestamp()
		WHERE environment=$1
		RETURNING updated_at`,
		profile.Environment, profile.Mode, profile.Generation,
		profile.UpdatedBy,
	).Scan(&profile.UpdatedAt); err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_autonomy_event (
		event_id,environment,generation,from_mode,to_mode,updated_by,occurred_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		NewID(), profile.Environment, profile.Generation, previousMode,
		profile.Mode, profile.UpdatedBy, profile.UpdatedAt,
	); err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return AgentAutonomyProfile{}, normalizeDBError(err)
	}
	committed = true
	return profile, nil
}

func validAgentEnvironment(environment string) bool {
	return environment == "paper" || environment == "live"
}

func validAgentAutonomyMode(mode string) bool {
	return mode == "observe" || mode == "copilot" || mode == "agentic"
}
