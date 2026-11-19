# v3.1.1

## Fixes

- `Config.LockTimeout` now also bounds the waits for a lock on the ledger
  table. Before, the creation of the ledger, the upgrade of a v2 ledger, the
  read of the ledger, and the writes of `Baseline` and `Resolve` ran before
  any `lock_timeout` was set. If another session held a conflicting lock on
  the ledger, `Up`, `UpTo`, `Down`, `Baseline`, and `Resolve` waited without a
  bound. They now return `ErrLockTimeout` after `LockTimeout`, 5 s by default,
  and the transaction that timed out is rolled back. The same holds for the read of the ledger before a
  `NoTx` migration.

  A runner still waits for another runner without a bound: the timeout starts
  after the migration lock is held. `Config.StatementTimeout` still applies
  only inside a migration. `Status` does not change.

# v3.1.0

## New

- `Config.AllowUnknownVersions` lets a run continue when the ledger holds
  applied versions that the code does not contain. Set it for a service that
  migrates at boot, so that an older image starts against a newer ledger, for
  example after an image rollback.
  - `Up` and `UpTo` apply the known pending migrations and leave the unknown
    rows untouched.
  - `Down` leaves unknown versions at or below its target untouched. It still
    returns `ErrUnknownVersion` if one lies above the target, because it has
    no `Down` to run for it.
  - A dirty version, known or unknown, still returns `ErrDirty`.
  - Unknown versions still count as applied history. A pending migration older
    than one returns `ErrOutOfOrder` unless `Config.AllowOutOfOrder` is also
    set.
  - `Baseline`, `Resolve`, and `Status` do not change.
  - Each call that passes unknown versions logs them once at warn level.

  The default is unchanged: an unknown applied version returns
  `ErrUnknownVersion`.
- [docs/downgrade-to-v2.sql](docs/downgrade-to-v2.sql) converts a v3 ledger
  back to the v2 table, to roll back to an image that still runs v2.

## Changed

- The upgrade of a v2 ledger now drops the unique constraint that v2 put on
  `number`. The primary key on `version` already covers it, so an upgraded
  ledger now has the same constraints as a new one. A ledger that v3.0
  upgraded keeps the constraint, normally `migrations_number_key`. It is
  harmless, and this release does not remove it.

# v3.0.0

v3 is a rewrite that fixes how v2 applied, recorded, and rolled back
migrations. The module path is `github.com/lawzava/go-pg-migrate/v3`.

## Fixes

Each item below was reproduced against v2 on PostgreSQL 14 and has a
regression test.

- A migration and its ledger row now commit in one transaction. In v2, a
  failed ledger write after a successful migration left the schema changed but
  unrecorded, and the next run failed.
- An advisory lock serializes runners. In v2, concurrent runners raced on table
  creation and could apply a data migration twice.
- A panic or commit failure no longer leaks an open transaction or hides the
  original error.
- Versions are `int64` and stored as `BIGINT`, so timestamp versions such as
  `20261003120000` work. Large `uint` values no longer wrap to negative
  numbers.
- History is checked before anything runs. v2 silently skipped a migration
  added below the newest applied version, accepted targets that did not exist,
  ran `Down` for migrations that were never applied, and erased ledger rows for
  versions it did not know.
- A rollback checks every migration on its path first and reverts nothing if
  one lacks `Down`.
- The ledger is schema-qualified, so `search_path` no longer decides which
  table records history.
- `Status` only reads. In v2, asking for the current version with
  `RefreshSchema` set dropped the schema.

## New

- `New(db *sql.DB, migrations []Migration, Config)`. The caller owns the
  connection pool and chooses the driver.
- `Up`, `UpTo`, `Down`, `Status`, `Baseline`, and `Resolve`, all taking a
  `context.Context`.
- `Migration.NoTx` for statements such as `CREATE INDEX CONCURRENTLY`. A failed
  `NoTx` migration, or one that leaves a transaction open, is marked dirty
  until `Resolve` is called. Its connection is closed afterwards rather than
  returned to the pool.
- `Config.LockTimeout` (default 5 s) and `Config.StatementTimeout`.
- `Config.AllowOutOfOrder`, `Config.Schema`, `Config.Table`, and
  `Config.Logger` (`*slog.Logger`). Schema and table names may be at most 63
  bytes, the PostgreSQL identifier limit.
- `migrate.SQL(query)` for plain SQL migrations.
- `migrate.Validate` to check migrations in CI without a database.
- `migratetest.RoundTrip` to check that every `Down` reverses its `Up`.
- Sentinel errors for each failure class, such as `ErrOutOfOrder`,
  `ErrIrreversible`, `ErrDirty`, and `ErrLockTimeout`.
- The root module has no dependencies outside the standard library.
- Go 1.25 or later is required.

## Removed

- `AddMigration` and the global registry.
- `Options`, including `DatabaseURI`, `DB`, `RefreshSchema`, `SchemasToRefresh`,
  `PrintInfoAndExit`, `ForceVersionWithoutMigrations`, and `LogInfo`.
- The `Tx` type. Migrations receive an `Executor`, which has no `Commit` or
  `Rollback`.
- The `github.com/lib/pq` dependency.

## Upgrading from v2

1. Stop every v2 runner before the first v3 run. v2 takes no lock, and v3
   renames ledger columns during the upgrade.
1. Find your ledger. v2 created `migrations` in the first schema on the
   connection's `search_path`, usually `public`. v3 looks only in
   `Config.Schema` (default `public`). If v3 finds no ledger, it creates an
   empty one and tries to run every migration again. Run `Status` first: if
   every version shows `pending`, set `Config.Schema` to the schema that holds
   the v2 table.
1. Replace `init()` calls to `migrate.AddMigration` with one slice:

   ```go
   // v2
   migrate.AddMigration(&migrate.Migration{
   	Name: "create users", Number: 1,
   	Up: func(tx migrate.Tx) error { _, err := tx.Exec(`CREATE TABLE users (id int)`); return err },
   })

   // v3
   var migrations = []migrate.Migration{{
   	Version: 1, Name: "create users",
   	Up: migrate.SQL(`CREATE TABLE users (id int)`),
   }}
   ```

   Keep every version number. The ledger matches migrations by version.
1. Open the database yourself with a driver, and pass it to `New`:

   ```go
   import _ "github.com/lib/pq" // or github.com/jackc/pgx/v5/stdlib

   db, err := sql.Open("postgres", databaseURI)
   m, err := migrate.New(db, migrations, migrate.Config{})
   ```

1. Map the old options:

   | v2 | v3 |
   |---|---|
   | `VersionNumberToApply: 0` | `Up(ctx)` |
   | `VersionNumberToApply: n` above the current version | `UpTo(ctx, n)` |
   | `VersionNumberToApply: n` below the current version | `Down(ctx, n)` |
   | `DatabaseURI` or `DB` (v2.3.0) | `New(db, ...)`; v3 never opens or closes the pool |
   | `PrintInfoAndExit` | `Status(ctx)` |
   | `ForceVersionWithoutMigrations` | `Baseline(ctx, n)` on an empty ledger |
   | `RefreshSchema`, `SchemasToRefresh` | `DROP SCHEMA` in your test setup |
   | `LogInfo` | `Config.Logger` |

1. The first `Up`, `Down`, `Baseline`, or `Resolve` call upgrades the v2 table
   in place and keeps every row:
   - `number INTEGER` becomes `version BIGINT`.
   - `created_at TIMESTAMP` becomes `applied_at TIMESTAMPTZ`, read in the
     session time zone.
   - `name` becomes `TEXT`.
   - `id` is dropped, and a `dirty` column is added.
   - Since v3.1.0, the unique constraint on `number` is dropped. The primary
     key on `version` replaces it.

   `Status` reads a v2 table without changing it. v2 cannot read the upgraded
   table. To roll back to a v2 image, convert the table back with
   [docs/downgrade-to-v2.sql](docs/downgrade-to-v2.sql). Stop every v3 runner
   first. The script needs a ledger with no dirty rows, versions from 1 to
   2147483647, and names of at most 255 characters. It checks these and
   changes nothing if one fails.

## Behavior changes to check

- A database with applied versions that the code does not contain now returns
  `ErrUnknownVersion`. v2 silently did nothing. Run migrations as a deploy
  step before new code starts, so an older release never meets a newer ledger.
  A service that migrates at boot can set `Config.AllowUnknownVersions`
  (v3.1.0) instead: `Up` then applies what it knows and leaves the unknown
  versions alone.
- A pending migration older than the newest applied one returns
  `ErrOutOfOrder` unless `Config.AllowOutOfOrder` is set.
- Migrations wait at most 5 s for a table lock by default. Raise
  `Config.LockTimeout`, or set it negative to disable it. The value is set for
  each migration, so it overrides a `lock_timeout` set on the database or
  role.
- `Version` must be positive and `Up` is required.
