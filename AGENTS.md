# Contributing to go-pg-migrate

## Layout

- Root module `github.com/lawzava/go-pg-migrate/v3`: the library and
  `migratetest`. It must import only the standard library.
- `integration/`: a separate module with the PostgreSQL integration suite,
  the `pgtest` harness, and the example CLI. Drivers and test dependencies live
  here.

## Commands

```sh
go test -race ./...                      # unit tests, no database
go -C integration test -race ./...       # starts embedded PostgreSQL 18
PGM_TEST_PG_VERSION=14 go -C integration test ./...
PGM_TEST_DSN=postgres://... go -C integration test ./...   # existing server
golangci-lint run ./...
(cd integration && golangci-lint run --config ../.golangci.yml ./...)
```

Embedded PostgreSQL writes a socket lock under `/tmp` and listens on
localhost. Some sandboxes block both.

## Invariants

- A transactional migration, its ledger insert or delete, and the advisory
  lock share one transaction. Do not move ledger writes outside it.
- Every plan is computed under the lock from the current ledger, and an invalid
  plan fails before any migration runs.
- `Status` never writes.
- A `NoTx` connection is always discarded after use. A migration can leave
  session state, such as `SET ROLE`, temporary tables, or an open transaction,
  that no reset clears.
- The root `go.mod` has no `require` lines. CI checks this.

## Changes

- Behavior changes need an integration test that fails without the change.
  Run it with both drivers; `eachDriver` does this.
- Keep the exported API small. Add an option only for a concrete need.
- Update `CHANGELOG.md` for user-visible changes.
