# Agent instructions for go-pg-migrate

Copy the block below into your project's `AGENTS.md` or `CLAUDE.md`. Adjust
the file paths and commands to your project.

```markdown
## Database migrations

Migrations use github.com/lawzava/go-pg-migrate/v3. They live in
`internal/db/migrations.go` as one `[]migrate.Migration` slice.

Before writing a migration:
- Run `go run ./cmd/migrate status` against the development database and read
  the JSON. Every version must be `applied` or `pending`. Stop and report a
  `dirty` or `unknown` version instead of working around it.

When writing a migration:
- Append one migration to the end of the slice with the next version number.
  Never edit, renumber, or delete an existing migration.
- One logical change per migration. Use `migrate.SQL` for plain SQL.
- Never put BEGIN, COMMIT, or ROLLBACK in migration SQL.
- Schema-qualify table names.
- Set `Down` only if it exactly reverses `Up`. Leave it nil if reversal would
  lose data.
- Follow docs/writing-migrations.md in go-pg-migrate for locking rules:
  - `CREATE INDEX CONCURRENTLY` in its own migration with `NoTx: true`.
  - Constraints and foreign keys added `NOT VALID`, validated in a later
    migration.
  - Renames and type changes done as expand and contract across releases.
  - No large backfills inside migrations.

Before finishing:
- Run the migration unit tests: `migrate.Validate` and
  `migratetest.RoundTrip` against a disposable database.
- Report the new version, what it locks, and whether it is reversible.
- Never run a migration against a shared or production database. Never call
  `Baseline` or `Resolve` unless a person asked for that exact call.
```
