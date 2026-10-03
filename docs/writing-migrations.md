# Writing migrations

These rules keep migrations safe to run against a live database while old and
new application versions overlap during a deploy.

## History is append-only

- Add a migration to the end of the slice. Never edit, renumber, or delete one
  that has run anywhere outside your machine. The ledger matches by version,
  so an edited migration silently diverges between environments.
- To undo a shipped change, add a new forward migration.
- Pick one versioning scheme per project:
  - Sequential numbers (`1, 2, 3`) are easy to read. Two branches that both add
    version 4 fail `Validate` with a duplicate, so the second branch renumbers
    before merging.
  - Timestamps (`20261003120000`) rarely collide, but a branch merged after a
    newer one is deployed creates an out-of-order version. `Up` returns
    `ErrOutOfOrder` unless `Config.AllowOutOfOrder` is set.

## One change per migration

- Each migration runs in its own transaction, except `NoTx` migrations. Keep
  it to one logical change so that a failure is easy to read and a rollback is
  precise.
- Use `migrate.SQL` for plain SQL. It sends the text without arguments, so it
  may hold several statements. Anything that passes arguments to `ExecContext`
  must be a single statement.
- Schema-qualify names, or use `SET LOCAL search_path` inside the migration. A
  plain `SET` would outlive the migration on a pooled connection.
- Never call `COMMIT`, `ROLLBACK`, or `BEGIN` in migration SQL. The Migrator
  owns the transaction, and that is what keeps the ledger row consistent.

## Down is optional and exact

- Set `Down` only when it exactly reverses `Up`. `migratetest.RoundTrip`
  checks this.
- Leave `Down` nil when reversal would lose data or cannot be done. `Down`
  then stops with `ErrIrreversible` before reverting anything.
- Recreating a dropped column does not bring its data back. In production,
  prefer a new forward migration to `Down`.

## Locks and zero downtime

Most `ALTER TABLE` forms need an `ACCESS EXCLUSIVE` lock. While one waits for
that lock, every later query on the table queues behind it. `Config.LockTimeout` (default 5 s) makes the migration
fail instead of stalling traffic; rerun it when the table is quieter.

| Operation | Problem | Safer pattern |
|---|---|---|
| `CREATE INDEX` | Blocks writes for the whole build. | `CREATE INDEX CONCURRENTLY` in a `NoTx` migration. |
| `ADD COLUMN ... DEFAULT` with a volatile default such as `clock_timestamp()` or `gen_random_uuid()` | Rewrites the table under an exclusive lock. | Add the column without a default, then backfill in batches. Constant and stable defaults such as `now()` are instant since PostgreSQL 11. |
| `ALTER COLUMN ... TYPE` | Usually rewrites the table. | Add a new column, backfill, switch reads, drop the old column later. |
| `SET NOT NULL` | Scans the table under an exclusive lock. | `ADD CONSTRAINT ... CHECK (col IS NOT NULL) NOT VALID`, then `VALIDATE CONSTRAINT`, then `SET NOT NULL`. PostgreSQL 12 and later skip the scan when a valid check exists. |
| `ADD FOREIGN KEY` or `ADD CHECK` | Validates every row while holding a lock that blocks writes. | Add it `NOT VALID`, then `VALIDATE CONSTRAINT` in a later migration. Validation takes a weaker lock that allows reads and writes. |
| `RENAME` a column or table | Breaks the application version still running. | Expand and contract: add the new name, write both, move reads, drop the old name in a later release. |
| `DROP COLUMN` | Breaks the application version still running. | Stop using the column in a release first, then drop it. |
| Large `UPDATE` backfill | One long transaction that holds row locks. | Backfill in batches from application code or a job, not in a migration. |

## NoTx migrations

- Use `NoTx: true` only for statements that cannot run in a transaction, such
  as `CREATE INDEX CONCURRENTLY` and `DROP INDEX CONCURRENTLY`.
- Keep a `NoTx` migration to one statement. Without a transaction, a failure
  part way through cannot be rolled back.
- A failed `NoTx` migration leaves its version `dirty`, and `Up` and `Down`
  return `ErrDirty` until you resolve it. Check the database, then call
  `Resolve(ctx, version, applied)`. A failed `CREATE INDEX CONCURRENTLY` leaves
  an `INVALID` index: drop it and resolve with `applied=false`.
- `NoTx` migrations need a direct connection or session pooling, not
  PgBouncer in transaction mode.

## Checks before merging

1. `migrate.Validate(migrations)` in a unit test catches duplicate or
   non-positive versions, empty names, and missing `Up`.
2. `migratetest.RoundTrip` against an empty database applies each migration,
   reverts it, and applies it again, comparing the schema each time.
3. Optionally lint the SQL with [squawk](https://squawkhq.com), which flags the
   lock-heavy patterns in the table above.
4. `Status` on a copy of production shows exactly which versions will run.
