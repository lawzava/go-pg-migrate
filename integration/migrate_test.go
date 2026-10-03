package integration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	migrate "github.com/lawzava/go-pg-migrate/v3"
	"github.com/lawzava/go-pg-migrate/v3/integration/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

// eachDriver runs fn once per driver, in parallel, each with a fresh database.
// Callers mark their own test parallel.
func eachDriver(t *testing.T, fn func(t *testing.T, driver, dsn string, db *sql.DB)) {
	t.Helper()

	for _, driver := range pgtest.Drivers {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()

			dsn := pgtest.DSN(t)
			fn(t, driver, dsn, pgtest.Open(t, driver, dsn))
		})
	}
}

func table(version int64) migrate.Migration {
	return migrate.Migration{
		Version: version,
		Name:    fmt.Sprintf("create t%d", version),
		Up:      migrate.SQL(fmt.Sprintf("CREATE TABLE t%d (id int)", version)),
		Down:    migrate.SQL(fmt.Sprintf("DROP TABLE t%d", version)),
	}
}

func tables(versions ...int64) []migrate.Migration {
	out := make([]migrate.Migration, 0, len(versions))
	for _, v := range versions {
		out = append(out, table(v))
	}

	return out
}

func newMigrator(t *testing.T, db *sql.DB, migrations []migrate.Migration, config migrate.Config) *migrate.Migrator {
	t.Helper()

	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}

	m, err := migrate.New(db, migrations, config)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func ledgerVersions(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT coalesce(string_agg(version::text, ',' ORDER BY version), '') FROM public.migrations`)
}

func userTables(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT coalesce(string_agg(table_schema || '.' || table_name, ',' ORDER BY table_schema, table_name), '')
		FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog', 'information_schema')`)
}

func queryString(t *testing.T, db *sql.DB, query string) string {
	t.Helper()

	var out string
	if err := db.QueryRowContext(t.Context(), query).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return out
}

func exec(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
}

func check[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestUpDownStatus(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		var logs bytes.Buffer

		m := newMigrator(t, db, tables(1, 2, 3), migrate.Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
		ctx := t.Context()

		if err := m.Up(ctx); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Up", ledgerVersions(t, db), "1,2,3")
		check(t, "tables after Up", userTables(t, db), "public.migrations,public.t1,public.t2,public.t3")

		for _, field := range []string{`"version":3`, `"direction":"up"`, `"name":"create t3"`, `"duration":`, `"lock_wait":`} {
			if !strings.Contains(logs.String(), field) {
				t.Errorf("log missing %s:\n%s", field, logs.String())
			}
		}

		if err := m.Down(ctx, 1); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Down(1)", ledgerVersions(t, db), "1")
		check(t, "tables after Down(1)", userTables(t, db), "public.migrations,public.t1")

		if err := m.Down(ctx, 0); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Down(0)", ledgerVersions(t, db), "")
		check(t, "tables after Down(0)", userTables(t, db), "public.migrations")

		if err := m.UpTo(ctx, 2); err != nil {
			t.Fatal(err)
		}

		if err := m.UpTo(ctx, 1); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after UpTo(2), UpTo(1)", ledgerVersions(t, db), "1,2")

		status, err := m.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]string, 0, len(status))
		for _, e := range status {
			got = append(got, fmt.Sprintf("%d:%s", e.Version, e.State))

			if e.State == migrate.StateApplied && e.AppliedAt.IsZero() {
				t.Errorf("version %d has no AppliedAt", e.Version)
			}
		}

		check(t, "status", strings.Join(got, ","), "1:applied,2:applied,3:pending")

		if err := db.PingContext(ctx); err != nil {
			t.Errorf("caller's db unusable after migrating: %v", err)
		}
	})
}

func TestUpLedgerFailureRollsBackMigration(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		m := newMigrator(t, db, tables(1, 2), migrate.Config{})

		if err := m.UpTo(t.Context(), 1); err != nil {
			t.Fatal(err)
		}

		exec(t, db, `CREATE FUNCTION reject() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'ledger write rejected'; END $$;
			CREATE TRIGGER reject BEFORE INSERT ON public.migrations FOR EACH ROW EXECUTE FUNCTION reject()`)

		err := m.Up(t.Context())
		if err == nil || !strings.Contains(err.Error(), "ledger write rejected") {
			t.Fatalf("err = %v, want ledger write rejected", err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1")
		check(t, "tables", userTables(t, db), "public.migrations,public.t1")

		exec(t, db, "DROP TRIGGER reject ON public.migrations")

		if err := m.Up(t.Context()); err != nil {
			t.Fatalf("rerun: %v", err)
		}

		check(t, "ledger after rerun", ledgerVersions(t, db), "1,2")
	})
}

func TestDownLedgerFailureRollsBackMigration(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		m := newMigrator(t, db, tables(1, 2), migrate.Config{})

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		exec(t, db, `CREATE FUNCTION reject() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'ledger delete rejected'; END $$;
			CREATE TRIGGER reject BEFORE DELETE ON public.migrations FOR EACH ROW EXECUTE FUNCTION reject()`)

		err := m.Down(t.Context(), 1)
		if err == nil || !strings.Contains(err.Error(), "ledger delete rejected") {
			t.Fatalf("err = %v, want ledger delete rejected", err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1,2")
		check(t, "tables", userTables(t, db), "public.migrations,public.t1,public.t2")

		exec(t, db, "DROP TRIGGER reject ON public.migrations")

		if err := m.Down(t.Context(), 1); err != nil {
			t.Fatalf("rerun: %v", err)
		}

		check(t, "ledger after rerun", ledgerVersions(t, db), "1")
	})
}

func TestConcurrentRunnersApplyOnce(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		migrations := []migrate.Migration{
			{Version: 1, Name: "log table", Up: migrate.SQL("CREATE TABLE runs (version int)")},
			// A data migration: a double run would insert two rows, not fail.
			{Version: 2, Name: "backfill", Up: migrate.SQL("SELECT pg_sleep(0.2); INSERT INTO runs VALUES (2)")},
			{Version: 3, Name: "backfill", Up: migrate.SQL("INSERT INTO runs VALUES (3)")},
		}

		const runners = 4

		var wg sync.WaitGroup

		errs := make([]error, runners)

		for i := range runners {
			// Separate pools stand in for separate processes.
			m := newMigrator(t, pgtest.Open(t, driver, dsn), migrations, migrate.Config{})

			wg.Go(func() { errs[i] = m.Up(t.Context()) })
		}

		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("runner %d: %v", i, err)
			}
		}

		check(t, "ledger", ledgerVersions(t, db), "1,2,3")
		check(t, "runs", queryString(t, db, "SELECT string_agg(version::text, ',' ORDER BY version) FROM runs"), "2,3")
	})
}

func TestPanicRollsBackAndReleasesConnection(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		m := newMigrator(t, db, []migrate.Migration{{
			Version: 1,
			Name:    "panics",
			Up: func(ctx context.Context, db migrate.Executor) error {
				if _, err := db.ExecContext(ctx, "CREATE TABLE half (id int)"); err != nil {
					return err
				}

				panic("boom")
			},
		}}, migrate.Config{})

		func() {
			defer func() {
				if r := recover(); r != "boom" {
					t.Fatalf("recover() = %v, want boom", r)
				}
			}()

			_ = m.Up(t.Context())
		}()

		check(t, "idle in transaction", queryString(t, db,
			"SELECT count(*)::text FROM pg_stat_activity WHERE datname = current_database() AND state = 'idle in transaction'"), "0")
		// The ledger was created in the same transaction, so it rolled back too.
		check(t, "tables", userTables(t, db), "")
	})
}

func TestCommitFailureReportsCause(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		m := newMigrator(t, db, []migrate.Migration{{
			Version: 1,
			Name:    "deferred violation",
			Up: migrate.SQL(`CREATE TABLE d (id int UNIQUE DEFERRABLE INITIALLY DEFERRED);
				INSERT INTO d VALUES (1), (1)`),
		}}, migrate.Config{})

		err := m.Up(t.Context())

		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "23505" {
			t.Fatalf("err = %v, want unique_violation (23505)", err)
		}

		check(t, "tables", userTables(t, db), "")
	})
}

func TestTimestampVersionsAndLongNames(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		long := strings.Repeat("n", 300)
		m := newMigrator(t, db, []migrate.Migration{
			{Version: 20261003120000, Name: long, Up: migrate.SQL("CREATE TABLE ts (id int)")},
			{Version: 20261004120000, Name: "second", Up: migrate.SQL("SELECT 1")},
		}, migrate.Config{})

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", ledgerVersions(t, db), "20261003120000,20261004120000")
		check(t, "name", queryString(t, db, "SELECT name FROM public.migrations WHERE version = 20261003120000"), long)
	})
}

// counting wraps migrations so a test can assert that nothing ran.
func counting(calls *atomic.Int64, migrations []migrate.Migration) []migrate.Migration {
	out := make([]migrate.Migration, len(migrations))

	for i, mig := range migrations {
		up := mig.Up
		mig.Up = func(ctx context.Context, db migrate.Executor) error {
			calls.Add(1)

			return up(ctx, db)
		}
		out[i] = mig
	}

	return out
}

func TestHistoryChecksRunNothing(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		ctx := t.Context()

		if err := newMigrator(t, db, tables(1, 3, 4), migrate.Config{}).Up(ctx); err != nil {
			t.Fatal(err)
		}

		var calls atomic.Int64

		// Version 4 exists in the database but not in this code.
		err := newMigrator(t, db, counting(&calls, tables(1, 3, 5)), migrate.Config{}).Up(ctx)
		wantErr(t, err, migrate.ErrUnknownVersion)

		// Version 2 was added below applied version 4.
		late := counting(&calls, tables(1, 2, 3, 4, 5))
		wantErr(t, newMigrator(t, db, late, migrate.Config{}).Up(ctx), migrate.ErrOutOfOrder)

		wantErr(t, newMigrator(t, db, late, migrate.Config{}).UpTo(ctx, 99), migrate.ErrUnknownTarget)
		wantErr(t, newMigrator(t, db, late, migrate.Config{}).Down(ctx, 99), migrate.ErrUnknownTarget)

		check(t, "migration calls", calls.Load(), int64(0))
		check(t, "ledger", ledgerVersions(t, db), "1,3,4")

		if err := newMigrator(t, db, late, migrate.Config{AllowOutOfOrder: true}).Up(ctx); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after AllowOutOfOrder", ledgerVersions(t, db), "1,2,3,4,5")
	})
}

func TestDownChecksWholePathFirst(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		migrations := tables(1, 2, 3)
		migrations[1].Down = nil

		m := newMigrator(t, db, migrations, migrate.Config{})

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		wantErr(t, m.Down(t.Context(), 0), migrate.ErrIrreversible)
		check(t, "ledger", ledgerVersions(t, db), "1,2,3")
		check(t, "tables", userTables(t, db), "public.migrations,public.t1,public.t2,public.t3")

		if err := m.Down(t.Context(), 2); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Down(2)", ledgerVersions(t, db), "1,2")
	})
}

func TestSearchPathDoesNotMoveLedger(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		exec(t, db, "CREATE SCHEMA app")

		var calls atomic.Int64

		migrations := counting(&calls, tables(1))
		appDB := pgtest.Open(t, driver, dsn+"&options=-c%20search_path%3Dapp")

		if err := newMigrator(t, appDB, migrations, migrate.Config{}).Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		if err := newMigrator(t, db, migrations, migrate.Config{}).Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "migration calls", calls.Load(), int64(1))
		check(t, "tables", userTables(t, db), "app.t1,public.migrations")
	})
}

func TestStatusIsReadOnly(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		status, err := newMigrator(t, db, tables(1), migrate.Config{}).Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		check(t, "entries", len(status), 1)
		check(t, "state", status[0].State, migrate.StatePending)
		check(t, "tables", userTables(t, db), "")
	})
}

func TestBaseline(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		var calls atomic.Int64

		m := newMigrator(t, db, counting(&calls, tables(1, 2, 3)), migrate.Config{})
		ctx := t.Context()

		wantErr(t, m.Baseline(ctx, 9), migrate.ErrUnknownTarget)

		if err := m.Baseline(ctx, 2); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1,2")
		check(t, "migration calls", calls.Load(), int64(0))

		wantErr(t, m.Baseline(ctx, 3), migrate.ErrLedgerNotEmpty)

		if err := m.Up(ctx); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Up", ledgerVersions(t, db), "1,2,3")
		check(t, "migration calls after Up", calls.Load(), int64(1))
	})
}

func TestLockTimeout(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		ctx := t.Context()
		migrations := []migrate.Migration{
			table(1),
			{Version: 2, Name: "alter", Up: migrate.SQL("ALTER TABLE t1 ADD COLUMN extra int")},
		}

		m := newMigrator(t, db, migrations, migrate.Config{LockTimeout: 200 * time.Millisecond})

		if err := m.UpTo(ctx, 1); err != nil {
			t.Fatal(err)
		}

		// An open reader holds ACCESS SHARE on t1, which the ALTER must wait for.
		reader, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Rollback() }()

		if _, err := reader.ExecContext(ctx, "SELECT id FROM t1"); err != nil {
			t.Fatal(err)
		}

		started := time.Now()
		err = m.Up(ctx)

		wantErr(t, err, migrate.ErrLockTimeout)

		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Errorf("Up took %s, want it bounded by the lock timeout", elapsed)
		}

		check(t, "ledger", ledgerVersions(t, db), "1")
	})
}

func TestContextCancelsLockWait(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		slow := []migrate.Migration{{Version: 1, Name: "slow", Up: migrate.SQL("SELECT pg_sleep(2)")}}
		holder := newMigrator(t, db, slow, migrate.Config{})
		waiter := newMigrator(t, pgtest.Open(t, driver, dsn), slow, migrate.Config{})

		done := make(chan error, 1)

		go func() { done <- holder.Up(context.Background()) }()

		time.Sleep(300 * time.Millisecond)

		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()

		started := time.Now()
		err := waiter.Up(ctx)

		if err == nil || time.Since(started) > time.Second {
			t.Errorf("waiter: err = %v after %s, want a prompt cancellation error", err, time.Since(started))
		}

		if err := <-done; err != nil {
			t.Errorf("holder: %v", err)
		}
	})
}

func advisoryLocks(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT count(*)::text FROM pg_locks
		WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`)
}

func TestNoTx(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		// One connection: the NoTx path must not need a second one, and any
		// session state it leaves behind would be visible to the next query.
		db.SetMaxOpenConns(1)

		m := newMigrator(t, db, []migrate.Migration{
			table(1),
			{
				Version: 2,
				Name:    "index",
				Up:      migrate.SQL("CREATE INDEX CONCURRENTLY t1_id ON t1 (id)"),
				Down:    migrate.SQL("DROP INDEX CONCURRENTLY t1_id"),
				NoTx:    true,
			},
			table(3),
		}, migrate.Config{LockTimeout: 2 * time.Second})

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1,2,3")
		check(t, "dirty rows", queryString(t, db, "SELECT count(*)::text FROM public.migrations WHERE dirty"), "0")
		check(t, "index", queryString(t, db, "SELECT count(*)::text FROM pg_indexes WHERE indexname = 't1_id'"), "1")
		check(t, "advisory locks", advisoryLocks(t, db), "0")
		check(t, "lock_timeout", queryString(t, db, "SHOW lock_timeout"), "0")

		if err := m.Down(t.Context(), 1); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Down", ledgerVersions(t, db), "1")
		check(t, "index after Down", queryString(t, db, "SELECT count(*)::text FROM pg_indexes WHERE indexname = 't1_id'"), "0")
	})
}

func TestNoTxFailureLeavesDirty(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		db.SetMaxOpenConns(1)

		ctx := t.Context()
		m := newMigrator(t, db, []migrate.Migration{
			table(1),
			{Version: 2, Name: "bad index", Up: migrate.SQL("CREATE INDEX CONCURRENTLY ON missing (id)"), NoTx: true},
		}, migrate.Config{})

		if err := m.Up(ctx); err == nil {
			t.Fatal("Up succeeded, want failure")
		}

		check(t, "advisory locks", advisoryLocks(t, db), "0")
		check(t, "lock_timeout", queryString(t, db, "SHOW lock_timeout"), "0")

		status, err := m.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}

		check(t, "version 2 state", status[1].State, migrate.StateDirty)

		wantErr(t, m.Up(ctx), migrate.ErrDirty)
		wantErr(t, m.Down(ctx, 0), migrate.ErrDirty)
		wantErr(t, m.Resolve(ctx, 1, true), migrate.ErrNotDirty)

		if err := m.Resolve(ctx, 2, false); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger after Resolve", ledgerVersions(t, db), "1")
	})
}

func TestUpgradesV2Ledger(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		// The exact table v2 created, with two applied versions.
		exec(t, db, `CREATE TABLE migrations (
				id SERIAL PRIMARY KEY,
				created_at TIMESTAMP NOT NULL DEFAULT NOW(),
				number INTEGER NOT NULL UNIQUE,
				name VARCHAR(255) NOT NULL
			);
			INSERT INTO migrations (number, name, created_at) VALUES
				(1, 'create t1', '2024-01-02 03:04:05'), (2, 'create t2', '2024-01-02 03:04:05');
			CREATE TABLE t1 (id int); CREATE TABLE t2 (id int)`)

		m := newMigrator(t, db, append(tables(1, 2), table(20261003120000)), migrate.Config{})

		status, err := m.Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		check(t, "status before upgrade", fmt.Sprint(status[0].State, status[1].State, status[2].State),
			fmt.Sprint(migrate.StateApplied, migrate.StateApplied, migrate.StatePending))
		check(t, "v2 column kept by Status", queryString(t, db,
			"SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'migrations' AND column_name = 'number'"), "1")

		if err := m.Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", ledgerVersions(t, db), "1,2,20261003120000")
		check(t, "columns", queryString(t, db, `SELECT string_agg(column_name || ':' || data_type, ',' ORDER BY column_name)
			FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'migrations'`),
			"applied_at:timestamp with time zone,dirty:boolean,name:text,version:bigint")
		check(t, "applied_at kept", queryString(t, db,
			"SELECT to_char(applied_at, 'YYYY-MM-DD') FROM public.migrations WHERE version = 1"), "2024-01-02")
	})
}

func TestIncompatibleLedger(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		exec(t, db, "CREATE TABLE migrations (id int, label text)")

		m := newMigrator(t, db, tables(1), migrate.Config{})

		wantErr(t, m.Up(t.Context()), migrate.ErrIncompatibleLedger)

		_, err := m.Status(t.Context())
		wantErr(t, err, migrate.ErrIncompatibleLedger)
	})
}

func TestCustomLedgerLocation(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		config := migrate.Config{Schema: `odd"schema`, Table: "schema_migrations"}

		if err := newMigrator(t, db, tables(1), config).Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		check(t, "ledger", queryString(t, db, `SELECT string_agg(version::text, ',') FROM "odd""schema".schema_migrations`), "1")
		check(t, "default ledger absent", queryString(t, db, "SELECT coalesce(to_regclass('public.migrations')::text, '')"), "")
	})
}

func TestNoTxSessionStateDoesNotLeak(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, _ string, db *sql.DB) {
		// One connection, so the next query would reuse the migration's session.
		db.SetMaxOpenConns(1)

		role := "r_" + queryString(t, db, "SELECT current_database()")
		exec(t, db, "CREATE ROLE "+role)
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP ROLE "+role) })

		wantUser := queryString(t, db, "SELECT current_user")
		wantPath := queryString(t, db, "SHOW search_path")

		dirtySession := func(fail bool) migrate.Func {
			return func(ctx context.Context, db migrate.Executor) error {
				for _, query := range []string{
					"SET search_path TO pg_catalog",
					"CREATE TEMP TABLE leftover (id int)",
					"SET ROLE " + role,
					"BEGIN",
				} {
					if _, err := db.ExecContext(ctx, query); err != nil {
						return err
					}
				}

				if fail {
					return errors.New("failed with an open transaction")
				}

				return nil
			}
		}

		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s fail=%t", driver, fail), func(t *testing.T) {
				err := newMigrator(t, db, []migrate.Migration{
					{Version: 1, Name: "dirty session", Up: dirtySession(fail), NoTx: true},
				}, migrate.Config{}).Up(t.Context())
				if err == nil {
					t.Error("Up succeeded although the migration left a transaction open")
				}

				check(t, "ledger dirty", queryString(t, db, "SELECT dirty::text FROM public.migrations WHERE version = 1"), "true")

				check(t, "current_user", queryString(t, db, "SELECT current_user"), wantUser)
				check(t, "search_path", queryString(t, db, "SHOW search_path"), wantPath)
				check(t, "temp table", queryString(t, db, "SELECT coalesce(to_regclass('pg_temp.leftover')::text, '')"), "")
				// SAVEPOINT succeeds only inside a transaction block.
				if _, err := db.ExecContext(t.Context(), "SAVEPOINT probe"); err == nil {
					t.Error("pooled connection is still inside the migration's transaction")
				}

				exec(t, db, "DROP TABLE IF EXISTS public.migrations")
			})
		}
	})
}

func TestRepeatableReadDefaultDoesNotBreakReplanning(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		exec(t, db, "DO $$ BEGIN EXECUTE format('ALTER DATABASE %I SET default_transaction_isolation = ''repeatable read''', current_database()); END $$")

		migrations := []migrate.Migration{
			{Version: 1, Name: "slow", Up: migrate.SQL("SELECT pg_sleep(0.3); CREATE TABLE once (id int)")},
		}

		var wg sync.WaitGroup

		errs := make([]error, 3)

		for i := range errs {
			m := newMigrator(t, pgtest.Open(t, driver, dsn), migrations, migrate.Config{})

			wg.Go(func() { errs[i] = m.Up(t.Context()) })
		}

		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("runner %d: %v", i, err)
			}
		}
	})
}

func TestNegativeLockTimeoutOverridesServerSetting(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		ctx := t.Context()
		exec(t, db, "CREATE TABLE t1 (id int)")

		reader, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := reader.ExecContext(ctx, "SELECT id FROM t1"); err != nil {
			t.Fatal(err)
		}

		go func() {
			time.Sleep(500 * time.Millisecond)

			_ = reader.Rollback()
		}()

		// The connection's own lock_timeout is 100ms; LockTimeout -1 must lift it.
		short := pgtest.Open(t, driver, dsn+"&options=-c%20lock_timeout%3D100")
		m := newMigrator(t, short, []migrate.Migration{
			{Version: 1, Name: "alter", Up: migrate.SQL("ALTER TABLE t1 ADD COLUMN extra int")},
		}, migrate.Config{LockTimeout: -1})

		if err := m.Up(ctx); err != nil {
			t.Fatalf("Up with LockTimeout -1: %v", err)
		}
	})
}

func TestLedgersShareNewSchema(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, _ *sql.DB) {
		var wg sync.WaitGroup

		errs := make([]error, 4)

		for i := range errs {
			config := migrate.Config{Schema: "shared", Table: fmt.Sprintf("ledger_%d", i)}
			m := newMigrator(t, pgtest.Open(t, driver, dsn), nil, config)

			wg.Go(func() { errs[i] = m.Up(t.Context()) })
		}

		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("runner %d: %v", i, err)
			}
		}
	})
}

func TestStatusReportsUnreadableLedger(t *testing.T) {
	t.Parallel()
	eachDriver(t, func(t *testing.T, driver, dsn string, db *sql.DB) {
		if err := newMigrator(t, db, tables(1), migrate.Config{}).Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		// A role that can connect but cannot read the ledger.
		role := "reader_" + queryString(t, db, "SELECT current_database()")
		exec(t, db, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'reader'", role))
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP OWNED BY "+role+"; DROP ROLE "+role) })

		readerDSN, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}

		readerDSN.User = url.UserPassword(role, "reader")

		status, err := newMigrator(t, pgtest.Open(t, driver, readerDSN.String()), tables(1), migrate.Config{}).
			Status(t.Context())
		if err == nil {
			t.Fatalf("Status = %+v, want a permission error instead of reporting version 1 pending", status)
		}
	})
}
