package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	migrate "github.com/lawzava/go-pg-migrate/v3"
	"github.com/lawzava/go-pg-migrate/v3/integration/pgtest"
)

const (
	// v2LedgerWithT1 is the exact table v2 created, with version 1 applied.
	v2LedgerWithT1 = `CREATE TABLE migrations (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			number INTEGER NOT NULL UNIQUE,
			name VARCHAR(255) NOT NULL
		);
		INSERT INTO migrations (number, name) VALUES (1, 'create t1');
		CREATE TABLE t1 (id int)`

	v3Ledger = `CREATE TABLE public.migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			dirty BOOLEAN NOT NULL DEFAULT false
		)`
)

// TestLockTimeoutBoundsLedgerStep checks that Config.LockTimeout also bounds
// the work on the ledger itself, which runs before any migration. In each case
// another session holds a lock that the ledger step must wait for.
func TestLockTimeoutBoundsLedgerStep(t *testing.T) {
	t.Parallel()

	runUp := func(ctx context.Context, m *migrate.Migrator) error { return m.Up(ctx) }

	cases := []struct {
		name    string
		setup   string // run before the blocker
		blocker string // run in a transaction that stays open
		run     func(ctx context.Context, m *migrate.Migrator) error
	}{
		{
			name:  "upgrade of a v2 ledger",
			setup: v2LedgerWithT1,
			// A reader holds ACCESS SHARE, which every ALTER TABLE waits for.
			blocker: "SELECT number FROM migrations",
			run:     runUp,
		},
		{
			name: "creation of the ledger",
			// An uncommitted table of the same name makes CREATE TABLE wait
			// for the transaction that created it.
			blocker: v3Ledger,
			run:     runUp,
		},
		{
			name:    "read of the ledger",
			setup:   v3Ledger + `; INSERT INTO public.migrations (version, name) VALUES (1, 'create t1'); CREATE TABLE t1 (id int)`,
			blocker: "LOCK TABLE public.migrations IN ACCESS EXCLUSIVE MODE",
			run:     runUp,
		},
		{
			name:  "write by Baseline",
			setup: v3Ledger,
			// SHARE lets the migrator read the ledger but not insert into it.
			blocker: "LOCK TABLE public.migrations IN SHARE MODE",
			run:     func(ctx context.Context, m *migrate.Migrator) error { return m.Baseline(ctx, 1) },
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
				if testCase.setup != "" {
					exec(t, db, testCase.setup)
				}

				blocker, err := db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = blocker.Rollback() }()

				if _, err := blocker.ExecContext(t.Context(), testCase.blocker); err != nil {
					t.Fatal(err)
				}

				m := newMigrator(t, db, tables(1), migrate.Config{LockTimeout: 200 * time.Millisecond})

				// Without a lock timeout the call waits until this context ends.
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()

				started := time.Now()
				err = testCase.run(ctx, m)

				wantErr(t, err, migrate.ErrLockTimeout)

				if elapsed := time.Since(started); elapsed > 3*time.Second {
					t.Errorf("call took %s, want it bounded by the lock timeout", elapsed)
				}

				// The failed call changed nothing: once the lock is gone, the
				// same call succeeds.
				if err := blocker.Rollback(); err != nil {
					t.Fatal(err)
				}

				if err := testCase.run(t.Context(), m); err != nil {
					t.Fatal(err)
				}

				check(t, "ledger", ledgerVersions(t, db), "1")
			})
		})
	}
}

// hookedConnector calls onConnect before it opens each connection.
type hookedConnector struct {
	driver    driver.Driver
	dsn       string
	onConnect func()
}

func (c hookedConnector) Connect(context.Context) (driver.Conn, error) {
	c.onConnect()

	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}

	return conn, nil
}

func (c hookedConnector) Driver() driver.Driver { return c.driver }

// TestLockTimeoutBoundsNoTxLedgerRead covers the second read of the ledger,
// which a NoTx migration does on its own connection. The lock on the ledger
// must appear after the planning transaction and before that read, so the
// test takes it when the migrator opens its second connection.
func TestLockTimeoutBoundsNoTxLedgerRead(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, dsn string, db *sql.DB) {
		exec(t, db, v3Ledger)

		blocker, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback() }()

		var (
			connects atomic.Int64
			lockErr  error
		)

		pool := sql.OpenDB(hookedConnector{driver: db.Driver(), dsn: dsn, onConnect: func() {
			if connects.Add(1) == 2 {
				_, lockErr = blocker.ExecContext(context.Background(), "LOCK TABLE public.migrations IN ACCESS EXCLUSIVE MODE")
			}
		}})

		t.Cleanup(func() { _ = pool.Close() })

		// No idle connections: the planning transaction and the NoTx session
		// each open their own.
		pool.SetMaxIdleConns(0)

		m := newMigrator(t, pool, []migrate.Migration{
			{Version: 1, Name: "create t1", Up: migrate.SQL("CREATE TABLE t1 (id int)"), NoTx: true},
		}, migrate.Config{LockTimeout: 200 * time.Millisecond})

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		started := time.Now()
		err = m.Up(ctx)

		wantErr(t, err, migrate.ErrLockTimeout)

		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Errorf("Up took %s, want it bounded by the lock timeout", elapsed)
		}

		check(t, "connections opened", connects.Load(), int64(2))

		if lockErr != nil {
			t.Fatal(lockErr)
		}

		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}

		check(t, "advisory locks", advisoryLocks(t, db), "0")
		check(t, "ledger after the timeout", ledgerVersions(t, db), "")

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1")
	})
}

// TestLockTimeoutDoesNotBoundRunnerWait checks that a runner still waits for
// another runner for longer than Config.LockTimeout. PostgreSQL applies
// lock_timeout to advisory locks too, so the migrator must set it only after
// it holds its own.
func TestLockTimeoutDoesNotBoundRunnerWait(t *testing.T) {
	t.Parallel()

	// The holder sleeps inside its migration. The waiter's own migration is
	// instant, so any time the waiter takes is time it waited for the holder.
	slow := []migrate.Migration{{Version: 1, Name: "first", Up: migrate.SQL("SELECT pg_sleep(2)")}}
	fast := []migrate.Migration{{Version: 1, Name: "first", Up: migrate.SQL("SELECT 1")}}

	cases := []struct {
		name string
		// The configurations of the runner that holds the lock and of the
		// one that waits for it.
		holder, waiter migrate.Config
	}{
		{name: "migration lock"},
		{
			// Ledgers in one schema hold different migration locks. The first
			// runner creates the schema and holds the schema lock until its
			// migration commits.
			name:   "schema lock",
			holder: migrate.Config{Schema: "shared", Table: "ledger_a"},
			waiter: migrate.Config{Schema: "shared", Table: "ledger_b"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
				waiterConfig := testCase.waiter
				waiterConfig.LockTimeout = 200 * time.Millisecond

				holder := newMigrator(t, db, slow, testCase.holder)
				waiter := newMigrator(t, pgtest.Open(t, driver, dsn), fast, waiterConfig)

				done := make(chan error, 1)

				go func() { done <- holder.Up(context.Background()) }()

				// Wait until the holder is inside its migration. Each test has
				// its own database in one cluster.
				waitFor(t, db, `SELECT count(*) > 0 FROM pg_stat_activity
					WHERE datname = current_database() AND pid <> pg_backend_pid() AND query LIKE '%pg_sleep(2)%'`)

				started := time.Now()

				if err := waiter.Up(t.Context()); err != nil {
					t.Errorf("waiter: %v, want it to wait for the holder", err)
				}

				// Three times the waiter's LockTimeout, and well inside the
				// holder's two seconds.
				if elapsed := time.Since(started); elapsed < 600*time.Millisecond {
					t.Errorf("waiter returned after %s, want it to wait for the holder", elapsed)
				}

				if err := <-done; err != nil {
					t.Errorf("holder: %v", err)
				}
			})
		})
	}
}

// waitFor polls a boolean query until it returns true.
func waitFor(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		var ok bool
		if err := db.QueryRowContext(t.Context(), query).Scan(&ok); err != nil {
			t.Fatalf("%s: %v", query, err)
		}

		if ok {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s: still false after 5s", query)
		}

		time.Sleep(20 * time.Millisecond)
	}
}
