package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"time"
)

const (
	defaultSchema      = "public"
	defaultTable       = "migrations"
	defaultLockTimeout = 5 * time.Second

	// cleanupTimeout bounds unlocking a NoTx session after the caller's
	// context is done.
	cleanupTimeout = 5 * time.Second
)

// Config adjusts a Migrator. The zero value is ready to use.
type Config struct {
	// Schema holds the ledger table. Default "public". It is created if missing.
	Schema string

	// Table names the ledger table. Default "migrations", the v2 name, so an
	// existing v2 ledger is upgraded in place.
	Table string

	// LockTimeout bounds each wait for a table lock inside a migration, so a
	// blocked ALTER fails instead of queueing every reader behind it.
	// Default 5s. Negative disables it.
	LockTimeout time.Duration

	// StatementTimeout bounds each statement inside a migration. Zero keeps
	// the server setting.
	StatementTimeout time.Duration

	// AllowOutOfOrder applies pending migrations older than the newest applied
	// version instead of returning ErrOutOfOrder.
	AllowOutOfOrder bool

	// Logger receives one event per applied or reverted migration.
	// Default slog.Default().
	Logger *slog.Logger
}

// Migrator applies migrations to one database. Its methods are safe for
// concurrent use, including from separate processes: an advisory lock
// serializes every change to the ledger.
type Migrator struct {
	db         *sql.DB
	migrations []Migration // sorted by version
	ledger     ledger
	config     Config
	log        *slog.Logger
}

// New validates migrations and returns a Migrator. It does not touch the
// database. The caller owns db and closes it.
func New(db *sql.DB, migrations []Migration, config Config) (*Migrator, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db is nil", ErrInvalidConfig)
	}

	if err := Validate(migrations); err != nil {
		return nil, err
	}

	config.Schema = cmp.Or(config.Schema, defaultSchema)
	config.Table = cmp.Or(config.Table, defaultTable)
	config.LockTimeout = cmp.Or(config.LockTimeout, defaultLockTimeout)

	ledger, err := newLedger(config.Schema, config.Table)
	if err != nil {
		return nil, err
	}

	sorted := slices.Clone(migrations)
	slices.SortFunc(sorted, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })

	return &Migrator{
		db:         db,
		migrations: sorted,
		ledger:     ledger,
		config:     config,
		log:        cmp.Or(config.Logger, slog.Default()),
	}, nil
}

// Up applies every pending migration, oldest first.
func (m *Migrator) Up(ctx context.Context) error {
	return m.up(ctx, math.MaxInt64)
}

// UpTo applies pending migrations up to and including version. It never
// reverts: if version is already applied, it does nothing.
func (m *Migrator) UpTo(ctx context.Context, version int64) error {
	if !isKnown(m.migrations, version) {
		return fmt.Errorf("%w: %d", ErrUnknownTarget, version)
	}

	return m.up(ctx, version)
}

// Down reverts applied migrations newer than version, newest first. Zero
// reverts everything. It checks the whole path first and reverts nothing if
// any migration on it lacks Down.
func (m *Migrator) Down(ctx context.Context, version int64) error {
	return m.run(ctx, directionDown, func(history map[int64]record) ([]Migration, error) {
		return planDown(m.migrations, history, version)
	})
}

// Status lists every known and applied version in version order. It only
// reads: it neither creates nor upgrades the ledger.
func (m *Migrator) Status(ctx context.Context) ([]Entry, error) {
	for attempt := 0; ; attempt++ {
		shape, err := m.ledger.shape(ctx, m.db)
		if err != nil {
			return nil, err
		}

		history, err := m.ledger.read(ctx, m.db, shape)
		if err == nil {
			return buildStatus(m.migrations, history), nil
		}

		// A concurrent runner may have upgraded a v2 ledger between the two
		// queries, renaming the columns this read used. Inspect it once more.
		if shape != shapeV2 || attempt > 0 {
			return nil, err
		}
	}
}

// Baseline records every migration up to and including version as applied
// without running it. Use it once to adopt a database whose schema already
// matches those migrations. The ledger must be empty.
func (m *Migrator) Baseline(ctx context.Context, version int64) error {
	if !isKnown(m.migrations, version) {
		return fmt.Errorf("%w: %d", ErrUnknownTarget, version)
	}

	return m.inLockedTx(ctx, func(tx *sql.Tx, history map[int64]record) error {
		if len(history) > 0 {
			return fmt.Errorf("%w: %d versions recorded", ErrLedgerNotEmpty, len(history))
		}

		for _, mig := range m.migrations {
			if mig.Version > version {
				break
			}

			if err := m.ledger.insert(ctx, tx, mig, false); err != nil {
				return err
			}
		}

		return nil
	})
}

// Resolve clears a dirty version after a NoTx migration failed. Inspect the
// database first. Pass applied=true if the migration's change is in effect,
// or false if it is not.
func (m *Migrator) Resolve(ctx context.Context, version int64, applied bool) error {
	return m.inLockedTx(ctx, func(tx *sql.Tx, history map[int64]record) error {
		if !history[version].dirty {
			return fmt.Errorf("%w: %d", ErrNotDirty, version)
		}

		if applied {
			return m.ledger.setDirty(ctx, tx, version, false)
		}

		return m.ledger.remove(ctx, tx, version)
	})
}

type direction string

const (
	directionUp   direction = "up"
	directionDown direction = "down"
)

func (d direction) fn(mig Migration) Func {
	if d == directionDown {
		return mig.Down
	}

	return mig.Up
}

type planFunc func(history map[int64]record) ([]Migration, error)

func (m *Migrator) up(ctx context.Context, target int64) error {
	return m.run(ctx, directionUp, func(history map[int64]record) ([]Migration, error) {
		return planUp(m.migrations, history, target, m.config.AllowOutOfOrder)
	})
}

// run applies a plan one migration per transaction. Each transaction takes the
// lock and re-plans from the ledger, so concurrent runners never apply the
// same version twice, and the first iteration rejects an invalid plan before
// any migration runs.
func (m *Migrator) run(ctx context.Context, dir direction, plan planFunc) error {
	for {
		done, err := m.step(ctx, dir, plan)
		if err != nil || done {
			return err
		}
	}
}

func (m *Migrator) step(ctx context.Context, dir direction, plan planFunc) (bool, error) {
	var (
		next     Migration
		found    bool
		lockWait time.Duration
	)

	started := time.Now()

	err := m.inLockedTx(ctx, func(tx *sql.Tx, history map[int64]record) error {
		lockWait = time.Since(started)

		steps, err := plan(history)
		if err != nil || len(steps) == 0 {
			return err
		}

		next, found = steps[0], true
		if next.NoTx {
			return nil
		}

		if err := setTimeouts(ctx, tx, m.config.LockTimeout, m.config.StatementTimeout, true); err != nil {
			return err
		}

		if err := dir.fn(next)(ctx, tx); err != nil {
			return fmt.Errorf("migration %d (%s) %s: %w", next.Version, next.Name, dir, wrapLockTimeout(err))
		}

		if dir == directionUp {
			return m.ledger.insert(ctx, tx, next, false)
		}

		return m.ledger.remove(ctx, tx, next.Version)
	})
	if err != nil {
		return false, err
	}

	if !found {
		return true, nil
	}

	if next.NoTx {
		return false, m.applyNoTx(ctx, next, dir, plan)
	}

	m.logApplied(ctx, next, dir, lockWait, time.Since(started))

	return false, nil
}

// inLockedTx runs fn in a transaction that holds the migration lock and has
// an up-to-date ledger.
func (m *Migrator) inLockedTx(ctx context.Context, fn func(*sql.Tx, map[int64]record) error) error {
	// Read committed lets the ledger read after the lock wait see what the
	// previous holder committed, whatever the server default isolation is.
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Runs on every path, including a panic in a migration. After Commit it is
	// a no-op.
	defer func() { _ = tx.Rollback() }()

	if err := m.ledger.lock(ctx, tx); err != nil {
		return err
	}

	if err := m.ledger.ensure(ctx, tx); err != nil {
		return err
	}

	history, err := m.ledger.read(ctx, tx, shapeV3)
	if err != nil {
		return err
	}

	if err := fn(tx, history); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// applyNoTx runs mig on a dedicated connection that holds the session-level
// advisory lock. PgBouncer in transaction mode cannot hold a session lock;
// NoTx migrations need a direct connection or session pooling.
func (m *Migrator) applyNoTx(ctx context.Context, mig Migration, dir direction, plan planFunc) error {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open connection: %w", err)
	}

	started := time.Now()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", m.ledger.lockID); err != nil {
		_ = conn.Close()

		return fmt.Errorf("acquire migration lock: %w", err)
	}

	lockWait := time.Since(started)

	defer m.releaseSession(ctx, conn)

	// Another runner may have moved the ledger while this one waited.
	history, err := m.ledger.read(ctx, conn, shapeV3)
	if err != nil {
		return err
	}

	steps, err := plan(history)
	if err != nil || len(steps) == 0 || steps[0].Version != mig.Version {
		return err
	}

	if err := m.runNoTx(ctx, conn, mig, dir); err != nil {
		return err
	}

	m.logApplied(ctx, mig, dir, lockWait, time.Since(started))

	return nil
}

// runNoTx marks the version dirty before the change and clears it after, so an
// interrupted run is visible instead of silently half-done.
func (m *Migrator) runNoTx(ctx context.Context, conn *sql.Conn, mig Migration, dir direction) error {
	if err := setTimeouts(ctx, conn, m.config.LockTimeout, m.config.StatementTimeout, false); err != nil {
		return err
	}

	var err error
	if dir == directionUp {
		err = m.ledger.insert(ctx, conn, mig, true)
	} else {
		err = m.ledger.setDirty(ctx, conn, mig.Version, true)
	}

	if err != nil {
		return err
	}

	if err := dir.fn(mig)(ctx, conn); err != nil {
		return fmt.Errorf("migration %d (%s) %s without transaction, version left dirty: %w",
			mig.Version, mig.Name, dir, wrapLockTimeout(err))
	}

	// Clearing the dirty flag inside a transaction the migration left open
	// would be rolled back when the session is discarded.
	if err := checkNoOpenTransaction(ctx, conn); err != nil {
		return fmt.Errorf("migration %d (%s) %s without transaction, version left dirty: %w",
			mig.Version, mig.Name, dir, err)
	}

	if dir == directionUp {
		return m.ledger.setDirty(ctx, conn, mig.Version, false)
	}

	return m.ledger.remove(ctx, conn, mig.Version)
}

// releaseSession unlocks and then discards the NoTx connection. A migration
// can leave state that RESET ALL does not clear, such as SET ROLE, temporary
// tables, or an open transaction, so the session is never returned to the
// pool. Ending the session also releases the lock if the unlock failed.
func (m *Migrator) releaseSession(ctx context.Context, conn *sql.Conn) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()

	// Unlocking first lets a waiting runner proceed without waiting for the
	// server to notice the closed session.
	_, _ = conn.ExecContext(cleanupCtx, "SELECT pg_advisory_unlock($1)", m.ledger.lockID)
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func (m *Migrator) logApplied(ctx context.Context, mig Migration, dir direction, lockWait, elapsed time.Duration) {
	m.log.InfoContext(ctx, "migration "+string(dir),
		slog.Int64("version", mig.Version),
		slog.String("name", mig.Name),
		slog.String("direction", string(dir)),
		slog.Bool("no_tx", mig.NoTx),
		slog.Duration("lock_wait", lockWait),
		slog.Duration("duration", elapsed),
	)
}
