// Package store is the persistence layer. The kernel is the only writer;
// agents go through the HTTP API.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type Store struct{ DB *sql.DB }

var ErrInvalidOperationReference = errors.New("invalid operation reference")

// OperationGate is the minimum store surface used while the kernel holds a
// per-ledger, per-market-day advisory transaction lock. Count, event, and
// insert must use the same implementation passed to WithLedgerLock.
type OperationGate interface {
	CountTradesForDay(shadow bool, start, end time.Time) (int, error)
	InsertEvent(kind string, payload any) error
	InsertOperation(id, proposer, class, status string, payload, verdict any) error
}

type ledgerTx struct{ tx *sql.Tx }

// Open waits for postgres on cold start.
func Open(url string) (*Store, error) {
	var db *sql.DB
	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("postgres", url)
		if err == nil {
			if err = db.Ping(); err == nil {
				return &Store{DB: db}, nil
			}
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("postgres unreachable: %w", err)
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
	return insertEvent(s.DB, kind, payload)
}

func (t *ledgerTx) InsertEvent(kind string, payload any) error {
	return insertEvent(t.tx, kind, payload)
}

func insertEvent(db execer, kind string, payload any) error {
	_, err := db.Exec(`INSERT INTO events (kind, payload) VALUES ($1, $2)`, kind, jstr(payload))
	return err
}

func (s *Store) InsertOperation(id, proposer, class, status string, payload, verdict any) error {
	return insertOperation(s.DB, id, proposer, class, status, payload, verdict)
}

func (t *ledgerTx) InsertOperation(id, proposer, class, status string, payload, verdict any) error {
	return insertOperation(t.tx, id, proposer, class, status, payload, verdict)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertOperation(db execer, id, proposer, class, status string, payload, verdict any) error {
	_, err := db.Exec(
		`INSERT INTO operations (id, proposer, class, status, payload, verdict) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, proposer, class, status, jstr(payload), jstr(verdict))
	return err
}

func (s *Store) SetOperationStatus(id, status string, verdict any) error {
	if verdict == nil {
		_, err := s.DB.Exec(`UPDATE operations SET status=$1 WHERE id=$2`, status, id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE operations SET status=$1, verdict=$2 WHERE id=$3`, status, jstr(verdict), id)
	return err
}

type OperationRow struct {
	ID       string          `json:"id"`
	TS       time.Time       `json:"ts"`
	Proposer string          `json:"proposer"`
	Class    string          `json:"class"`
	Status   string          `json:"status"`
	Payload  json.RawMessage `json:"payload"`
	Verdict  json.RawMessage `json:"verdict"`
}

func (s *Store) GetOperation(id string) (*OperationRow, error) {
	var r OperationRow
	var verdict sql.NullString
	err := s.DB.QueryRow(
		`SELECT id, ts, proposer, class, status, payload, COALESCE(verdict::text,'') FROM operations WHERE id=$1`, id).
		Scan(&r.ID, &r.TS, &r.Proposer, &r.Class, &r.Status, &r.Payload, &verdict)
	if err != nil {
		return nil, err
	}
	if verdict.Valid && verdict.String != "" {
		r.Verdict = json.RawMessage(verdict.String)
	}
	return &r, nil
}

func (s *Store) CountTradesForDay(shadow bool, start, end time.Time) (int, error) {
	return countTradesForDay(s.DB, shadow, start, end)
}

func (t *ledgerTx) CountTradesForDay(shadow bool, start, end time.Time) (int, error) {
	return countTradesForDay(t.tx, shadow, start, end)
}

type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

func countTradesForDay(db queryRower, shadow bool, start, end time.Time) (int, error) {
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM operations
		 WHERE class='B'
		   AND ts >= $2
		   AND ts < $3
		   AND status IN ('auto_approved','executed')
		   AND COALESCE((payload->>'shadow')::bool, false) = $1`, shadow, start, end).Scan(&n)
	return n, err
}

// WithLedgerLock serializes risk classification for one ledger and market day
// across kernel processes. The transaction-scoped lock is released on commit
// or rollback, so a crashed request cannot strand the gate.
func (s *Store) WithLedgerLock(shadow bool, marketDay time.Time, fn func(OperationGate) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(shadow, marketDay)); err != nil {
		return err
	}
	if err := fn(&ledgerTx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func ledgerLockKey(shadow bool, marketDay time.Time) int64 {
	year, month, day := marketDay.Date()
	dateKey := int64(year*10_000 + int(month)*100 + day)
	const namespace int64 = 0x414c5048 // "ALPH"
	key := (namespace << 32) | (dateKey << 1)
	if shadow {
		key |= 1
	}
	return key
}

func (s *Store) InsertJournal(operationID string, hypothesis, outcome, promptVersions any, shadow bool) error {
	var out any
	if outcome != nil {
		out = jstr(outcome)
	}
	_, err := s.DB.Exec(
		`INSERT INTO journal (operation_id, hypothesis, outcome, prompt_versions, shadow) VALUES ($1,$2,$3,$4,$5)`,
		operationID, jstr(hypothesis), out, jstr(promptVersions), shadow)
	return normalizeJournalError(err)
}

func normalizeJournalError(err error) error {
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
	rows, err := s.DB.Query(
		`SELECT text, confidence, COALESCE(applicable_when,'') FROM lessons
		 WHERE expires_at IS NULL OR expires_at > now()
		 ORDER BY confidence DESC, ts DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Lesson{}
	for rows.Next() {
		var l Lesson
		if err := rows.Scan(&l.Text, &l.Confidence, &l.ApplicableWhen); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetBlackboard(day string) (json.RawMessage, error) {
	var doc string
	err := s.DB.QueryRow(`SELECT doc::text FROM blackboard WHERE day=$1`, day).Scan(&doc)
	if err == sql.ErrNoRows {
		return json.RawMessage(`{}`), nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(doc), nil
}

func (s *Store) PutBlackboard(day string, doc json.RawMessage) error {
	_, err := s.DB.Exec(
		`INSERT INTO blackboard (day, doc) VALUES ($1,$2)
		 ON CONFLICT (day) DO UPDATE SET doc=EXCLUDED.doc, updated_at=now()`, day, string(doc))
	return err
}

func (s *Store) LoadGlobalHalt() (bool, string, error) {
	var halted bool
	var reason string
	err := s.DB.QueryRow(
		`SELECT COALESCE((payload->>'halted')::boolean, false), COALESCE(payload->>'reason','')
		 FROM events WHERE kind='global_halt_transition' ORDER BY id DESC LIMIT 1`).
		Scan(&halted, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return halted, reason, err
}
