// Package migratetest checks that migrations reverse cleanly.
package migratetest

import (
	"cmp"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	migrate "github.com/lawzava/go-pg-migrate/v3"
)

// RoundTrip applies migrations one at a time against an empty database. After
// each reversible migration it runs Down and checks that the schema matches
// the state before Up, then runs Up again and checks that the schema matches
// the first Up. Irreversible migrations are applied but not reverted.
//
// The schema snapshot covers schemas, relations, columns with their order,
// identity, and defaults, view and function definitions, indexes,
// constraints, triggers, and enum types. It does not compare data.
func RoundTrip(tb testing.TB, db *sql.DB, migrations []migrate.Migration, config migrate.Config) {
	tb.Helper()

	migrator, err := migrate.New(db, migrations, config)
	if err != nil {
		tb.Fatalf("migratetest: %v", err)
	}

	status, err := migrator.Status(tb.Context())
	if err != nil {
		tb.Fatalf("migratetest: status: %v", err)
	}

	for _, entry := range status {
		if entry.State != migrate.StatePending {
			tb.Fatalf("migratetest: database must be empty; version %d is %s", entry.Version, entry.State)
		}
	}

	run := roundTrip{
		tb:       tb,
		db:       db,
		migrator: migrator,
		schema:   cmp.Or(config.Schema, "public"),
		table:    cmp.Or(config.Table, "migrations"),
	}

	sorted := slices.SortedFunc(slices.Values(migrations), func(a, b migrate.Migration) int {
		return cmp.Compare(a.Version, b.Version)
	})

	before := run.snapshot()

	var previous int64

	for _, mig := range sorted {
		if err := migrator.UpTo(tb.Context(), mig.Version); err != nil {
			tb.Fatalf("migratetest: up %d: %v", mig.Version, err)
		}

		after := run.snapshot()

		if mig.Down != nil && !run.reverse(mig, previous, before, after) {
			return
		}

		before, previous = after, mig.Version
	}
}

type roundTrip struct {
	tb       testing.TB
	db       *sql.DB
	migrator *migrate.Migrator
	schema   string
	table    string
}

// reverse runs Down then Up for mig and compares each result with the
// expected snapshot. It returns false when the database is left in an
// unknown state.
func (r roundTrip) reverse(mig migrate.Migration, previous int64, before, after []string) bool {
	r.tb.Helper()

	if err := r.migrator.Down(r.tb.Context(), previous); err != nil {
		r.tb.Fatalf("migratetest: down %d: %v", mig.Version, err)
	}

	if diff := diffLines(before, r.snapshot()); diff != "" {
		// Up would likely fail on the leftovers, hiding this diff.
		r.tb.Errorf("migratetest: version %d (%s): Down did not restore the schema:\n%s", mig.Version, mig.Name, diff)

		return false
	}

	if err := r.migrator.UpTo(r.tb.Context(), mig.Version); err != nil {
		r.tb.Fatalf("migratetest: up %d after down: %v", mig.Version, err)
	}

	if diff := diffLines(after, r.snapshot()); diff != "" {
		r.tb.Errorf("migratetest: version %d (%s): Up after Down produced a different schema:\n%s",
			mig.Version, mig.Name, diff)
	}

	return true
}

// snapshotQuery describes user-defined schema objects as sorted text lines.
// $1 and $2 name the ledger table. The ledger, its indexes, and its schema
// entry are excluded, because the Migrator creates them.
const snapshotQuery = `
WITH ledger AS (
	SELECT to_regclass(format('%I.%I', $1::text, $2::text)) AS oid
),
user_ns AS (
	SELECT oid, nspname FROM pg_namespace
	WHERE nspname NOT IN ('pg_catalog', 'information_schema')
		AND nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp%'
),
user_rel AS (
	SELECT c.oid, c.relname, c.relkind, n.nspname FROM pg_class c
	JOIN user_ns n ON n.oid = c.relnamespace
	WHERE c.oid IS DISTINCT FROM (SELECT oid FROM ledger)
		AND NOT EXISTS (
			SELECT 1 FROM pg_index i WHERE i.indexrelid = c.oid AND i.indrelid = (SELECT oid FROM ledger)
		)
)
SELECT line FROM (
	SELECT format('schema %I', nspname) FROM user_ns WHERE nspname <> $1::text
	UNION ALL
	SELECT format('relation %I.%I kind=%s', nspname, relname, relkind) FROM user_rel WHERE relkind <> 'i'
	UNION ALL
	SELECT format('column %I.%I.%I position=%s %s notnull=%s identity=%s generated=%s default=%s',
		r.nspname, r.relname, a.attname,
		row_number() OVER (PARTITION BY a.attrelid ORDER BY a.attnum),
		format_type(a.atttypid, a.atttypmod), a.attnotnull, a.attidentity, a.attgenerated,
		coalesce(pg_get_expr(d.adbin, d.adrelid), ''))
	FROM user_rel r
	JOIN pg_attribute a ON a.attrelid = r.oid AND a.attnum > 0 AND NOT a.attisdropped
	LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	WHERE r.relkind IN ('r', 'p', 'v', 'm', 'f')
	UNION ALL
	SELECT format('view %I.%I %s', nspname, relname, pg_get_viewdef(oid)) FROM user_rel WHERE relkind IN ('v', 'm')
	UNION ALL
	SELECT format('index %s', pg_get_indexdef(r.oid)) FROM user_rel r WHERE r.relkind = 'i'
	UNION ALL
	SELECT format('constraint %I.%I %I %s', r.nspname, r.relname, c.conname, pg_get_constraintdef(c.oid))
	FROM pg_constraint c JOIN user_rel r ON r.oid = c.conrelid
	UNION ALL
	SELECT format('trigger %s', pg_get_triggerdef(t.oid))
	FROM pg_trigger t JOIN user_rel r ON r.oid = t.tgrelid WHERE NOT t.tgisinternal
	UNION ALL
	SELECT format('enum %s %s', t.oid::regtype, string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder))
	FROM pg_type t JOIN user_ns n ON n.oid = t.typnamespace JOIN pg_enum e ON e.enumtypid = t.oid
	GROUP BY t.oid
	UNION ALL
	-- pg_get_functiondef rejects aggregates, so those are listed by signature.
	SELECT format('function %s %s', p.oid::regprocedure,
		CASE WHEN p.prokind IN ('f', 'p') THEN pg_get_functiondef(p.oid) ELSE p.prokind::text END)
	FROM pg_proc p JOIN user_ns n ON n.oid = p.pronamespace
) AS objects(line)
ORDER BY line`

func (r roundTrip) snapshot() []string {
	r.tb.Helper()

	rows, err := r.db.QueryContext(r.tb.Context(), snapshotQuery, r.schema, r.table)
	if err != nil {
		r.tb.Fatalf("migratetest: snapshot: %v", err)
	}
	defer rows.Close()

	var lines []string

	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			r.tb.Fatalf("migratetest: snapshot: %v", err)
		}

		lines = append(lines, line)
	}

	if err := rows.Err(); err != nil {
		r.tb.Fatalf("migratetest: snapshot: %v", err)
	}

	return lines
}

func diffLines(want, got []string) string {
	var diff strings.Builder

	for _, line := range want {
		if !slices.Contains(got, line) {
			fmt.Fprintf(&diff, "- %s\n", line)
		}
	}

	for _, line := range got {
		if !slices.Contains(want, line) {
			fmt.Fprintf(&diff, "+ %s\n", line)
		}
	}

	return diff.String()
}
