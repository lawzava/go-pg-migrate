package migrate

import "errors"

var (
	// ErrInvalidMigration reports a migration that fails Validate.
	ErrInvalidMigration = errors.New("invalid migration")

	// ErrInvalidConfig reports an unusable Config or a nil database.
	ErrInvalidConfig = errors.New("invalid config")

	// ErrDirty reports a NoTx migration that started but did not finish.
	// Inspect the database, then call Migrator.Resolve.
	ErrDirty = errors.New("dirty migration")

	// ErrUnknownVersion reports an applied version that has no migration in code.
	ErrUnknownVersion = errors.New("applied version has no migration")

	// ErrOutOfOrder reports a pending migration older than the newest applied one.
	// Set Config.AllowOutOfOrder to apply it anyway.
	ErrOutOfOrder = errors.New("pending migration is older than applied history")

	// ErrUnknownTarget reports a target version that has no migration.
	ErrUnknownTarget = errors.New("target version has no migration")

	// ErrIrreversible reports a rollback through a migration without Down.
	ErrIrreversible = errors.New("migration has no Down")

	// ErrLedgerNotEmpty reports a Baseline call on a database with applied history.
	ErrLedgerNotEmpty = errors.New("ledger already has applied versions")

	// ErrIncompatibleLedger reports a ledger table with unexpected columns.
	ErrIncompatibleLedger = errors.New("incompatible ledger table")

	// ErrNotDirty reports a Resolve call for a version that is not dirty.
	ErrNotDirty = errors.New("version is not dirty")

	// ErrLockTimeout reports a statement that exceeded Config.LockTimeout.
	ErrLockTimeout = errors.New("lock timeout")
)
