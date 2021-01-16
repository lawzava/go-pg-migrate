package migrate

import (
	"errors"
	"fmt"

	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
)

type repository interface {
	GetLatestMigrationNumber() (uint, error)
	ApplyMigration(txFunc func(*pg.Tx) error) error
	InsertMigration(m *migration) error
	RemoveMigrationsAfter(number uint) error
	EnsureMigrationTable() error
	DropDatabase() error
}

type repo struct {
	db *pg.DB
}

func newRepo(db *pg.DB) repository {
	return &repo{db}
}

// GetLatestMigration returns 0,nil if not found.
func (r *repo) GetLatestMigrationNumber() (uint, error) {
	var m migration

	err := r.db.Model(&m).Order("number DESC").First()
	if err != nil {
		if errors.Is(err, pg.ErrNoRows) {
			return 0, nil
		}

		return 0, fmt.Errorf("querying for latest migration: %w", err)
	}

	return m.Number, nil
}

func (r *repo) ApplyMigration(txFunc func(*pg.Tx) error) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}

	if err = txFunc(tx); err != nil {
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

	if err = tx.Close(); err != nil {
		return fmt.Errorf("failed to close transaction: %w", err)
	}

	return nil
}

func (r *repo) InsertMigration(m *migration) error {
	if _, err := r.db.Model(m).Insert(); err != nil {
		return fmt.Errorf("failed to create migration record: %w", err)
	}

	return nil
}

func (r *repo) RemoveMigrationsAfter(number uint) error {
	if _, err := r.db.Model(&migration{}).
		Where("number >= ?", number).
		Delete(); err != nil {
		return fmt.Errorf("failed to create migration record: %w", err)
	}

	return nil
}

func (r *repo) EnsureMigrationTable() error {
	err := r.db.Model(&migration{}).CreateTable(&orm.CreateTableOptions{
		Varchar:       0,
		Temp:          false,
		IfNotExists:   true,
		FKConstraints: false,
	})
	if err != nil {
		return fmt.Errorf("failed to ensure migration table: %w", err)
	}

	return nil
}

func (r *repo) DropDatabase() error {
	_, err := r.db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
	if err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	return nil
}
