package migrate //nolint:testpackage // allow direct tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMigrate(t *testing.T) {
	postgres := prepareDB(t)

	err := postgres.Start()
	require.NoError(t, err)

	defer func() {
		// If this fails to stop properly, the process will be stuck in the system background. Requires a manual kill.
		err = postgres.Stop()
		if err != nil {
			t.Error(err)
		}
	}()

	err = performMigrateWithMigrations(t, Options{})
	require.NoError(t, err, "Migrate Default")

	err = performMigrateWithMigrations(t, Options{RefreshSchema: true})
	require.NoError(t, err, "Refresh Schema")

	err = performMigrateWithMigrations(t, Options{SchemasToRefresh: []string{"public", "test"}})
	require.NoError(t, err, "Refresh Schemas 'public' and 'test'")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 2})
	require.NoError(t, err, "Migrate Down 2")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 1})
	require.NoError(t, err, "Migrate Down 1")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 2})
	require.NoError(t, err, "Migrate Forward 2")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 3})
	require.NoError(t, err, "Migrate Forward 3")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 2, ForceVersionWithoutMigrations: true})
	require.NoError(t, err, "Force Incorrect Version")

	err = performMigrateWithMigrations(t, Options{PrintInfoAndExit: true})
	require.NoError(t, err, "Print Info")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 3, ForceVersionWithoutMigrations: true})
	require.NoError(t, err, "Force Correct Version")

	err = performMigrate(t, Options{})
	require.NoError(t, err, "No Migrations To apply")

	err = performMigrateWithMigrations(t, Options{VersionNumberToApply: 0, ForceVersionWithoutMigrations: true})
	require.ErrorIs(t, err, errNoMigrationVersion, "Force Version Is Missing")
}

func performMigrateWithMigrations(t *testing.T, options Options) error {
	t.Helper()

	migrations = prepareMigrations()

	return performMigrate(t, options)
}

func performMigrate(t *testing.T, options Options) error {
	t.Helper()

	options.DatabaseURI = "postgres://migrate:migrate@localhost:54320/migrate?sslmode=disable"

	migrate, err := New(options)

	require.NoError(t, err)

	closeMigrationDB(t, migrate)

	err = migrate.Migrate()
	if err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}

	return nil
}

//nolint:funlen // allow longer function
func TestMigrateErrors(t *testing.T) {
	t.Parallel()

	repo := new(mockRepository)

	someErr := errors.New("test-err") //nolint:err113 // used for tests only

	repo.On("EnsureMigrationTable").Return(someErr).Once()
	err := performMigrateTaskWithMigrations(t, repo, Options{})
	require.ErrorIs(t, err, someErr, "Error On MigrationTable")

	repo.On("DropSchema", "public").Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{RefreshSchema: true})
	require.ErrorIs(t, err, someErr, "Error On DropSchema")

	repo.On("DropSchema", "public").Return(nil).Once()
	repo.On("EnsureMigrationTable").Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{RefreshSchema: true})
	require.ErrorIs(t, err, someErr, "Error On EnsureMigrationTable After DropSchema")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("RemoveMigrationsAfter", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{ForceVersionWithoutMigrations: true, VersionNumberToApply: 3})
	require.ErrorIs(t, err, someErr, "Error On RemoveMigrationsAfter")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("RemoveMigrationsAfter", mock.Anything).Return(nil).Once()
	repo.On("InsertMigration", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{ForceVersionWithoutMigrations: true, VersionNumberToApply: 3})
	require.ErrorIs(t, err, someErr, "Error On InsertMigration")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(0), someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{})
	require.ErrorIs(t, err, someErr, "Error On GetLatestMigrationNumber")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(3), nil).Once()
	repo.On("ApplyMigration", mock.Anything, mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{VersionNumberToApply: 2})
	require.ErrorIs(t, err, someErr, "Error On BackwardMigration")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(3), nil).Once()
	repo.On("ApplyMigration", mock.Anything).Return(nil).Once()
	repo.On("RemoveMigrationsAfter", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{VersionNumberToApply: 2})
	require.ErrorIs(t, err, someErr, "Error On RemoveMigrationsAfter")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(0), nil).Once()
	repo.On("ApplyMigration", mock.Anything, mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{})
	require.ErrorIs(t, err, someErr, "Error On ForwardMigration")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(0), nil).Once()
	repo.On("ApplyMigration", mock.Anything).Return(nil).Once()
	repo.On("InsertMigration", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{})
	require.ErrorIs(t, err, someErr, "Error On InsertMigration")
}

func performMigrateTaskWithMigrations(t *testing.T, repo repository, options Options) error {
	t.Helper()

	return performMigrateTask(t, repo, options, prepareMigrations())
}

func performMigrateTask(t *testing.T, repo repository, options Options, migrations []*Migration) error {
	t.Helper()

	options.LogInfo = func(format string, args ...any) {
		//nolint:forbidigo // allow in tests
		fmt.Printf(format+"\n", args...)
	}

	task := migrationTask{
		migrations: mapMigrations(migrations),
		repo:       repo,
		opt:        options,
	}

	return task.migrate()
}

func TestNew(t *testing.T) {
	testCases := []struct {
		name        string
		migrations  []*Migration
		expectedErr error
	}{
		{
			name:        "missing migrations",
			migrations:  []*Migration{{Name: "Test Migration", Number: 1, Up: nil, Down: nil}},
			expectedErr: errMigrationIsMissing,
		},
		{
			name: "duplicate migrations",
			migrations: []*Migration{
				{Name: "Test Migration", Number: 1, Up: func(_ Tx) error { return nil }, Down: nil},
				{Name: "Test Migration 2", Number: 1, Up: func(_ Tx) error { return nil }, Down: nil},
			},
			expectedErr: errDuplicateMigrationVersion,
		},
		{
			name: "empty name",
			migrations: []*Migration{
				{Name: "", Number: 1, Up: func(_ Tx) error { return nil }, Down: nil},
			},
			expectedErr: errMigrationNameCannotBeEmpty,
		},
		{
			name: "success",
			migrations: []*Migration{
				{Name: "Test Migration", Number: 1, Up: func(_ Tx) error { return nil }, Down: nil},
				{Name: "Test Migration 2", Number: 2, Up: func(_ Tx) error { return nil }, Down: nil},
			},
			expectedErr: nil,
		},
	}

	var opt Options

	for _, testCase := range testCases {
		migrations = testCase.migrations

		m, err := New(opt)

		require.ErrorIs(t, err, testCase.expectedErr, testCase.name)

		if m != nil {
			closeMigrationDB(t, m)
		}
	}
}

func closeMigrationDB(t *testing.T, m *Migrate) {
	t.Helper()

	repository, ok := m.task.repo.(*repo)
	require.True(t, ok)
	t.Cleanup(func() { assert.NoError(t, repository.db.Close()) })
}

func prepareDB(t *testing.T) *embeddedpostgres.EmbeddedPostgres {
	t.Helper()

	return embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		CachePath(filepath.Join(os.TempDir(), "go-pg-migrate-postgres-cache")).
		RuntimePath(filepath.Join(t.TempDir(), "postgres")).
		Username("migrate").
		Password("migrate").
		Database("migrate").
		Version(embeddedpostgres.V14).
		Port(54320))
}

//nolint:funlen // allow longer function
func prepareMigrations() []*Migration {
	migrations := []*Migration{
		{
			Name:   "Create Users Table",
			Number: 1,
			Up: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
				if err != nil {
					return fmt.Errorf("failed to create users table: %w", err)
				}

				return nil
			},
			Down: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "DROP TABLE users")
				if err != nil {
					return fmt.Errorf("failed to drop users table: %w", err)
				}

				return nil
			},
		},
		{
			Name:   "Add Email For Users",
			Number: 2,
			Up: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "ALTER TABLE users ADD COLUMN email TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add email: %w", err)
				}

				return nil
			},
			Down: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "ALTER TABLE users DROP COLUMN email")
				if err != nil {
					return fmt.Errorf("failed to drop email column for users table: %w", err)
				}

				return nil
			},
		},
		{
			Name:   "Add Address For Users",
			Number: 3,
			Up: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "ALTER TABLE users ADD COLUMN address TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add address: %w", err)
				}

				return nil
			},
			Down: func(tx Tx) error {
				_, err := tx.ExecContext(context.Background(), "ALTER TABLE users DROP COLUMN address")
				if err != nil {
					return fmt.Errorf("failed to drop address column for users table: %w", err)
				}

				return nil
			},
		},
	}

	return migrations
}
