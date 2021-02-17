package migrate

import (
	"errors"
	"fmt"
	"sort"

	"github.com/go-pg/pg/v10"
	"github.com/rs/zerolog/log"
)

var errNoMigrationVersion = errors.New("migration version not found")

// InfoLogger defines info level logger, passes go-sprintf-friendly format & arguments
type InfoLogger func(format string, args ...interface{})

// Options define applied migrations options and behavior.
type Options struct {
	// VersionNumberToApply defines target version for migration actions.
	VersionNumberToApply uint

	// PrintInfoAndExit controls whether the migration should do an early exit after printing out current version info.
	PrintInfoAndExit bool

	// ForceVersionWithoutMigrations allows to force specific migration version without actually applying the migrations.
	ForceVersionWithoutMigrations bool

	// RefreshSchema drops and recreates public schema.
	RefreshSchema bool

	// LogInfo handles info logging
	LogInfo InfoLogger
}

type migrationTask struct {
	migrations []*migration
	repo       repository

	opt Options
}

// Migrate describes migration tasks.
type Migrate interface {
	Migrate() error
}

type migrate struct {
	task *migrationTask
}

// Migrate executes actual migrations based on the specified options.
func (m migrate) Migrate() error {
	return m.task.migrate()
}

// New creates new migration instance.
func New(db *pg.DB, opt Options, migrations ...*Migration) (Migrate, error) {
	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}

	if opt.LogInfo == nil {
		opt.LogInfo = func(format string, args ...interface{}) {
			log.Info().Msgf(format, args...)
		}
	}

	return migrate{
		task: &migrationTask{
			migrations: mapMigrations(migrations),
			repo:       newRepo(db),
			opt:        opt,
		},
	}, nil
}

// migrate applies actual migrations based on the specified options.
func (m migrationTask) migrate() error {
	if m.opt.RefreshSchema {
		if err := m.refreshDatabase(); err != nil {
			return fmt.Errorf("refreshing database: %w", err)
		}
	} else {
		err := m.repo.EnsureMigrationTable()
		if err != nil {
			return fmt.Errorf("failed to automatically migrate migrations table: %w", err)
		}
	}

	if m.opt.ForceVersionWithoutMigrations {
		return m.handleForceVersionWithoutMigrations()
	}

	lastAppliedMigrationNumber, err := m.repo.GetLatestMigrationNumber()
	if err != nil {
		return fmt.Errorf("failed to get the number of the latest migration: %w", err)
	}

	if m.opt.PrintInfoAndExit {
		m.opt.LogInfo("currently applied version: %d", lastAppliedMigrationNumber)

		return nil
	}

	if err := m.applyMigrations(lastAppliedMigrationNumber); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}

func (m migrationTask) handleForceVersionWithoutMigrations() error {
	for _, migration := range m.migrations {
		if migration.Number != m.opt.VersionNumberToApply {
			continue
		}

		if err := m.repo.RemoveMigrationsAfter(migration.Number); err != nil {
			return fmt.Errorf("failed to remove migrations: %w", err)
		}

		if err := m.repo.InsertMigration(migration); err != nil {
			return fmt.Errorf("failed insert migration: %w", err)
		}

		return nil
	}

	return errNoMigrationVersion
}

func (m migrationTask) refreshDatabase() error {
	m.opt.LogInfo("refreshing database")

	err := m.repo.DropDatabase()
	if err != nil {
		return fmt.Errorf("failed to DropDatabase (running with 'refresh' flag): %w", err)
	}

	m.opt.LogInfo("ensuring migrations table is present")

	err = m.repo.EnsureMigrationTable()
	if err != nil {
		return fmt.Errorf("failed to automatically migrate migrations table: %w", err)
	}

	return nil
}

func (m *migrationTask) applyMigrations(lastAppliedMigrationNumber uint) error {
	if len(m.migrations) == 0 {
		m.opt.LogInfo("no migrations to apply.")

		return nil
	}

	if m.opt.VersionNumberToApply == 0 {
		m.opt.VersionNumberToApply = m.getLastMigrationNumber()
	}

	if m.opt.VersionNumberToApply < lastAppliedMigrationNumber {
		return m.applyBackwardMigrations(lastAppliedMigrationNumber)
	}

	return m.applyForwardMigrations(lastAppliedMigrationNumber)
}

func (m *migrationTask) applyBackwardMigrations(lastAppliedMigrationNumber uint) error {
	m.sortMigrationsDesc()

	for _, migration := range m.migrations {
		if migration.Number > lastAppliedMigrationNumber {
			continue
		}

		if migration.Number <= m.opt.VersionNumberToApply {
			break
		}

		m.opt.LogInfo("applying backwards migration %d (%s)", migration.Number, migration.Name)

		if err := m.repo.ApplyMigration(migration.Backwards); err != nil {
			return fmt.Errorf("failed to apply the migration (BackwardMigration): %w", err)
		}

		if err := m.repo.RemoveMigrationsAfter(migration.Number); err != nil {
			return fmt.Errorf("failed to remove migrations: %w", err)
		}
	}

	return nil
}

func (m *migrationTask) applyForwardMigrations(lastAppliedMigrationNumber uint) error {
	m.sortMigrationsAsc()

	for _, migration := range m.migrations {
		if migration.Number <= lastAppliedMigrationNumber {
			continue
		}

		if migration.Number > m.opt.VersionNumberToApply && m.opt.VersionNumberToApply != 0 {
			break
		}

		m.opt.LogInfo("applying forward migration %d (%s)", migration.Number, migration.Name)

		if err := m.repo.ApplyMigration(migration.Forwards); err != nil {
			return fmt.Errorf("failed to apply the migration (ForwardMigration): %w", err)
		}

		if err := m.repo.InsertMigration(migration); err != nil {
			return fmt.Errorf("failed to create migration record: %w", err)
		}
	}

	return nil
}

func (m *migrationTask) sortMigrationsAsc() {
	sort.SliceStable(m.migrations, func(i, j int) bool {
		return m.migrations[i].Number < m.migrations[j].Number
	})
}

func (m *migrationTask) sortMigrationsDesc() {
	sort.SliceStable(m.migrations, func(i, j int) bool {
		return m.migrations[i].Number > m.migrations[j].Number
	})
}

func (m *migrationTask) getLastMigrationNumber() uint {
	var lastNumber uint

	for i := range m.migrations {
		if m.migrations[i].Number > lastNumber {
			lastNumber = m.migrations[i].Number
		}
	}

	return lastNumber
}
