package migrate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/go-pg/pg/v10"

	"github.com/stretchr/testify/assert"
)

func TestMigrate(t *testing.T) {
	t.Parallel()

	postgres := prepareDB()

	err := postgres.Start()
	if err != nil {
		t.Error(err)
	}

	defer func() {
		// If this fails to stop properly, the process will be stuck in the system background. Requires a manual kill.
		if err = postgres.Stop(); err != nil {
			t.Error(err)
		}
	}()

	db := preparePG()

	if err = db.Ping(context.Background()); err != nil {
		t.Error(err)
	}

	err = performMigrateWithMigrations(t, db, Options{})
	assert.NoError(t, err, "Migrate Default")

	err = performMigrateWithMigrations(t, db, Options{RefreshSchema: true})
	assert.NoError(t, err, "Refresh Schema")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 2})
	assert.NoError(t, err, "Migrate Backwards 2")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 1})
	assert.NoError(t, err, "Migrate Backwards 1")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 2})
	assert.NoError(t, err, "Migrate Forward 2")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 3})
	assert.NoError(t, err, "Migrate Forward 3")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 2, ForceVersionWithoutMigrations: true})
	assert.NoError(t, err, "Force Incorrect Version")

	err = performMigrateWithMigrations(t, db, Options{PrintInfoAndExit: true})
	assert.NoError(t, err, "Print Info")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 3, ForceVersionWithoutMigrations: true})
	assert.NoError(t, err, "Force Correct Version")

	err = performMigrate(t, db, Options{}, []*Migration{})
	assert.NoError(t, err, "No Migrations To apply")

	err = performMigrateWithMigrations(t, db, Options{VersionNumberToApply: 0, ForceVersionWithoutMigrations: true})
	assert.ErrorIs(t, err, errNoMigrationVersion, "Force Version Is Missing")
}

func performMigrateWithMigrations(t *testing.T, db *pg.DB, options Options) error {
	t.Helper()

	return performMigrate(t, db, options, prepareMigrations())
}

func performMigrate(t *testing.T, db *pg.DB, options Options, migrations []*Migration) error {
	t.Helper()

	migrate, err := New(db, options, migrations...)
	if err != nil {
		t.Error(err)
	}

	return migrate.Migrate()
}

func TestMigrateErrors(t *testing.T) {
	t.Parallel()

	repo := new(mockRepository)

	someErr := errors.New("test-err") // nolint:goerr113 // used for tests only

	repo.On("EnsureMigrationTable").Return(someErr).Once()
	err := performMigrateTaskWithMigrations(t, repo, Options{})
	assert.ErrorIs(t, err, someErr, "Error On MigrationTable")

	repo.On("DropDatabase").Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{RefreshSchema: true})
	assert.ErrorIs(t, err, someErr, "Error On DropDatabase")

	repo.On("DropDatabase").Return(nil).Once()
	repo.On("EnsureMigrationTable").Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{RefreshSchema: true})
	assert.ErrorIs(t, err, someErr, "Error On EnsureMigrationTable After DropDatabase")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("RemoveMigrationsAfter", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{ForceVersionWithoutMigrations: true, VersionNumberToApply: 3})
	assert.ErrorIs(t, err, someErr, "Error On RemoveMigrationsAfter")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("RemoveMigrationsAfter", mock.Anything).Return(nil).Once()
	repo.On("InsertMigration", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{ForceVersionWithoutMigrations: true, VersionNumberToApply: 3})
	assert.ErrorIs(t, err, someErr, "Error On InsertMigration")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(0), someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{})
	assert.ErrorIs(t, err, someErr, "Error On GetLatestMigrationNumber")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(3), nil).Once()
	repo.On("BackwardMigration", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{VersionNumberToApply: 2})
	assert.ErrorIs(t, err, someErr, "Error On BackwardMigration")

	repo.On("EnsureMigrationTable").Return(nil).Once()
	repo.On("GetLatestMigrationNumber").Return(uint(0), nil).Once()
	repo.On("ForwardMigration", mock.Anything).Return(someErr).Once()
	err = performMigrateTaskWithMigrations(t, repo, Options{})
	assert.ErrorIs(t, err, someErr, "Error On ForwardMigration")
}

func performMigrateTaskWithMigrations(t *testing.T, repo repository, options Options) error {
	t.Helper()

	return performMigrateTask(t, repo, options, prepareMigrations())
}

func performMigrateTask(t *testing.T, repo repository, options Options, migrations []*Migration) error {
	t.Helper()

	task := migrationTask{
		migrations: mapMigrations(migrations),
		repo:       repo,
		opt:        options,
	}

	return task.migrate()
}

func TestNew(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		migrations  []*Migration
		expectedErr error
	}{
		{
			name:        "missing migrations",
			migrations:  []*Migration{{Name: "Test Migration", Number: 1, Forwards: nil, Backwards: nil}},
			expectedErr: errMigrationIsMissing,
		},
		{
			name: "duplicate migrations",
			migrations: []*Migration{
				{Name: "Test Migration", Number: 1, Forwards: func(tx *pg.Tx) error { return nil }, Backwards: nil},
				{Name: "Test Migration 2", Number: 1, Forwards: func(tx *pg.Tx) error { return nil }, Backwards: nil},
			},
			expectedErr: errDuplicateMigrationVersion,
		},
		{
			name: "success",
			migrations: []*Migration{
				{Name: "Test Migration", Number: 1, Forwards: func(tx *pg.Tx) error { return nil }, Backwards: nil},
				{Name: "Test Migration 2", Number: 2, Forwards: func(tx *pg.Tx) error { return nil }, Backwards: nil},
			},
			expectedErr: nil,
		},
	}

	var opt Options

	for _, testCase := range testCases {
		_, err := New(nil, opt, testCase.migrations...)

		assert.ErrorIs(t, err, testCase.expectedErr, testCase.name)
	}
}

func prepareDB() *embeddedpostgres.EmbeddedPostgres {
	return embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("migrate").
		Password("migrate").
		Database("migrate").
		Version(embeddedpostgres.V13).
		Port(54320))
}

func preparePG() *pg.DB {
	return pg.Connect(
		&pg.Options{
			User:     "migrate",
			Password: "migrate",
			Database: "migrate",
			Addr:     ":54320",
		})
}

// nolint:funlen // allow longer function
func prepareMigrations() []*Migration {
	return []*Migration{
		{
			Name:   "Create Users Table",
			Number: 1,
			Forwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
				if err != nil {
					return fmt.Errorf("failed to create users table: %w", err)
				}

				return nil
			},
			Backwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("DROP TABLE users")
				if err != nil {
					return fmt.Errorf("failed to drop users table: %w", err)
				}

				return nil
			},
		},
		{
			Name:   "Add Email For Users",
			Number: 2,
			Forwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("ALTER TABLE users ADD COLUMN email TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add email: %w", err)
				}

				return nil
			},
			Backwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("ALTER TABLE users DROP COLUMN email")
				if err != nil {
					return fmt.Errorf("failed to drop email column for users table: %w", err)
				}

				return nil
			},
		},
		{
			Name:   "Add Address For Users",
			Number: 3,
			Forwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("ALTER TABLE users ADD COLUMN address TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add address: %w", err)
				}

				return nil
			},
			Backwards: func(tx *pg.Tx) error {
				_, err := tx.Exec("ALTER TABLE users DROP COLUMN address")
				if err != nil {
					return fmt.Errorf("failed to drop address column for users table: %w", err)
				}

				return nil
			},
		},
	}
}
