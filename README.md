![Golang](https://github.com/lawzava/go-pg-migrate/actions/workflows/golang.yml/badge.svg?branch=main)
[![Version](https://img.shields.io/github/v/release/lawzava/go-pg-migrate)](https://github.com/lawzava/go-pg-migrate/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/lawzava/go-pg-migrate/v3)](https://goreportcard.com/report/github.com/lawzava/go-pg-migrate/v3)
[![Coverage Status](https://coveralls.io/repos/github/lawzava/go-pg-migrate/badge.svg?branch=main)](https://coveralls.io/github/lawzava/go-pg-migrate?branch=main)
[![Go Reference](https://pkg.go.dev/badge/github.com/lawzava/go-pg-migrate/v3.svg)](https://pkg.go.dev/github.com/lawzava/go-pg-migrate/v3)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://awesome-go.com)

# go-pg-migrate

PostgreSQL schema migrations for Go, with no dependencies outside the
standard library.

- Each migration commits in the same transaction as its ledger row. A failed
  migration leaves neither a half-applied change nor an unrecorded one. The
  exception is `NoTx` migrations, below.
- An advisory lock lets any number of replicas run `Up` at startup. Each
  migration runs once.
- History is checked before anything runs. Unknown applied versions, versions
  added below the applied history, and rollbacks through a migration without
  `Down` all fail up front.
- A default 5 s `lock_timeout` makes a blocked `ALTER TABLE` fail instead of
  stalling every query behind it.
- `NoTx` migrations support `CREATE INDEX CONCURRENTLY`, and an interrupted one
  is marked dirty instead of being forgotten.
- Works with any `database/sql` driver. Tested with
  [pgx](https://github.com/jackc/pgx) and [lib/pq](https://github.com/lib/pq)
  on PostgreSQL 14 and 18.

Requires Go 1.25 or later.

```
go get github.com/lawzava/go-pg-migrate/v3
```

## Usage

```go
import (
	"context"
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
	migrate "github.com/lawzava/go-pg-migrate/v3"
)

var migrations = []migrate.Migration{
	{
		Version: 1,
		Name:    "create users",
		Up:      migrate.SQL(`CREATE TABLE users (id BIGINT PRIMARY KEY, name TEXT NOT NULL)`),
		Down:    migrate.SQL(`DROP TABLE users`),
	},
	{
		Version: 2,
		Name:    "index users.name",
		Up:      migrate.SQL(`CREATE INDEX CONCURRENTLY users_name ON users (name)`),
		Down:    migrate.SQL(`DROP INDEX CONCURRENTLY users_name`),
		NoTx:    true,
	},
}

func migrateDB(ctx context.Context, db *sql.DB) error {
	m, err := migrate.New(db, migrations, migrate.Config{})
	if err != nil {
		return err
	}

	return m.Up(ctx)
}
```

`Up` and `Down` can be any `func(ctx context.Context, db migrate.Executor) error`,
so a migration can also transform data in Go.

| Method | Effect |
|---|---|
| `Up(ctx)` | Apply every pending migration. |
| `UpTo(ctx, v)` | Apply pending migrations up to `v`. Never reverts. |
| `Down(ctx, v)` | Revert applied migrations above `v`. `0` reverts all. |
| `Status(ctx)` | List versions as applied, pending, dirty, or unknown. Read-only. |
| `Baseline(ctx, v)` | Record versions up to `v` as applied without running them, to adopt an existing database. |
| `Resolve(ctx, v, applied)` | Clear a dirty version after inspecting the database. |

`Config` sets the ledger location (default `public.migrations`), `LockTimeout`,
`StatementTimeout`, `AllowOutOfOrder`, and a `*slog.Logger`. The zero value
works.

### Connection poolers

Transactional migrations work through PgBouncer in transaction mode. `NoTx`
migrations hold a session-level lock, so they need a direct connection or
session pooling.

## Writing migrations

Read [docs/writing-migrations.md](docs/writing-migrations.md) for the rules:
append-only history, zero-downtime patterns, and the checks to run.
[docs/agents.md](docs/agents.md) is a block to paste into your project's
`AGENTS.md` or `CLAUDE.md` so coding agents follow the same rules.

To test that every `Down` reverses its `Up`, run
`migratetest.RoundTrip` against an empty database:

```go
func TestMigrations(t *testing.T) {
	migratetest.RoundTrip(t, emptyTestDB(t), migrations, migrate.Config{})
}
```

## Example

[integration/example](integration/example) is a small CLI with `up`, `up-to`,
`down`, `status` (JSON), `baseline`, and `resolve` commands.

## Upgrading from v2

See [CHANGELOG.md](CHANGELOG.md#upgrading-from-v2). The v2 ledger table is
upgraded in place on the first v3 run.
