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

func validateMigrations(m []*Migration) error {
	for i := range m {
		for j := range m {
			if i != j && m[i].Number == m[j].Number {
				return fmt.Errorf("%s (%d) and %s (%d) have duplicate numbers: %w",
					m[i].Name, m[i].Number,
					m[j].Name, m[j].Number,
					errDuplicateMigrationVersion)
			}
		}

		if m[i].Name == "" {
			return fmt.Errorf("%s (%d) name cannot be empty: %w",
				m[i].Name, m[i].Number,
				errMigrationNameCannotBeEmpty,
			)
		}

		if m[i].Up == nil && m[i].Down == nil {
			return fmt.Errorf("%s (%d) at least one migration specification is required: %w",
				m[i].Name, m[i].Number,
				errMigrationIsMissing,
			)
		}
	}

	return nil
}

func mapMigrations(m []*Migration) []*migration {
	migrations := make([]*migration, len(m))

	for i := range m {
		// nolint:exhaustivestruct // ID & created_at are filled by go-pg
		migrations[i] = &migration{
			Name:      m[i].Name,
			Number:    m[i].Number,
			Forwards:  m[i].Up,
			Backwards: m[i].Down,
		}
	}

	return migrations
}
