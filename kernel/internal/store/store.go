// Package store is the persistence layer. The kernel is the only writer;
// agents go through the HTTP API.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"alpheus/kernel/internal/units"

	"github.com/lib/pq"
)

type Config struct {
	URL           string
	MigrationsDir string
	Timeout       time.Duration
	MarketTZ      string
}

type Store struct {
	DB       *sql.DB
	timeout  time.Duration
	marketTZ string
}

var (
	ErrInvalidOperationReference  = errors.New("invalid operation reference")
	ErrBreakerNotActive           = errors.New("breaker is not active for that reason")
	ErrOperationNotPending        = errors.New("operation is not pending review")
	ErrLiveExecutionBusy          = errors.New("live execution busy")
	ErrLiveExecutionSuspended     = errors.New("live execution suspended")
	ErrLiveSendHalted             = errors.New("live send halted")
	ErrReplayWindowExpired        = errors.New("replay window expired")
	ErrInvalidControlWarningQuery = errors.New("invalid control warning query")
	ErrUnavailable                = errors.New("database unavailable")
)

type IdempotencyIdentity struct {
	Subject     string
	Key         string
	RequestHash [sha256.Size]byte
}

// OperationGate is the minimum store surface used while the kernel holds a
// stable per-ledger advisory transaction lock. Market day remains a data
// dimension and never participates in the mutex key. Count, event, and insert
// must use the same implementation passed to WithLedgerLock.
type OperationGate interface {
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error)
	InsertEvent(kind string, payload any) error
	InsertOperation(id, proposer, class, status string, payload, verdict any, identity *IdempotencyIdentity) error
	SetOperationStatus(id, status string, verdict any) error
	FindOperationByIdempotency(subject, key string) (*OperationRow, error)
	LockLedger(shadow bool) error
	LockLedgerSymbol(ledger, symbol string) error
	HeldCloseQuantity(ledger, symbol string) (units.Qty, error)
	InsertTradeGrant(grant TradeGrant) error
	TradeGrantUsage(ledger string, marketDay time.Time, excludeOperationID string) (TradeGrantUsage, error)
	LiveCanaryAuthority(marketDay time.Time) (*LiveCanaryRevision, error)
	InsertCloseReservation(reservation CloseReservation) error
	InsertExecutionAttempt(attempt ExecutionAttempt) error
	InsertOrder(order Order) error
	InsertOpenReservation(reservation OpenReservation) error
	LedgerResources(ledger, excludeOperationID string) (LedgerResources, error)
	InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error
	DatabaseNow() (time.Time, error)
	ShadowAccount() (ShadowAccount, error)
	ShadowPositions() ([]ShadowPosition, error)
	OpenExposureQuantity(ledger, symbol, kind string) (units.Qty, error)
	FirstOpenExposureOperation(ledger, symbol, kind string) (string, error)
	EvaluateDayRisk(input DayRiskInput) (DayRiskStats, error)
	ResumeBreaker(ledger, reason string, marketDay, observedAt time.Time, subject string) (BreakerState, error)
	RequireLiveExecutionIdle(blockWhenHalted bool) error
}

type ledgerTx struct {
	tx       *sql.Tx
	ctx      context.Context
	marketTZ string
}

func (t *ledgerTx) DatabaseNow() (time.Time, error) {
	var now time.Time
	// clock_timestamp(), unlike now(), advances after transaction start. This
	// keeps a proposal that waited on the ledger gate across market midnight
	// from consuming the previous day's entitlement.
	err := t.tx.QueryRowContext(t.ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, normalizeDBError(err)
}

func (t *ledgerTx) LockLedger(shadow bool) error {
	_, err := t.tx.ExecContext(t.ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(shadow))
	return normalizeDBError(err)
}

func (t *ledgerTx) RequireLiveExecutionIdle(blockWhenHalted bool) error {
	halted := false
	if blockWhenHalted {
		if _, err := t.tx.ExecContext(t.ctx, `SELECT pg_advisory_xact_lock($1)`, globalHaltSendLockKey()); err != nil {
			return normalizeDBError(err)
		}
		var err error
		halted, _, _, _, err = loadGlobalHalt(t.ctx, t.tx)
		if err != nil {
			return normalizeDBError(err)
		}
	}
	var activeID, unknownID sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeID, &unknownID); err != nil {
		return normalizeDBError(err)
	}
	if unknownID.Valid {
		return ErrLiveExecutionSuspended
	}
	if activeID.Valid {
		return ErrLiveExecutionBusy
	}
	if halted {
		return ErrLiveSendHalted
	}
	return nil
}

// Open waits for postgres on cold start.
func Open(cfg Config) (*Store, error) {
	if cfg.URL == "" || cfg.MigrationsDir == "" || cfg.Timeout <= 0 || cfg.MarketTZ == "" {
		return nil, fmt.Errorf("store config is incomplete")
	}
	if _, err := time.LoadLocation(cfg.MarketTZ); err != nil {
		return nil, fmt.Errorf("invalid market timezone")
	}
	migrations, err := LoadMigrations(cfg.MigrationsDir)
	if err != nil {
		return nil, err
	}
	connector, err := pq.NewConnector(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("configure postgres: %w", err)
	}
	connector.Dialer(&deadlineDialer{timeout: cfg.Timeout})
	db := sql.OpenDB(connector)
	connected := false
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		err = db.PingContext(ctx)
		cancel()
		if err == nil {
			connected = true
			break
		}
		time.Sleep(time.Second)
	}
	if !connected {
		_ = db.Close()
		return nil, fmt.Errorf("postgres unreachable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	err = Migrate(ctx, db, migrations, cfg.MarketTZ)
	cancel()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return &Store{DB: db, timeout: cfg.Timeout, marketTZ: cfg.MarketTZ}, nil
}

// deadlineDialer adds a hard socket deadline to lib/pq's context deadline.
// lib/pq otherwise gives its cancellation connection a fixed 10-second
// timeout, so a SIGSTOP/docker-pause database can outlive DB_TIMEOUT_MS.
type deadlineDialer struct {
	dialer  net.Dialer
	timeout time.Duration
}

func (d *deadlineDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *deadlineDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 || timeout > d.timeout {
		timeout = d.timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

func (d *deadlineDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	bounded, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	conn, err := d.dialer.DialContext(bounded, network, address)
	if err != nil {
		return nil, err
	}
	return &deadlineConn{Conn: conn, timeout: d.timeout}, nil
}

type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c *deadlineConn) Read(buffer []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Read(buffer)
}

func (c *deadlineConn) Write(buffer []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(buffer)
}

func (s *Store) deadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.timeout)
}

func normalizeDBError(err error) error {
	if err == nil {
		return nil
	}
	var pqErr *pq.Error
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) ||
		(errors.As(err, &pqErr) && (pqErr.Code == "57014" || pqErr.Code.Class() == "08")) ||
		errors.As(err, &netErr) {
		return fmt.Errorf("%w", ErrUnavailable)
	}
	return err
}

// NewID returns a random UUIDv4 without an external dependency.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func jstr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"marshal_error":true}`
	}
	return string(b)
}

func (s *Store) Event(kind string, payload any) {
	_ = s.InsertEvent(kind, payload)
}

func (s *Store) InsertEvent(kind string, payload any) error {
	ctx, cancel := s.deadline()
	defer cancel()
	return normalizeDBError(insertEvent(ctx, s.DB, kind, payload))
}

func (s *Store) InsertEventWithID(kind string, payload any) (int64, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	id, err := insertEventWithID(ctx, s.DB, kind, payload)
	return id, normalizeDBError(err)
}

func (t *ledgerTx) InsertEvent(kind string, payload any) error {
	return normalizeDBError(insertEvent(t.ctx, t.tx, kind, payload))
}

func insertEvent(ctx context.Context, db execer, kind string, payload any) error {
	_, err := db.ExecContext(ctx, `INSERT INTO events (kind, payload) VALUES ($1, $2)`, kind, jstr(payload))
	return err
}

func insertEventAt(ctx context.Context, db execer, kind string, payload any, observedAt time.Time) error {
	if observedAt.IsZero() {
		return fmt.Errorf("event observation time is required")
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO events (ts, kind, payload) VALUES ($1, $2, $3)`,
		observedAt, kind, jstr(payload))
	return err
}

func insertEventWithID(ctx context.Context, db rowQueryer, kind string, payload any) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO events (kind, payload) VALUES ($1, $2) RETURNING id`,
		kind, jstr(payload)).Scan(&id)
	return id, err
}

func insertEventWithIDAt(ctx context.Context, db rowQueryer, kind string, payload any, observedAt time.Time) (int64, error) {
	if observedAt.IsZero() {
		return 0, fmt.Errorf("event observation time is required")
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO events (ts, kind, payload) VALUES ($1, $2, $3) RETURNING id`,
		observedAt, kind, jstr(payload)).Scan(&id)
	return id, err
}

func (s *Store) InsertOperation(id, proposer, class, status string, payload, verdict any, identity *IdempotencyIdentity) error {
	ctx, cancel := s.deadline()
	defer cancel()
	return normalizeDBError(insertOperation(ctx, s.DB, id, proposer, class, status, payload, verdict, identity))
}

func (t *ledgerTx) InsertOperation(id, proposer, class, status string, payload, verdict any, identity *IdempotencyIdentity) error {
	return normalizeDBError(insertOperation(t.ctx, t.tx, id, proposer, class, status, payload, verdict, identity))
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertOperation(ctx context.Context, db execer, id, proposer, class, status string, payload, verdict any, identity *IdempotencyIdentity) error {
	var subject, key any
	var requestHash any
	if identity != nil {
		subject, key, requestHash = identity.Subject, identity.Key, identity.RequestHash[:]
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO operations (
			id, proposer, class, status, payload, verdict,
			authenticated_subject, idempotency_key, request_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, proposer, class, status, jstr(payload), jstr(verdict), subject, key, requestHash)
	return err
}

func (s *Store) SetOperationStatus(id, status string, verdict any) error {
	ctx, cancel := s.deadline()
	defer cancel()
	return normalizeDBError(setOperationStatus(ctx, s.DB, id, status, verdict))
}

func (t *ledgerTx) SetOperationStatus(id, status string, verdict any) error {
	return normalizeDBError(setOperationStatus(t.ctx, t.tx, id, status, verdict))
}

func setOperationStatus(ctx context.Context, db execer, id, status string, verdict any) error {
	if verdict == nil {
		_, err := db.ExecContext(ctx, `UPDATE operations SET status=$1 WHERE id=$2`, status, id)
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE operations SET status=$1, verdict=$2 WHERE id=$3`, status, jstr(verdict), id)
	return err
}

type OperationRow struct {
	ID                   string          `json:"id"`
	TS                   time.Time       `json:"ts"`
	Proposer             string          `json:"proposer"`
	Class                string          `json:"class"`
	Status               string          `json:"status"`
	Payload              json.RawMessage `json:"payload"`
	Verdict              json.RawMessage `json:"verdict"`
	AuthenticatedSubject string          `json:"-"`
	IdempotencyKey       string          `json:"-"`
	RequestHash          []byte          `json:"-"`
}

// OperationCursor is the stable key for descending operation pagination.
// Both fields are required because operation timestamps are not unique.
type OperationCursor struct {
	TS time.Time `json:"ts"`
	ID string    `json:"id"`
}

func (s *Store) GetOperation(id string) (*OperationRow, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	row, err := getOperation(ctx, s.DB, `WHERE id=$1`, id)
	if err == nil && row == nil {
		return nil, sql.ErrNoRows
	}
	return row, err
}

func (s *Store) FindOperationByIdempotency(subject, key string) (*OperationRow, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	return getOperation(ctx, s.DB, `WHERE authenticated_subject=$1 AND idempotency_key=$2`, subject, key)
}

func (t *ledgerTx) FindOperationByIdempotency(subject, key string) (*OperationRow, error) {
	return getOperation(t.ctx, t.tx, `WHERE authenticated_subject=$1 AND idempotency_key=$2`, subject, key)
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getOperation(ctx context.Context, db rowQueryer, where string, args ...any) (*OperationRow, error) {
	var r OperationRow
	var verdict, subject, key sql.NullString
	var requestHash []byte
	err := db.QueryRowContext(ctx,
		`SELECT id, ts, proposer, class, status, payload, COALESCE(verdict::text,''),
			authenticated_subject, idempotency_key, request_hash
		 FROM operations `+where, args...).
		Scan(&r.ID, &r.TS, &r.Proposer, &r.Class, &r.Status, &r.Payload, &verdict, &subject, &key, &requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if verdict.Valid && verdict.String != "" {
		r.Verdict = json.RawMessage(verdict.String)
	}
	if subject.Valid {
		r.AuthenticatedSubject = subject.String
	}
	if key.Valid {
		r.IdempotencyKey = key.String
	}
	r.RequestHash = append([]byte(nil), requestHash...)
	return &r, nil
}

func (s *Store) ListOperations(status string, limit int, cursor *OperationCursor) ([]OperationRow, error) {
	if limit < 1 {
		return nil, fmt.Errorf("operation list limit must be positive")
	}
	query := `SELECT id, ts, proposer, class, status, payload, COALESCE(verdict::text,'')
		FROM operations WHERE ($1 = '' OR status = $1)`
	args := []any{status}
	if cursor != nil {
		query += ` AND (ts, id) < ($2, $3::uuid)`
		args = append(args, cursor.TS, cursor.ID)
	}
	query += ` ORDER BY ts DESC, id DESC LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, limit)

	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()

	result := make([]OperationRow, 0, limit)
	for rows.Next() {
		var row OperationRow
		var verdict sql.NullString
		if err := rows.Scan(&row.ID, &row.TS, &row.Proposer, &row.Class, &row.Status, &row.Payload, &verdict); err != nil {
			return nil, normalizeDBError(err)
		}
		if verdict.Valid && verdict.String != "" {
			row.Verdict = json.RawMessage(verdict.String)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return result, nil
}

func (s *Store) CountTradesForDay(shadow bool, start, end time.Time) (int, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	return countTradesForDay(ctx, s.DB, shadow, start, end, "")
}

func (t *ledgerTx) CountTradesForDay(shadow bool, start, end time.Time) (int, error) {
	return countTradesForDay(t.ctx, t.tx, shadow, start, end, "")
}

func (s *Store) CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	return countTradesForDay(ctx, s.DB, shadow, start, end, operationID)
}

func (t *ledgerTx) CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error) {
	return countTradesForDay(t.ctx, t.tx, shadow, start, end, operationID)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func countTradesForDay(ctx context.Context, db queryRower, shadow bool, start, end time.Time, operationID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM trade_grant
		 WHERE ledger = $1 AND market_day = $2::date
		   AND ($1 <> 'shadow'
		        OR NOT EXISTS (SELECT 1 FROM feature_activation WHERE name='m3a')
		        OR granted_at >= (SELECT activated_at FROM feature_activation WHERE name='m3a'))
		   AND (NULLIF($3,'') IS NULL OR operation_id <> NULLIF($3,'')::uuid)`,
		ledgerName(shadow), start, operationID).Scan(&n)
	return n, normalizeDBError(err)
}

// WithProposalLock takes the idempotency lock before the ledger lock. Both are
// transaction-scoped, so a crashed process cannot strand either gate.
func (s *Store) WithProposalLock(identity *IdempotencyIdentity, shadow, lockLedger bool, fn func(OperationGate) error) error {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if identity != nil {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, idempotencyLockKey(identity.Subject, identity.Key)); err != nil {
			return normalizeDBError(err)
		}
	}
	if lockLedger {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(shadow)); err != nil {
			return normalizeDBError(err)
		}
	}
	if err := fn(&ledgerTx{tx: tx, ctx: ctx, marketTZ: s.marketTZ}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return normalizeDBError(err)
	}
	committed = true
	return nil
}

func (s *Store) WithLedgerLock(shadow bool, fn func(OperationGate) error) error {
	return s.WithProposalLock(nil, shadow, true, fn)
}

// WithReviewLock serializes a review against the pending operation row. The
// callback decides when to acquire the stable ledger lock so TTL evaluation
// happens before any account read or entitlement lock.
func (s *Store) WithReviewLock(id string, fn func(OperationGate, *OperationRow) error) error {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	row, err := getOperation(ctx, tx, `WHERE id=$1 AND status='pending_review' FOR UPDATE`, id)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrOperationNotPending
	}
	if err := fn(&ledgerTx{tx: tx, ctx: ctx, marketTZ: s.marketTZ}, row); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return normalizeDBError(err)
	}
	committed = true
	return nil
}

func idempotencyLockKey(subject, key string) int64 {
	digest := sha256.Sum256([]byte(subject + "\x00" + key))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func ledgerLockKey(shadow bool) int64 {
	// M3A resources survive market midnight. The mutex is therefore stable per
	// ledger; market_day remains a query dimension and never enters this key.
	const keyBase int64 = 0x414c50484c454400 // "ALPHLED\0"
	key := keyBase
	if shadow {
		key |= 1
	}
	return key
}

func globalHaltSendLockKey() int64 {
	// This stable key is part of the database concurrency protocol. It is not a
	// configurable trading policy: every Kernel instance must serialize a
	// global Halt transition with Live open-placement send authorization.
	const key int64 = 0x414c5048484c5400 // "ALPHHLT\x00"
	return key
}

func (s *Store) InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error {
	ctx, cancel := s.deadline()
	defer cancel()
	var out any
	if outcome != nil {
		out = jstr(outcome)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO journal (operation_id, hypothesis, outcome, prompt_versions, shadow) VALUES ($1,$2,$3,$4,$5)`,
		operationID, jstr(hypothesis), out, jstr(promptVersions), shadow)
	return normalizeJournalError(normalizeDBError(err))
}

func normalizeJournalError(err error) error {
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return ErrInvalidOperationReference
	}
	return err
}

type Lesson struct {
	Text           string  `json:"text"`
	Confidence     float64 `json:"confidence"`
	ApplicableWhen string  `json:"applicable_when"`
}

func (s *Store) TopLessons(limit int) ([]Lesson, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx,
		`SELECT text, confidence, COALESCE(applicable_when,'') FROM lessons
		 WHERE expires_at IS NULL OR expires_at > now()
		 ORDER BY confidence DESC, ts DESC LIMIT $1`, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	out := []Lesson{}
	for rows.Next() {
		var l Lesson
		if err := rows.Scan(&l.Text, &l.Confidence, &l.ApplicableWhen); err != nil {
			return nil, normalizeDBError(err)
		}
		out = append(out, l)
	}
	return out, normalizeDBError(rows.Err())
}

func (s *Store) GetBlackboard(day string) (json.RawMessage, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var doc string
	err := s.DB.QueryRowContext(ctx, `SELECT doc::text FROM blackboard WHERE day=$1`, day).Scan(&doc)
	if err == sql.ErrNoRows {
		return json.RawMessage(`{}`), nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return json.RawMessage(doc), nil
}

func (s *Store) PutBlackboard(day string, doc json.RawMessage) error {
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO blackboard (day, doc) VALUES ($1,$2)
		 ON CONFLICT (day) DO UPDATE SET doc=EXCLUDED.doc, updated_at=now()`, day, string(doc))
	return normalizeDBError(err)
}

type GlobalHaltTransition struct {
	Reason                 string
	EventID                int64
	CutAt                  time.Time
	InFlightAttemptID      string
	InFlightAttemptState   string
	BlockedUnsentAttemptID string
}

func (s *Store) LoadGlobalHalt() (bool, string, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	halted, reason, _, _, err := loadGlobalHalt(ctx, s.DB)
	return halted, reason, normalizeDBError(err)
}

// ActivateGlobalHalt commits the database-authoritative Halt cut under the
// same advisory lock used by Live open-placement send authorization. A sent
// marker that commits first is reported as pre-cut in-flight work; a marker
// that loses this lock race must observe Halt and cannot reach the Provider.
func (s *Store) ActivateGlobalHalt(reason, subject, mode string) (GlobalHaltTransition, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return GlobalHaltTransition{}, normalizeDBError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, globalHaltSendLockKey()); err != nil {
		return GlobalHaltTransition{}, normalizeDBError(err)
	}
	halted, persistedReason, eventID, cutAt, err := loadGlobalHalt(ctx, tx)
	if err != nil {
		return GlobalHaltTransition{}, normalizeDBError(err)
	}
	if !halted {
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&cutAt); err != nil {
			return GlobalHaltTransition{}, normalizeDBError(err)
		}
		eventID, err = insertEventWithIDAt(ctx, tx, "global_halt_transition", map[string]any{
			"halted": true, "reason": reason, "subject": subject, "mode": mode,
		}, cutAt)
		if err != nil {
			return GlobalHaltTransition{}, normalizeDBError(err)
		}
		persistedReason = reason
	}
	transition := GlobalHaltTransition{Reason: persistedReason, EventID: eventID, CutAt: cutAt}
	var activeID, unknownID sql.NullString
	var activeSentAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT gate.active_attempt_id,gate.unknown_attempt_id,attempt.sent_at
		FROM live_execution_gate AS gate
		LEFT JOIN execution_attempt AS attempt ON attempt.id=gate.active_attempt_id
		WHERE gate.singleton=true`).Scan(&activeID, &unknownID, &activeSentAt); err != nil {
		return GlobalHaltTransition{}, normalizeDBError(err)
	}
	if unknownID.Valid {
		transition.InFlightAttemptID = unknownID.String
		transition.InFlightAttemptState = "unknown"
	} else if activeID.Valid && activeSentAt.Valid {
		transition.InFlightAttemptID = activeID.String
		transition.InFlightAttemptState = "active"
	} else if activeID.Valid {
		transition.BlockedUnsentAttemptID = activeID.String
	}
	if err := tx.Commit(); err != nil {
		return GlobalHaltTransition{}, normalizeDBError(err)
	}
	return transition, nil
}

func loadGlobalHalt(ctx context.Context, db queryRower) (bool, string, int64, time.Time, error) {
	var halted bool
	var reason string
	var eventID int64
	var cutAt time.Time
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE((payload->>'halted')::boolean, false), COALESCE(payload->>'reason',''), id, ts
		 FROM events WHERE kind='global_halt_transition' ORDER BY id DESC LIMIT 1`).
		Scan(&halted, &reason, &eventID, &cutAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", 0, time.Time{}, nil
	}
	return halted, reason, eventID, cutAt, err
}
