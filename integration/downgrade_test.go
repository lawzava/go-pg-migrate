package integration

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	migrate "github.com/lawzava/go-pg-migrate/v3"
)

// downgradeScript is the file the changelog points v2 rollbacks to. The test
// runs its exact content.
const downgradeScript = "../docs/downgrade-to-v2.sql"

// runScript sends the whole script in one Exec, as psql -f would, on its own
// connection. A failed script leaves that session inside an aborted
// transaction, so the test ends it the way psql's COMMIT would: by rolling
// back.
func runScript(t *testing.T, db *sql.DB, script string) error {
	t.Helper()

	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(t.Context(), script)
	if err != nil {
		if _, rollbackErr := conn.ExecContext(t.Context(), "ROLLBACK"); rollbackErr != nil {
			t.Fatalf("rollback after failed script: %v", rollbackErr)
		}
	}

	return err //nolint:wrapcheck // The caller inspects the PostgreSQL error.
}

func ledgerConstraints(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT coalesce(string_agg(conname || ' ' || pg_get_constraintdef(oid), ', ' ORDER BY conname), '')
		FROM pg_constraint WHERE conrelid = 'public.migrations'::regclass AND contype IN ('p', 'u')`)
}

func ledgerColumns(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT string_agg(attname || ' ' || format_type(atttypid, atttypmod)
			|| CASE WHEN attnotnull THEN ' NOT NULL' ELSE '' END, ', ' ORDER BY attname)
		FROM pg_attribute WHERE attrelid = 'public.migrations'::regclass AND attnum > 0 AND NOT attisdropped`)
}

// v3Rows lists every v3 ledger row. The timestamp is shown as the v2 column
// type, in the session time zone, so it compares with v2Rows.
func v3Rows(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT coalesce(string_agg(
			version || '|' || name || '|' || applied_at::timestamp || '|' || dirty, E'\n' ORDER BY version), '')
		FROM public.migrations`)
}

func v2Rows(t *testing.T, db *sql.DB) string {
	t.Helper()

	return queryString(t, db, `SELECT coalesce(string_agg(
			number || '|' || name || '|' || created_at || '|false', E'\n' ORDER BY number), '')
		FROM public.migrations`)
}

func TestDowngradeToV2Script(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(downgradeScript)
	if err != nil {
		t.Fatal(err)
	}

	script := string(content)

	// The shapes a v3 ledger can have, with the constraints each starts
	// with. Both hold versions 1, 2, and 3.
	shapes := []struct {
		name        string
		setup       func(t *testing.T, db *sql.DB)
		constraints string
	}{
		{
			name: "created by v3",
			setup: func(t *testing.T, db *sql.DB) {
				if err := newMigrator(t, db, tables(1, 2, 3), migrate.Config{}).Up(t.Context()); err != nil {
					t.Fatal(err)
				}
			},
			constraints: "migrations_pkey PRIMARY KEY (version)",
		},
		{
			name: "upgraded from v2",
			setup: func(t *testing.T, db *sql.DB) {
				// The exact table v2 created, with two applied versions.
				exec(t, db, `CREATE TABLE migrations (
						id SERIAL PRIMARY KEY,
						created_at TIMESTAMP NOT NULL DEFAULT NOW(),
						number INTEGER NOT NULL UNIQUE,
						name VARCHAR(255) NOT NULL
					);
					INSERT INTO migrations (number, name, created_at) VALUES
						(1, 'create t1', '2024-01-02 03:04:05.678901'), (2, 'create t2', '2024-01-02 03:04:06');
					CREATE TABLE t1 (id int); CREATE TABLE t2 (id int)`)

				if err := newMigrator(t, db, tables(1, 2, 3), migrate.Config{}).Up(t.Context()); err != nil {
					t.Fatal(err)
				}
			},
			constraints: "migrations_pkey PRIMARY KEY (version)",
		},
		{
			// v3.0.x kept the unique constraint of v2 when it upgraded a
			// ledger, and later versions do not remove it.
			name: "upgraded from v2 by v3.0",
			setup: func(t *testing.T, db *sql.DB) {
				if err := newMigrator(t, db, tables(1, 2, 3), migrate.Config{}).Up(t.Context()); err != nil {
					t.Fatal(err)
				}

				exec(t, db, "ALTER TABLE public.migrations ADD CONSTRAINT migrations_number_key UNIQUE (version)")
			},
			constraints: "migrations_number_key UNIQUE (version), migrations_pkey PRIMARY KEY (version)",
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
				shape.setup(t, db)
				check(t, "constraints before", ledgerConstraints(t, db), shape.constraints)

				checkDowngradePreconditions(t, db, script)

				// Rows at the limits of the v2 column types, applied before
				// the others and in an order that is not version order.
				exec(t, db, `INSERT INTO public.migrations (version, name, applied_at) VALUES
					(2147483647, repeat('n', 255), '2020-05-06 07:08:09.123456+00'),
					(2147483646, 'limit minus one', '2020-05-06 07:08:09.123456+00'),
					(1000, 'oldest', '2019-01-01 00:00:00+00')`)

				rows := v3Rows(t, db)

				if err := runScript(t, db, script); err != nil {
					t.Fatalf("downgrade: %v", err)
				}

				checkV2Ledger(t, db, rows)

				// v3 upgrades the table again and applies what is pending.
				known := append(tables(1, 2, 3, 1000, 2147483646, 2147483647), table(20261003120000))
				if err := newMigrator(t, db, known, migrate.Config{}).Up(t.Context()); err != nil {
					t.Fatalf("Up after downgrade: %v", err)
				}

				check(t, "ledger after upgrading again", ledgerVersions(t, db), "1,2,3,1000,2147483646,2147483647,20261003120000")
				check(t, "rows kept by upgrading again", queryString(t, db, `SELECT string_agg(
						version || '|' || name || '|' || applied_at::timestamp || '|' || dirty, E'\n' ORDER BY version)
					FROM public.migrations WHERE version <> 20261003120000`), rows)
				check(t, "columns after upgrading again", ledgerColumns(t, db),
					"applied_at timestamp with time zone NOT NULL, dirty boolean NOT NULL, name text NOT NULL, version bigint NOT NULL")
				check(t, "constraints after upgrading again", ledgerConstraints(t, db),
					"migrations_pkey PRIMARY KEY (version)")
			})
		})
	}
}

// checkDowngradePreconditions breaks each data precondition in turn. The
// script must raise and leave the ledger as it was.
func checkDowngradePreconditions(t *testing.T, db *sql.DB, script string) {
	t.Helper()

	failures := []struct {
		name, breakIt, repair, want string
	}{
		{
			name:    "dirty row",
			breakIt: "UPDATE public.migrations SET dirty = true WHERE version = 2",
			repair:  "UPDATE public.migrations SET dirty = false WHERE version = 2",
			want:    "1 dirty row",
		},
		{
			name:    "version above INTEGER",
			breakIt: "INSERT INTO public.migrations (version, name) VALUES (2147483648, 'too large')",
			repair:  "DELETE FROM public.migrations WHERE version = 2147483648",
			want:    "1 version(s) outside 1 to 2147483647",
		},
		{
			name:    "name above 255 characters",
			breakIt: "INSERT INTO public.migrations (version, name) VALUES (9, repeat('n', 256))",
			repair:  "DELETE FROM public.migrations WHERE version = 9",
			want:    "1 name(s) longer than 255 characters",
		},
		{
			// v3 rejects this table, so converting it would leave a
			// ledger that no later v3 run can upgrade.
			name:    "extra column",
			breakIt: "ALTER TABLE public.migrations ADD COLUMN metadata text",
			repair:  "ALTER TABLE public.migrations DROP COLUMN metadata",
			want:    "is not a v3 ledger",
		},
	}

	for _, failure := range failures {
		exec(t, db, failure.breakIt)

		before := fmt.Sprint(ledgerColumns(t, db), ledgerConstraints(t, db), v3Rows(t, db))

		err := runScript(t, db, script)
		if err == nil || !strings.Contains(err.Error(), failure.want) {
			t.Fatalf("%s: err = %v, want it to contain %q", failure.name, err, failure.want)
		}

		check(t, failure.name+": ledger unchanged", fmt.Sprint(ledgerColumns(t, db), ledgerConstraints(t, db), v3Rows(t, db)), before)

		exec(t, db, failure.repair)
	}
}

// checkV2Ledger checks the downgraded table against the one v2.3.0 creates,
// and runs the statements v2.3.0 sends to it. rows is v3Rows before the
// downgrade.
func checkV2Ledger(t *testing.T, db *sql.DB, rows string) {
	t.Helper()

	check(t, "columns", ledgerColumns(t, db),
		"created_at timestamp without time zone NOT NULL, id integer NOT NULL, name character varying(255) NOT NULL, number integer NOT NULL")
	check(t, "constraints", ledgerConstraints(t, db),
		"migrations_number_key UNIQUE (number), migrations_pkey PRIMARY KEY (id)")
	check(t, "id sequence", queryString(t, db, "SELECT pg_get_serial_sequence('public.migrations', 'id')"),
		"public.migrations_id_seq")
	check(t, "defaults", queryString(t, db, `SELECT string_agg(a.attname || ' ' || pg_get_expr(d.adbin, d.adrelid), ', ' ORDER BY a.attname)
		FROM pg_attrdef d JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
		WHERE d.adrelid = 'public.migrations'::regclass`),
		"created_at now(), id nextval('migrations_id_seq'::regclass)")

	check(t, "rows", v2Rows(t, db), rows)
	// id follows the order of application, as it did when v2 inserted the rows.
	check(t, "id order", queryString(t, db, "SELECT string_agg(number::text, ',' ORDER BY id) FROM public.migrations"),
		queryString(t, db, "SELECT string_agg(number::text, ',' ORDER BY created_at, number) FROM public.migrations"))
	check(t, "ids", queryString(t, db, "SELECT string_agg(id::text, ',' ORDER BY id) FROM public.migrations"), "1,2,3,4,5,6")

	// The library sees a v2 ledger and reads it without changing it.
	status, err := newMigrator(t, db, tables(1, 2, 3, 1000, 2147483646, 2147483647), migrate.Config{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range status {
		check(t, fmt.Sprintf("status of version %d", entry.Version), entry.State, migrate.StateApplied)
	}

	check(t, "v2 columns kept by Status", queryString(t, db,
		"SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'migrations' AND column_name IN ('id', 'number')"), "2")

	// The statements below are v2.3.0's, from its repository.go.
	exec(t, db, `
		CREATE TABLE IF NOT EXISTS migrations (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			number INTEGER NOT NULL UNIQUE,
			name VARCHAR(255) NOT NULL
		)
	`)
	check(t, "v2 latest number", queryString(t, db, "SELECT number FROM migrations ORDER BY number DESC LIMIT 1"), "2147483647")

	if _, err := db.ExecContext(t.Context(), "INSERT INTO migrations (number, name) VALUES ($1, $2)", 2147483645, "v2 insert"); err != nil {
		t.Fatalf("v2 insert: %v", err)
	}

	check(t, "v2 insert takes the next id and a timestamp", queryString(t, db,
		"SELECT id || ' ' || (created_at > now()::timestamp - interval '1 hour') FROM migrations WHERE number = 2147483645"), "7 true")

	if _, err := db.ExecContext(t.Context(), "INSERT INTO migrations (number, name) VALUES ($1, $2)", 2, "duplicate"); err == nil {
		t.Error("v2 insert of an applied number succeeded, want a unique violation")
	}

	if _, err := db.ExecContext(t.Context(), "DELETE FROM migrations WHERE number >= $1", 2147483645); err != nil {
		t.Fatalf("v2 delete: %v", err)
	}

	check(t, "v2 latest number after delete", queryString(t, db, "SELECT number FROM migrations ORDER BY number DESC LIMIT 1"), "1000")

	// Put back the rows v2's delete removed, so the caller still holds them all.
	exec(t, db, `INSERT INTO migrations (number, name, created_at) VALUES
		(2147483647, repeat('n', 255), '2020-05-06 07:08:09.123456+00'::timestamptz),
		(2147483646, 'limit minus one', '2020-05-06 07:08:09.123456+00'::timestamptz)`)
	check(t, "rows after v2 statements", v2Rows(t, db), rows)
}

func TestDowngradeToV2ScriptEmptyLedger(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(downgradeScript)
	if err != nil {
		t.Fatal(err)
	}

	eachDriver(t, func(t *testing.T, _, _ string, db *sql.DB) {
		if err := newMigrator(t, db, nil, migrate.Config{}).Up(t.Context()); err != nil {
			t.Fatal(err)
		}

		if err := runScript(t, db, string(content)); err != nil {
			t.Fatalf("downgrade: %v", err)
		}

		// v2.3.0's insert, from its repository.go.
		if _, err := db.ExecContext(t.Context(), "INSERT INTO migrations (number, name) VALUES ($1, $2)", 1, "first"); err != nil {
			t.Fatalf("v2 insert: %v", err)
		}

		check(t, "first id", queryString(t, db, "SELECT id::text FROM migrations WHERE number = 1"), "1")

		// A second run finds a v2 table, fails, and changes nothing.
		if err := runScript(t, db, string(content)); err == nil {
			t.Error("downgrade of a v2 ledger succeeded, want an error")
		}

		check(t, "rows after second run", v2Rows(t, db)[:8], "1|first|")
	})
}
