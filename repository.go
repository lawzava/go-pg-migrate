package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/lib/pq" // postgres driver
)

type repository interface {
	GetLatestMigrationNumber() (uint, error)
	ApplyMigration(txFunc func(Tx) error) error
	InsertMigration(m *migration) error
	RemoveMigrationsAfter(number uint) error
	EnsureMigrationTable() error
	DropSchema(schemaName string) error
}

type repo struct {
	db *sql.DB
}

func newRepo(databaseURI string) (*repo, error) {
	db, err := sql.Open("postgres", databaseURI)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &repo{db}, nil
}

// GetLatestMigrationNumber returns 0,nil if not found.
func (r *repo) GetLatestMigrationNumber() (uint, error) {
	var latestMigrationNumber uint

	const query = "SELECT number FROM migrations ORDER BY number DESC LIMIT 1"

	err := r.db.QueryRowContext(context.TODO(), query).
		Scan(&latestMigrationNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}

		return 0, fmt.Errorf("failed to get latest migration number: %w", err)
	}

	return latestMigrationNumber, nil
}

func (r *repo) ApplyMigration(txFunc func(Tx) error) error {
	dbTransaction, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}

	if err = txFunc(Tx{dbTransaction}); err != nil {
		if rollbackErr := dbTransaction.Rollback(); rollbackErr != nil {
			return fmt.Errorf("failed to rollback after failed transaction: %w", rollbackErr)
		}

		return fmt.Errorf("failed to apply the migration (rolled back successfully though): %w", err)
	}

	if err = dbTransaction.Commit(); err != nil {
		if rollbackErr := dbTransaction.Rollback(); rollbackErr != nil {
			return fmt.Errorf("failed to rollback after failed commit: %w", rollbackErr)
		}

		return fmt.Errorf("failed to commit the Transaction: %w", err)
	}

	return nil
}

func (r *repo) InsertMigration(m *migration) error {
	const query = "INSERT INTO migrations (number, name) VALUES ($1, $2)"

	_, err := r.db.ExecContext(context.TODO(), query, m.Number, m.Name)
	if err != nil {
		return fmt.Errorf("failed to create migration record: %w", err)
	}

	return nil
}

func (r *repo) RemoveMigrationsAfter(number uint) error {
	const query = "DELETE FROM migrations WHERE number >= $1"

	_, err := r.db.ExecContext(context.TODO(), query, number)
	if err != nil {
		return fmt.Errorf("failed to delete migrations: %w", err)
	}

	return nil
}

func (r *repo) EnsureMigrationTable() error {
	const query = `
		CREATE TABLE IF NOT EXISTS migrations (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			number INTEGER NOT NULL UNIQUE,
			name VARCHAR(255) NOT NULL
		)
	`

	_, err := r.db.ExecContext(context.TODO(), query)
	if err != nil {
		return fmt.Errorf("failed to ensure migration table: %w", err)
	}

	return nil
}

func (r *repo) DropSchema(schemaName string) error {
	_, err := r.db.ExecContext(context.TODO(),
		fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE; CREATE SCHEMA IF NOT EXISTS %q;`,
			schemaName, schemaName))
	if err != nil {
		return fmt.Errorf("failed to drop schema: %w", err)
	}

	return nil
}
