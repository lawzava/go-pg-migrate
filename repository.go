package migrate

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
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
	ctx context.Context
	db  *sql.DB
}

func newRepo(databaseURI string) (repository, error) {
	db, err := sql.Open("postgres", databaseURI)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	return &repo{context.Background(), db}, nil
}

// GetLatestMigrationNumber returns 0,nil if not found.
func (r *repo) GetLatestMigrationNumber() (uint, error) {
	var latestMigrationNumber uint

	err := r.db.QueryRowContext(r.ctx, "SELECT number FROM migrations ORDER BY number DESC LIMIT 1").
		Scan(&latestMigrationNumber)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}

		return 0, fmt.Errorf("failed to get latest migration number: %w", err)
	}

	return latestMigrationNumber, nil
}

func (r *repo) ApplyMigration(txFunc func(Tx) error) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}

	if err = txFunc(Tx{tx}); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("failed to rollback after failed transaction: %w", rollbackErr)
		}

		return fmt.Errorf("failed to apply the migration (rolled back successfully though): %w", err)
	}

	if err = tx.Commit(); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("failed to rollback after failed commit: %w", rollbackErr)
		}

		return fmt.Errorf("failed to commit the Transaction: %w", err)
	}

	return nil
}

func (r *repo) InsertMigration(m *migration) error {
	_, err := r.db.ExecContext(r.ctx,
		"INSERT INTO migrations (number, name) VALUES ($1, $2)",
		m.Number, m.Name)
	if err != nil {
		return fmt.Errorf("failed to create migration record: %w", err)
	}

	return nil
}

// nolint:exhaustivestruct // do not check for go-pg models
func (r *repo) RemoveMigrationsAfter(number uint) error {
	_, err := r.db.ExecContext(r.ctx,
		"DELETE FROM migrations WHERE number >= $1",
		number,
	)
	if err != nil {
		return fmt.Errorf("failed to delete migrations: %w", err)
	}

	return nil
}

// nolint:exhaustivestruct // do not check for go-pg models
func (r *repo) EnsureMigrationTable() error {
	_, err := r.db.ExecContext(r.ctx, `
		CREATE TABLE IF NOT EXISTS migrations (
			id SERIAL PRIMARY KEY,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			number INTEGER NOT NULL UNIQUE,
			name VARCHAR(255) NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to ensure migration table: %w", err)
	}

	return nil
}

func (r *repo) DropSchema(schemaName string) error {
	_, err := r.db.ExecContext(r.ctx,
		fmt.Sprintf(`DROP SCHEMA IF EXISTS %q CASCADE; CREATE SCHEMA IF NOT EXISTS %q;`,
			schemaName, schemaName))
	if err != nil {
		return fmt.Errorf("failed to drop schema: %w", err)
	}

	return nil
}
