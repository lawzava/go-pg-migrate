package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"
)

// maxIdentifierLength is PostgreSQL's NAMEDATALEN - 1. Longer identifiers are
// truncated, so two longer names could address one table under two lock keys.
const maxIdentifierLength = 63

// ledger is the table that records applied versions.
type ledger struct {
	schema       string
	table        string
	name         string // quoted, schema-qualified
	lockID       int64
	schemaLockID int64
}

func newLedger(schema, table string) (ledger, error) {
	for _, ident := range []string{schema, table} {
		if strings.ContainsRune(ident, 0) {
			return ledger{}, fmt.Errorf("%w: identifier %q contains NUL", ErrInvalidConfig, ident)
		}

		if len(ident) > maxIdentifierLength {
			return ledger{}, fmt.Errorf("%w: identifier %q is longer than %d bytes",
				ErrInvalidConfig, ident, maxIdentifierLength)
		}
	}

	return ledger{
		schema: schema,
		table:  table,
		name:   quoteIdent(schema) + "." + quoteIdent(table),
		// Keys derive from names so that separate ledgers do not block each
		// other, except while creating a schema they share.
		lockID:       lockKey("go-pg-migrate\x00" + schema + "\x00" + table),
		schemaLockID: lockKey("go-pg-migrate schema\x00" + schema),
	}, nil
}

func lockKey(name string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(name))

	return int64(hash.Sum64()) //nolint:gosec // Wrapping is intended; any int64 is a valid lock key.
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

type ledgerShape int

const (
	shapeMissing ledgerShape = iota
	shapeV3
	shapeV2
)

func (l ledger) shape(ctx context.Context, db Executor) (ledgerShape, error) {
	// pg_attribute, unlike information_schema, lists columns of tables the
	// role cannot read, so a permission problem surfaces as an error instead
	// of an apparently missing ledger.
	rows, err := db.QueryContext(ctx,
		`SELECT attname::text FROM pg_attribute
		WHERE attrelid = to_regclass($1) AND attnum > 0 AND NOT attisdropped ORDER BY attname`,
		l.name)
	if err != nil {
		return 0, fmt.Errorf("inspect ledger %s: %w", l.name, err)
	}
	defer rows.Close()

	var columns []string

	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return 0, fmt.Errorf("inspect ledger %s: %w", l.name, err)
		}

		columns = append(columns, column)
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("inspect ledger %s: %w", l.name, err)
	}

	switch {
	case len(columns) == 0:
		return shapeMissing, nil
	case slices.Equal(columns, []string{"applied_at", "dirty", "name", "version"}):
		return shapeV3, nil
	case slices.Equal(columns, []string{"created_at", "id", "name", "number"}):
		return shapeV2, nil
	default:
		return 0, fmt.Errorf("%w: %s has columns %v", ErrIncompatibleLedger, l.name, columns)
	}
}

// lock takes the transaction-scoped advisory lock. Every write to the ledger
// happens under it, and it works through PgBouncer in transaction mode.
func (l ledger) lock(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", l.lockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}

	return nil
}

// ensure creates the ledger, or upgrades a v2 ledger in place. The caller holds
// the lock.
func (l ledger) ensure(ctx context.Context, tx *sql.Tx) error {
	shape, err := l.shape(ctx, tx)
	if err != nil {
		return err
	}

	switch shape {
	case shapeV3:
		return nil
	case shapeV2:
		return l.upgradeV2(ctx, tx)
	case shapeMissing:
	}

	if err := l.ensureSchema(ctx, tx); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `CREATE TABLE `+l.name+` (
		version BIGINT PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		dirty BOOLEAN NOT NULL DEFAULT false
	)`); err != nil {
		return fmt.Errorf("create ledger %s: %w", l.name, err)
	}

	return nil
}

// ensureSchema creates the ledger schema if it is missing. Ledgers sharing a
// schema hold different locks, so creation takes a schema lock and rechecks.
func (l ledger) ensureSchema(ctx context.Context, tx *sql.Tx) error {
	exists := func() (bool, error) {
		var found bool

		err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", l.schema).
			Scan(&found)
		if err != nil {
			return false, fmt.Errorf("check schema %s: %w", quoteIdent(l.schema), err)
		}

		return found, nil
	}

	// CREATE SCHEMA IF NOT EXISTS checks the database CREATE privilege even
	// when the schema exists, so check first.
	if found, err := exists(); err != nil || found {
		return err
	}

	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", l.schemaLockID); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}

	if found, err := exists(); err != nil || found {
		return err
	}

	if _, err := tx.ExecContext(ctx, "CREATE SCHEMA "+quoteIdent(l.schema)); err != nil {
		return fmt.Errorf("create schema %s: %w", quoteIdent(l.schema), err)
	}

	return nil
}

// upgradeV2 converts the v2 table (id, created_at, number INTEGER, name
// VARCHAR) in place, keeping every row.
func (l ledger) upgradeV2(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE ` + l.name + ` RENAME COLUMN number TO version`,
		`ALTER TABLE ` + l.name + ` RENAME COLUMN created_at TO applied_at`,
		`ALTER TABLE ` + l.name + `
			DROP COLUMN id,
			ALTER COLUMN version TYPE BIGINT,
			ALTER COLUMN name TYPE TEXT,
			ALTER COLUMN applied_at TYPE TIMESTAMPTZ,
			ADD COLUMN dirty BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE ` + l.name + ` ADD PRIMARY KEY (version)`,
	}

	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("upgrade v2 ledger %s: %w", l.name, err)
		}
	}

	return nil
}

func (l ledger) read(ctx context.Context, db Executor, shape ledgerShape) (map[int64]record, error) {
	query := `SELECT version, name, applied_at, dirty FROM ` + l.name

	switch shape {
	case shapeMissing:
		return map[int64]record{}, nil
	case shapeV2:
		query = `SELECT number, name, created_at, false FROM ` + l.name
	case shapeV3:
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read ledger %s: %w", l.name, err)
	}
	defer rows.Close()

	history := make(map[int64]record)

	for rows.Next() {
		var rec record
		if err := rows.Scan(&rec.version, &rec.name, &rec.appliedAt, &rec.dirty); err != nil {
			return nil, fmt.Errorf("read ledger %s: %w", l.name, err)
		}

		history[rec.version] = rec
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ledger %s: %w", l.name, err)
	}

	return history, nil
}

func (l ledger) insert(ctx context.Context, db Executor, mig Migration, dirty bool) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO `+l.name+` (version, name, dirty) VALUES ($1, $2, $3)`,
		mig.Version, mig.Name, dirty); err != nil {
		return fmt.Errorf("record version %d: %w", mig.Version, err)
	}

	return nil
}

func (l ledger) setDirty(ctx context.Context, db Executor, version int64, dirty bool) error {
	if _, err := db.ExecContext(ctx,
		`UPDATE `+l.name+` SET dirty = $2 WHERE version = $1`, version, dirty); err != nil {
		return fmt.Errorf("mark version %d dirty=%t: %w", version, dirty, err)
	}

	return nil
}

func (l ledger) remove(ctx context.Context, db Executor, version int64) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM `+l.name+` WHERE version = $1`, version); err != nil {
		return fmt.Errorf("remove version %d: %w", version, err)
	}

	return nil
}

// setTimeouts applies lock_timeout and statement_timeout. local scopes them to
// the current transaction; otherwise they last for the session. A negative
// lock timeout sets 0, which disables any server or connection default.
func setTimeouts(ctx context.Context, db Executor, lockTimeout, statementTimeout time.Duration, local bool) error {
	settings := map[string]string{}

	switch {
	case lockTimeout < 0:
		settings["lock_timeout"] = "0"
	case lockTimeout > 0:
		settings["lock_timeout"] = milliseconds(lockTimeout)
	}

	if statementTimeout > 0 {
		settings["statement_timeout"] = milliseconds(statementTimeout)
	}

	for name, value := range settings {
		if _, err := db.ExecContext(ctx, "SELECT set_config($1, $2, $3)", name, value, local); err != nil {
			return fmt.Errorf("set %s: %w", name, err)
		}
	}

	return nil
}

func milliseconds(d time.Duration) string {
	return fmt.Sprintf("%dms", max(d.Milliseconds(), 1))
}

// wrapLockTimeout marks PostgreSQL lock_not_available (55P03) errors with
// ErrLockTimeout. lib/pq and pgx errors both expose SQLState.
func wrapLockTimeout(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "55P03" {
		return fmt.Errorf("%w: %w", ErrLockTimeout, err)
	}

	return err
}

var errOpenTransaction = errors.New("migration left a transaction open")

// checkNoOpenTransaction fails if db is inside a transaction block. SAVEPOINT
// succeeds only inside one and otherwise fails with no_active_sql_transaction
// (25P01), on both the simple and extended protocols.
func checkNoOpenTransaction(ctx context.Context, db Executor) error {
	_, err := db.ExecContext(ctx, "SAVEPOINT go_pg_migrate_check")
	if err == nil {
		return errOpenTransaction
	}

	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "25P01" {
		return nil
	}

	return fmt.Errorf("check transaction state: %w", err)
}
