package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Executor runs SQL. Migrations receive a *sql.Tx, or a *sql.Conn when NoTx is
// set. It deliberately has no Commit or Rollback: the Migrator owns the
// transaction so the ledger row commits together with the schema change.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Func is one direction of a migration.
type Func func(ctx context.Context, db Executor) error

// Migration is one versioned schema change.
type Migration struct {
	// Version orders migrations. It must be positive and unique. Sequential
	// numbers and timestamps such as 20261003120000 both work.
	Version int64

	// Name describes the change for logs and the ledger.
	Name string

	// Up applies the change. It is required.
	Up Func

	// Down reverts the change. Leave it nil for an irreversible migration;
	// Migrator.Down then refuses to roll back through it.
	Down Func

	// NoTx runs Up and Down outside a transaction, for statements such as
	// CREATE INDEX CONCURRENTLY. Keep each NoTx migration to one statement:
	// a failure leaves the version dirty until Migrator.Resolve is called.
	NoTx bool
}

// SQL returns a Func that executes query without arguments. Without arguments,
// both lib/pq and pgx accept several statements separated by semicolons.
func SQL(query string) Func {
	return func(ctx context.Context, db Executor) error {
		_, err := db.ExecContext(ctx, query)

		return err
	}
}

// Validate reports every problem in migrations: non-positive or duplicate
// versions, empty names, and missing Up functions. New calls it; call it
// directly to check migrations in CI without a database.
func Validate(migrations []Migration) error {
	var errs []error

	seen := make(map[int64]string, len(migrations))

	for _, mig := range migrations {
		invalid := func(reason string) {
			errs = append(errs, fmt.Errorf("%w: version %d (%q): %s", ErrInvalidMigration, mig.Version, mig.Name, reason))
		}

		if mig.Version <= 0 {
			invalid("version must be positive")
		}

		if mig.Name == "" {
			invalid("name is empty")
		}

		if mig.Up == nil {
			invalid("Up is nil")
		}

		if other, ok := seen[mig.Version]; ok {
			invalid(fmt.Sprintf("duplicates version of %q", other))
		}

		seen[mig.Version] = mig.Name
	}

	return errors.Join(errs...)
}
