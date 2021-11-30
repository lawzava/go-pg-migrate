package migrate

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Tx struct {
	*sql.Tx
}

// Migration defines a single version of a migration to run.
type Migration struct {
	Name   string
	Number uint

	Up   func(tx Tx) error
	Down func(tx Tx) error
}

// nolint:gochecknoglobals // allow global var as it's short-lived
var migrations []*Migration

type migration struct {
	ID        uint
	CreatedAt time.Time
	Name      string
	Number    uint

	Forwards  func(tx Tx) error `pg:"-"`
	Backwards func(tx Tx) error `pg:"-"`
}

var (
	errDuplicateMigrationVersion  = errors.New("duplicate migration version is not allowed")
	errMigrationIsMissing         = errors.New("migration is missing")
	errMigrationNameCannotBeEmpty = errors.New("migration name cannot be empty")
)

func AddMigration(m *Migration) {
	migrations = append(migrations, m)
}

func validateMigrations(migrations []*Migration) error {
	for migrationIdx := range migrations {
		for migrationSecondaryIdx := range migrations {
			if migrationIdx != migrationSecondaryIdx &&
				migrations[migrationIdx].Number == migrations[migrationSecondaryIdx].Number {
				return fmt.Errorf("%s (%d) and %s (%d) have duplicate numbers: %w",
					migrations[migrationIdx].Name, migrations[migrationIdx].Number,
					migrations[migrationSecondaryIdx].Name, migrations[migrationSecondaryIdx].Number,
					errDuplicateMigrationVersion)
			}
		}

		if migrations[migrationIdx].Name == "" {
			return fmt.Errorf("%s (%d) name cannot be empty: %w",
				migrations[migrationIdx].Name, migrations[migrationIdx].Number,
				errMigrationNameCannotBeEmpty,
			)
		}

		if migrations[migrationIdx].Up == nil && migrations[migrationIdx].Down == nil {
			return fmt.Errorf("%s (%d) at least one migration specification is required: %w",
				migrations[migrationIdx].Name, migrations[migrationIdx].Number,
				errMigrationIsMissing,
			)
		}
	}

	return nil
}

func mapMigrations(rawMigrations []*Migration) []*migration {
	migrations := make([]*migration, len(rawMigrations))

	for migrationIdx := range rawMigrations {
		// nolint:exhaustivestruct // ID & created_at are not used
		migrations[migrationIdx] = &migration{
			Name:      rawMigrations[migrationIdx].Name,
			Number:    rawMigrations[migrationIdx].Number,
			Forwards:  rawMigrations[migrationIdx].Up,
			Backwards: rawMigrations[migrationIdx].Down,
		}
	}

	return migrations
}
