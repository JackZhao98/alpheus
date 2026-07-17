package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const migrationLockKey int64 = 0x414c50484d494752 // "ALPHMIGR"

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type Migration struct {
	Version  int
	Filename string
	SQL      string
	Checksum [sha256.Size]byte
}

func LoadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory")
	}
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("migration directory contains a subdirectory")
		}
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version")
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil || len(bytes.TrimSpace(raw)) == 0 {
			return nil, fmt.Errorf("read migration %04d", version)
		}
		migrations = append(migrations, Migration{
			Version: version, Filename: entry.Name(), SQL: string(raw), Checksum: sha256.Sum256(raw),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	for i, migration := range migrations {
		if migration.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 0001")
		}
	}
	return migrations, nil
}

func Migrate(ctx context.Context, db *sql.DB, migrations []Migration, marketTZ string) error {
	if db == nil || len(migrations) == 0 || marketTZ == "" {
		return fmt.Errorf("migration input is incomplete")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('alpheus.tz_market', $1, true)`, marketTZ); err != nil {
		return fmt.Errorf("configure migration market timezone: %w", err)
	}

	exists, err := migrationTableExists(ctx, tx)
	if err != nil {
		return err
	}
	if !exists {
		tables, err := userTables(ctx, tx)
		if err != nil {
			return err
		}
		switch len(tables) {
		case 0:
			if err := createMigrationTable(ctx, tx); err != nil {
				return err
			}
		default:
			if err := validateLegacyM2(ctx, tx, tables); err != nil {
				return err
			}
			if err := createMigrationTable(ctx, tx); err != nil {
				return err
			}
			if migrations[0].Version != 1 {
				return fmt.Errorf("0001 migration is required for legacy baseline")
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, checksum) VALUES ($1,$2)`,
				1, migrations[0].Checksum[:]); err != nil {
				return fmt.Errorf("record legacy migration baseline: %w", err)
			}
		}
	}

	applied, err := appliedMigrations(ctx, tx)
	if err != nil {
		return err
	}
	known := make(map[int]Migration, len(migrations))
	for _, migration := range migrations {
		known[migration.Version] = migration
	}
	for version, checksum := range applied {
		migration, ok := known[version]
		if !ok {
			return fmt.Errorf("applied migration %04d is missing from the source tree", version)
		}
		if !bytes.Equal(checksum, migration.Checksum[:]) {
			return fmt.Errorf("migration %04d checksum mismatch; applied migrations are immutable", version)
		}
	}
	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply migration %04d: %w", migration.Version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, checksum) VALUES ($1,$2)`,
			migration.Version, migration.Checksum[:]); err != nil {
			return fmt.Errorf("record migration %04d: %w", migration.Version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func migrationTableExists(ctx context.Context, tx *sql.Tx) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='schema_migrations'
	)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("inspect migration table: %w", err)
	}
	return exists, nil
}

func createMigrationTable(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		checksum BYTEA NOT NULL CHECK (octet_length(checksum) = 32),
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func appliedMigrations(ctx context.Context, tx *sql.Tx) (map[int][]byte, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int][]byte{}
	for rows.Next() {
		var version int
		var checksum []byte
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = append([]byte(nil), checksum...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return applied, nil
}

func userTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_type='BASE TABLE'
		  AND table_name <> 'schema_migrations' ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan legacy tables: %w", err)
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

type legacyColumn struct {
	UDT         string
	Nullable    bool
	DefaultKind string
}

var legacyM2Columns = map[string]legacyColumn{
	"blackboard.day": {"date", false, ""}, "blackboard.doc": {"jsonb", false, ""}, "blackboard.updated_at": {"timestamptz", false, "now"},
	"events.id": {"int8", false, "nextval"}, "events.ts": {"timestamptz", false, "now"}, "events.kind": {"text", false, ""}, "events.payload": {"jsonb", false, ""},
	"fills.id": {"int8", false, "nextval"}, "fills.order_id": {"uuid", true, ""}, "fills.qty": {"numeric", false, ""}, "fills.price": {"numeric", false, ""}, "fills.ts": {"timestamptz", false, "now"},
	"journal.id": {"int8", false, "nextval"}, "journal.operation_id": {"uuid", true, ""}, "journal.hypothesis": {"jsonb", false, ""}, "journal.outcome": {"jsonb", true, ""}, "journal.prompt_versions": {"jsonb", true, ""}, "journal.shadow": {"bool", false, "false"}, "journal.ts_open": {"timestamptz", false, "now"}, "journal.ts_close": {"timestamptz", true, ""},
	"lessons.id": {"int8", false, "nextval"}, "lessons.ts": {"timestamptz", false, "now"}, "lessons.text": {"text", false, ""}, "lessons.confidence": {"numeric", false, ""}, "lessons.applicable_when": {"text", true, ""}, "lessons.source_journal_id": {"int8", true, ""}, "lessons.expires_at": {"timestamptz", true, ""},
	"operations.id": {"uuid", false, ""}, "operations.ts": {"timestamptz", false, "now"}, "operations.proposer": {"text", false, ""}, "operations.class": {"bpchar", false, ""}, "operations.status": {"text", false, ""}, "operations.payload": {"jsonb", false, ""}, "operations.verdict": {"jsonb", true, ""},
	"orders.id": {"uuid", false, ""}, "orders.operation_id": {"uuid", true, ""}, "orders.broker_order_id": {"text", true, ""}, "orders.state": {"text", false, ""}, "orders.payload": {"jsonb", false, ""}, "orders.updated_at": {"timestamptz", false, "now"},
}

var legacyM2Constraints = map[string]string{
	"blackboard_pkey":                "PRIMARY KEY (day)",
	"events_pkey":                    "PRIMARY KEY (id)",
	"fills_order_id_fkey":            "FOREIGN KEY (order_id) REFERENCES orders(id)",
	"fills_pkey":                     "PRIMARY KEY (id)",
	"journal_operation_id_fkey":      "FOREIGN KEY (operation_id) REFERENCES operations(id)",
	"journal_pkey":                   "PRIMARY KEY (id)",
	"lessons_pkey":                   "PRIMARY KEY (id)",
	"lessons_source_journal_id_fkey": "FOREIGN KEY (source_journal_id) REFERENCES journal(id)",
	"operations_pkey":                "PRIMARY KEY (id)",
	"orders_operation_id_fkey":       "FOREIGN KEY (operation_id) REFERENCES operations(id)",
	"orders_pkey":                    "PRIMARY KEY (id)",
}

var legacyM2Indexes = map[string]struct {
	Unique bool
	Suffix string
}{
	"blackboard_pkey": {true, "USING btree (day)"},
	"events_pkey":     {true, "USING btree (id)"},
	"fills_pkey":      {true, "USING btree (id)"},
	"journal_pkey":    {true, "USING btree (id)"},
	"lessons_pkey":    {true, "USING btree (id)"},
	"operations_pkey": {true, "USING btree (id)"},
	"ops_day_ledger":  {false, "USING btree (ts, COALESCE(((payload ->> 'shadow'::text))::boolean, false))"},
	"orders_pkey":     {true, "USING btree (id)"},
}

func validateLegacyM2(ctx context.Context, tx *sql.Tx, tables []string) error {
	wantTables := []string{"blackboard", "events", "fills", "journal", "lessons", "operations", "orders"}
	if strings.Join(tables, "\x00") != strings.Join(wantTables, "\x00") {
		return legacyRepairError()
	}

	rows, err := tx.QueryContext(ctx, `SELECT table_name, column_name, udt_name, is_nullable, COALESCE(column_default,'')
		FROM information_schema.columns WHERE table_schema=current_schema()
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		return fmt.Errorf("inspect legacy columns: %w", err)
	}
	columns := map[string]legacyColumn{}
	for rows.Next() {
		var table, column, udt, nullable, defaultValue string
		if err := rows.Scan(&table, &column, &udt, &nullable, &defaultValue); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy columns: %w", err)
		}
		columns[table+"."+column] = legacyColumn{UDT: udt, Nullable: nullable == "YES", DefaultKind: classifyDefault(defaultValue)}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect legacy columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect legacy columns: %w", err)
	}
	if !equalLegacyColumns(columns, legacyM2Columns) {
		return legacyRepairError()
	}

	constraintRows, err := tx.QueryContext(ctx, `SELECT c.conname, pg_get_constraintdef(c.oid)
		FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace
		WHERE n.nspname=current_schema() ORDER BY c.conname`)
	if err != nil {
		return fmt.Errorf("inspect legacy constraints: %w", err)
	}
	constraints := map[string]string{}
	for constraintRows.Next() {
		var name, definition string
		if err := constraintRows.Scan(&name, &definition); err != nil {
			constraintRows.Close()
			return fmt.Errorf("scan legacy constraints: %w", err)
		}
		constraints[name] = normalizeDefinition(definition)
	}
	if err := constraintRows.Err(); err != nil {
		constraintRows.Close()
		return fmt.Errorf("inspect legacy constraints: %w", err)
	}
	if err := constraintRows.Close(); err != nil {
		return fmt.Errorf("inspect legacy constraints: %w", err)
	}
	if !equalStrings(constraints, legacyM2Constraints) {
		return legacyRepairError()
	}

	indexRows, err := tx.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname=current_schema() ORDER BY indexname`)
	if err != nil {
		return fmt.Errorf("inspect legacy indexes: %w", err)
	}
	seenIndexes := map[string]bool{}
	for indexRows.Next() {
		var name, definition string
		if err := indexRows.Scan(&name, &definition); err != nil {
			indexRows.Close()
			return fmt.Errorf("scan legacy indexes: %w", err)
		}
		want, ok := legacyM2Indexes[name]
		if !ok || strings.HasPrefix(definition, "CREATE UNIQUE INDEX") != want.Unique || !strings.HasSuffix(normalizeDefinition(definition), want.Suffix) {
			indexRows.Close()
			return legacyRepairError()
		}
		seenIndexes[name] = true
	}
	if err := indexRows.Err(); err != nil {
		indexRows.Close()
		return fmt.Errorf("inspect legacy indexes: %w", err)
	}
	if err := indexRows.Close(); err != nil {
		return fmt.Errorf("inspect legacy indexes: %w", err)
	}
	if len(seenIndexes) != len(legacyM2Indexes) {
		return legacyRepairError()
	}
	return nil
}

func classifyDefault(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "nextval("):
		return "nextval"
	case value == "now()":
		return "now"
	case value == "false":
		return "false"
	default:
		return value
	}
}

func equalLegacyColumns(got, want map[string]legacyColumn) bool {
	if len(got) != len(want) {
		return false
	}
	for key, expected := range want {
		if got[key] != expected {
			return false
		}
	}
	return true
}

func equalStrings(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for key, expected := range want {
		if got[key] != expected {
			return false
		}
	}
	return true
}

func normalizeDefinition(value string) string { return strings.Join(strings.Fields(value), " ") }

func legacyRepairError() error {
	return fmt.Errorf("legacy schema does not match the M2 fingerprint; repair it before retrying migrations")
}
