package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
	"github.com/lib/pq"
)

var (
	ErrAgentIntradaySessionInvalid  = errors.New("agent intraday session is invalid")
	ErrAgentIntradaySessionConflict = errors.New("agent intraday session conflicts with persisted state")
)

var agentIntradayUUIDPattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type AgentIntradaySession struct {
	SessionID           string       `json:"session_id"`
	Subject             string       `json:"-"`
	Environment         string       `json:"environment"`
	RequestID           string       `json:"request_id"`
	ReplayID            string       `json:"replay_id"`
	ProviderID          string       `json:"provider_id"`
	Symbol              string       `json:"symbol"`
	Category            string       `json:"category"`
	StartAvailableAt    time.Time    `json:"start_available_at"`
	EndAvailableAt      time.Time    `json:"end_available_at"`
	AsOf                time.Time    `json:"as_of"`
	State               string       `json:"state"`
	ReplayGeneration    int64        `json:"replay_generation"`
	PaperAccountID      string       `json:"paper_account_id,omitempty"`
	InitialCash         units.Micros `json:"initial_cash"`
	DetectorIDs         []string     `json:"detector_ids"`
	LastSourceTimestamp time.Time    `json:"last_source_timestamp,omitempty"`
	LastAvailableAt     time.Time    `json:"last_available_at,omitempty"`
	LatestWakeRunID     string       `json:"latest_wake_run_id,omitempty"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

type AgentIntradaySessionCreate struct {
	Subject          string
	Environment      string
	RequestID        string
	ReplayID         string
	ProviderID       string
	Symbol           string
	Category         string
	StartAvailableAt time.Time
	EndAvailableAt   time.Time
	AsOf             time.Time
	State            string
	ReplayGeneration int64
	InitialCash      units.Micros
	DetectorIDs      []string
	Payload          json.RawMessage
}

type AgentIntradaySessionFrame struct {
	Subject          string
	ReplayID         string
	State            string
	ReplayGeneration int64
	SourceTimestamp  time.Time
	AvailableAt      time.Time
	LatestWakeRunID  string
	Payload          json.RawMessage
}

type AgentIntradaySessionEvent struct {
	EventID          string          `json:"event_id"`
	SessionID        string          `json:"session_id"`
	Kind             string          `json:"kind"`
	ReplayGeneration int64           `json:"replay_generation"`
	RunID            string          `json:"run_id,omitempty"`
	SourceTimestamp  time.Time       `json:"source_timestamp,omitempty"`
	AvailableAt      time.Time       `json:"available_at,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	OccurredAt       time.Time       `json:"occurred_at"`
}

func (s *Store) CreateAgentIntradaySession(
	input AgentIntradaySessionCreate,
) (AgentIntradaySession, error) {
	normalizeAgentIntradayCreate(&input)
	if !validAgentIntradayCreate(input) {
		return AgentIntradaySession{}, ErrAgentIntradaySessionInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	existing, existingErr := agentIntradaySessionByRequestTx(
		ctx, tx, input.Subject, input.RequestID,
	)
	if existingErr == nil {
		if !agentIntradayCreateMatches(existing, input) {
			return AgentIntradaySession{}, ErrAgentIntradaySessionConflict
		}
		if err := tx.Commit(); err != nil {
			return AgentIntradaySession{}, normalizeDBError(err)
		}
		committed = true
		return existing, nil
	}
	if !errors.Is(existingErr, sql.ErrNoRows) {
		return AgentIntradaySession{}, existingErr
	}
	sessionID := NewID()
	accountID := "playground-" + strings.ReplaceAll(sessionID, "-", "")
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_paper_account (
		account_id,account_type,starting_cash_micros,cash_micros,
		buying_power_micros,generation,created_at,updated_at
	) VALUES ($1,'paper',$2,$2,$2,1,$3,$3)`,
		accountID, int64(input.InitialCash), now,
	); err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	accountPayload, err := json.Marshal(map[string]any{
		"schema_revision":      1,
		"starting_cash_micros": int64(input.InitialCash),
		"reason_code":          "strategy_playground_initialized",
	})
	if err != nil {
		return AgentIntradaySession{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_paper_event (
		event_id,account_id,generation,event_type,payload,occurred_at
	) VALUES ($1::uuid,$2,1,'account_created',$3::jsonb,$4)`,
		NewID(), accountID, accountPayload, now,
	); err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_intraday_session (
		session_id,subject,environment,request_id,replay_id,provider_id,
		symbol,category,start_available_at,end_available_at,as_of,state,
		replay_generation,paper_account_id,initial_cash_micros,detector_ids,
		created_at,updated_at
	) VALUES (
		$1::uuid,$2,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,
		$14,$15,$16,clock_timestamp(),clock_timestamp()
	)`,
		sessionID, input.Subject, input.Environment, input.RequestID,
		input.ReplayID, input.ProviderID, input.Symbol, input.Category,
		input.StartAvailableAt, input.EndAvailableAt, input.AsOf,
		input.State, input.ReplayGeneration, accountID,
		int64(input.InitialCash), pq.Array(input.DetectorIDs),
	)
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	session, err := agentIntradaySessionByRequestTx(
		ctx, tx, input.Subject, input.RequestID,
	)
	if err != nil {
		return AgentIntradaySession{}, err
	}
	if !agentIntradayCreateMatches(session, input) {
		return AgentIntradaySession{}, ErrAgentIntradaySessionConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_intraday_session_event (
		event_id,session_id,kind,replay_generation,payload,occurred_at
	) VALUES ($1::uuid,$2::uuid,'created',$3,$4::jsonb,clock_timestamp())
	ON CONFLICT (session_id,kind,replay_generation) DO NOTHING`,
		NewID(), session.SessionID, input.ReplayGeneration,
		[]byte(input.Payload),
	); err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	committed = true
	return session, nil
}

func (s *Store) RecordAgentIntradaySessionFrame(
	input AgentIntradaySessionFrame,
) (AgentIntradaySession, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.ReplayID = strings.ToLower(strings.TrimSpace(input.ReplayID))
	input.State = strings.TrimSpace(input.State)
	input.LatestWakeRunID = strings.ToLower(
		strings.TrimSpace(input.LatestWakeRunID),
	)
	if !validAgentIntradayFrame(input) {
		return AgentIntradaySession{}, ErrAgentIntradaySessionInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	session, err := agentIntradaySessionByReplayTx(
		ctx, tx, input.Subject, input.ReplayID, true,
	)
	if err != nil {
		return AgentIntradaySession{}, err
	}
	if input.ReplayGeneration < session.ReplayGeneration {
		return AgentIntradaySession{}, ErrAgentIntradaySessionConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO agent_intraday_session_event (
		event_id,session_id,kind,replay_generation,run_id,
		source_timestamp,available_at,payload,occurred_at
	) VALUES (
		$1::uuid,$2::uuid,'frame',$3,NULLIF($4,'')::uuid,
		NULLIF($5::timestamptz,'0001-01-01T00:00:00Z'::timestamptz),
		NULLIF($6::timestamptz,'0001-01-01T00:00:00Z'::timestamptz),
		$7::jsonb,clock_timestamp()
	) ON CONFLICT (session_id,kind,replay_generation) DO NOTHING`,
		NewID(), session.SessionID, input.ReplayGeneration,
		input.LatestWakeRunID, input.SourceTimestamp, input.AvailableAt,
		[]byte(input.Payload),
	)
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows == 0 {
		var matches bool
		err = tx.QueryRowContext(ctx, `SELECT payload=$3::jsonb
			FROM agent_intraday_session_event
			WHERE session_id=$1::uuid AND kind='frame'
			  AND replay_generation=$2`,
			session.SessionID, input.ReplayGeneration, []byte(input.Payload),
		).Scan(&matches)
		if err != nil || !matches {
			return AgentIntradaySession{}, ErrAgentIntradaySessionConflict
		}
	}
	err = tx.QueryRowContext(ctx, `UPDATE agent_intraday_session SET
		state=$3,replay_generation=$4,
		last_source_timestamp=COALESCE(
			NULLIF($5::timestamptz,'0001-01-01T00:00:00Z'::timestamptz),
			last_source_timestamp
		),
		last_available_at=COALESCE(
			NULLIF($6::timestamptz,'0001-01-01T00:00:00Z'::timestamptz),
			last_available_at
		),
		latest_wake_run_id=COALESCE(NULLIF($7,'')::uuid,latest_wake_run_id),
		updated_at=clock_timestamp()
		WHERE subject=$1 AND replay_id=$2::uuid
		  AND replay_generation<=$4
		RETURNING session_id::text,subject,environment,request_id,
		  replay_id::text,provider_id,symbol,category,start_available_at,
		  end_available_at,as_of,state,replay_generation,
		  COALESCE(paper_account_id,''),initial_cash_micros,detector_ids,
		  COALESCE(last_source_timestamp,'0001-01-01T00:00:00Z'),
		  COALESCE(last_available_at,'0001-01-01T00:00:00Z'),
		  COALESCE(latest_wake_run_id::text,''),created_at,updated_at`,
		input.Subject, input.ReplayID, input.State,
		input.ReplayGeneration, input.SourceTimestamp, input.AvailableAt,
		input.LatestWakeRunID,
	).Scan(agentIntradaySessionScanTargets(&session)...)
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	committed = true
	return session, nil
}

func (s *Store) AgentIntradaySessionByReplay(
	subject string,
	replayID string,
) (AgentIntradaySession, error) {
	subject = strings.TrimSpace(subject)
	replayID = strings.ToLower(strings.TrimSpace(replayID))
	if subject == "" || len(subject) > 200 ||
		!agentIntradayUUIDPattern.MatchString(replayID) {
		return AgentIntradaySession{}, ErrAgentIntradaySessionInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	return agentIntradaySessionByReplayTx(
		ctx, s.DB, subject, replayID, false,
	)
}

func (s *Store) ListAgentIntradaySessions(
	subject string,
	limit int,
) ([]AgentIntradaySession, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" || len(subject) > 200 || limit < 1 || limit > 20 {
		return nil, ErrAgentIntradaySessionInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT
		session_id::text,subject,environment,request_id,replay_id::text,
		provider_id,symbol,category,start_available_at,end_available_at,
		as_of,state,replay_generation,
		COALESCE(paper_account_id,''),initial_cash_micros,detector_ids,
		COALESCE(last_source_timestamp,'0001-01-01T00:00:00Z'),
		COALESCE(last_available_at,'0001-01-01T00:00:00Z'),
		COALESCE(latest_wake_run_id::text,''),created_at,updated_at
		FROM agent_intraday_session
		WHERE subject=$1 ORDER BY created_at DESC LIMIT $2`,
		subject, limit,
	)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	items := make([]AgentIntradaySession, 0)
	for rows.Next() {
		var item AgentIntradaySession
		if err := rows.Scan(agentIntradaySessionScanTargets(&item)...); err != nil {
			return nil, normalizeDBError(err)
		}
		items = append(items, item)
	}
	return items, normalizeDBError(rows.Err())
}

func (s *Store) ListAgentIntradaySessionEvents(
	subject string,
	sessionID string,
	limit int,
) ([]AgentIntradaySessionEvent, error) {
	subject = strings.TrimSpace(subject)
	sessionID = strings.ToLower(strings.TrimSpace(sessionID))
	if subject == "" || len(subject) > 200 ||
		!agentIntradayUUIDPattern.MatchString(sessionID) ||
		limit < 1 || limit > 200 {
		return nil, ErrAgentIntradaySessionInvalid
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT
		event.event_id::text,event.session_id::text,event.kind,
		event.replay_generation,COALESCE(event.run_id::text,''),
		COALESCE(event.source_timestamp,'0001-01-01T00:00:00Z'),
		COALESCE(event.available_at,'0001-01-01T00:00:00Z'),
		event.payload::text,event.occurred_at
		FROM agent_intraday_session_event AS event
		JOIN agent_intraday_session AS session
		  ON session.session_id=event.session_id
		WHERE session.subject=$1 AND event.session_id=$2::uuid
		ORDER BY event.replay_generation DESC,event.occurred_at DESC
		LIMIT $3`,
		subject, sessionID, limit,
	)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	items := make([]AgentIntradaySessionEvent, 0)
	for rows.Next() {
		var item AgentIntradaySessionEvent
		var payload []byte
		if err := rows.Scan(
			&item.EventID, &item.SessionID, &item.Kind,
			&item.ReplayGeneration, &item.RunID,
			&item.SourceTimestamp, &item.AvailableAt,
			&payload, &item.OccurredAt,
		); err != nil {
			return nil, normalizeDBError(err)
		}
		item.Payload = append(json.RawMessage(nil), payload...)
		items = append(items, item)
	}
	return items, normalizeDBError(rows.Err())
}

func agentIntradaySessionByRequestTx(
	ctx context.Context,
	tx *sql.Tx,
	subject string,
	requestID string,
) (AgentIntradaySession, error) {
	var session AgentIntradaySession
	err := tx.QueryRowContext(ctx, `SELECT
		session_id::text,subject,environment,request_id,replay_id::text,
		provider_id,symbol,category,start_available_at,end_available_at,
		as_of,state,replay_generation,
		COALESCE(paper_account_id,''),initial_cash_micros,detector_ids,
		COALESCE(last_source_timestamp,'0001-01-01T00:00:00Z'),
		COALESCE(last_available_at,'0001-01-01T00:00:00Z'),
		COALESCE(latest_wake_run_id::text,''),created_at,updated_at
		FROM agent_intraday_session
		WHERE subject=$1 AND request_id=$2 FOR UPDATE`,
		subject, requestID,
	).Scan(agentIntradaySessionScanTargets(&session)...)
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	return session, nil
}

type agentIntradayRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func agentIntradaySessionByReplayTx(
	ctx context.Context,
	queryer agentIntradayRowQueryer,
	subject string,
	replayID string,
	forUpdate bool,
) (AgentIntradaySession, error) {
	query := `SELECT
		session_id::text,subject,environment,request_id,replay_id::text,
		provider_id,symbol,category,start_available_at,end_available_at,
		as_of,state,replay_generation,
		COALESCE(paper_account_id,''),initial_cash_micros,detector_ids,
		COALESCE(last_source_timestamp,'0001-01-01T00:00:00Z'),
		COALESCE(last_available_at,'0001-01-01T00:00:00Z'),
		COALESCE(latest_wake_run_id::text,''),created_at,updated_at
		FROM agent_intraday_session
		WHERE subject=$1 AND replay_id=$2::uuid`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var session AgentIntradaySession
	err := queryer.QueryRowContext(
		ctx, query, subject, replayID,
	).Scan(agentIntradaySessionScanTargets(&session)...)
	if err != nil {
		return AgentIntradaySession{}, normalizeDBError(err)
	}
	return session, nil
}

func agentIntradaySessionScanTargets(
	session *AgentIntradaySession,
) []any {
	return []any{
		&session.SessionID, &session.Subject, &session.Environment,
		&session.RequestID, &session.ReplayID, &session.ProviderID,
		&session.Symbol, &session.Category, &session.StartAvailableAt,
		&session.EndAvailableAt, &session.AsOf, &session.State,
		&session.ReplayGeneration, &session.PaperAccountID,
		&session.InitialCash, pq.Array(&session.DetectorIDs),
		&session.LastSourceTimestamp,
		&session.LastAvailableAt, &session.LatestWakeRunID,
		&session.CreatedAt, &session.UpdatedAt,
	}
}

func normalizeAgentIntradayCreate(input *AgentIntradaySessionCreate) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Environment = strings.TrimSpace(input.Environment)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ReplayID = strings.ToLower(strings.TrimSpace(input.ReplayID))
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Category = strings.TrimSpace(input.Category)
	input.State = strings.TrimSpace(input.State)
	input.DetectorIDs = normalizeAgentDetectorIDs(input.DetectorIDs)
}

func validAgentIntradayCreate(input AgentIntradaySessionCreate) bool {
	return input.Subject != "" && len(input.Subject) <= 200 &&
		input.Environment == "paper" &&
		input.RequestID != "" && len(input.RequestID) <= 200 &&
		agentIntradayUUIDPattern.MatchString(input.ReplayID) &&
		input.ProviderID == "gexbot-classic" &&
		input.Symbol == "SPX" &&
		validAgentIntradayCategory(input.Category) &&
		validAgentIntradayState(input.State) &&
		input.ReplayGeneration > 0 &&
		input.InitialCash >= units.MustMicros("1000") &&
		input.InitialCash <= units.MustMicros("10000000") &&
		len(input.DetectorIDs) <= 32 &&
		validAgentDetectorIDs(input.DetectorIDs) &&
		!input.StartAvailableAt.IsZero() &&
		!input.EndAvailableAt.Before(input.StartAvailableAt) &&
		!input.AsOf.Before(input.EndAvailableAt) &&
		validAgentIntradayPayload(input.Payload)
}

func validAgentIntradayFrame(input AgentIntradaySessionFrame) bool {
	return input.Subject != "" && len(input.Subject) <= 200 &&
		agentIntradayUUIDPattern.MatchString(input.ReplayID) &&
		validAgentIntradayState(input.State) &&
		input.ReplayGeneration > 0 &&
		(input.LatestWakeRunID == "" ||
			agentIntradayUUIDPattern.MatchString(input.LatestWakeRunID)) &&
		validAgentIntradayPayload(input.Payload)
}

func validAgentIntradayCategory(value string) bool {
	return value == "gex_full" || value == "gex_zero" ||
		value == "gex_one"
}

func validAgentIntradayState(value string) bool {
	return value == "active" || value == "complete" || value == "failed"
}

func validAgentIntradayPayload(value json.RawMessage) bool {
	if len(value) == 0 || len(value) > 65536 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func agentIntradayCreateMatches(
	session AgentIntradaySession,
	input AgentIntradaySessionCreate,
) bool {
	return session.Subject == input.Subject &&
		session.Environment == input.Environment &&
		session.RequestID == input.RequestID &&
		session.ReplayID == input.ReplayID &&
		session.ProviderID == input.ProviderID &&
		session.Symbol == input.Symbol &&
		session.Category == input.Category &&
		session.StartAvailableAt.Equal(input.StartAvailableAt) &&
		session.EndAvailableAt.Equal(input.EndAvailableAt) &&
		session.AsOf.Equal(input.AsOf) &&
		session.InitialCash == input.InitialCash &&
		equalAgentDetectorIDs(session.DetectorIDs, input.DetectorIDs)
}

func normalizeAgentDetectorIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validAgentDetectorIDs(values []string) bool {
	for _, value := range values {
		if len(value) > 200 ||
			(!agentIntradayUUIDPattern.MatchString(value) &&
				!regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`).MatchString(value)) {
			return false
		}
	}
	return true
}

func equalAgentDetectorIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
