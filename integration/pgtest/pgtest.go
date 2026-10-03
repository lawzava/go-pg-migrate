// Package pgtest gives each test a fresh PostgreSQL database.
//
// Set PGM_TEST_DSN to use an existing server, for example in CI. Otherwise
// Main starts an embedded server; PGM_TEST_PG_VERSION selects its major
// version (14 to 18, default 18).
package pgtest

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/jackc/pgx/v5/stdlib" // Registers driver "pgx".
	_ "github.com/lib/pq"              // Registers driver "postgres".
)

// Drivers lists the database/sql drivers every test runs against.
var Drivers = []string{"postgres", "pgx"}

var (
	adminDSN string
	counter  atomic.Int64
)

var versions = map[string]embeddedpostgres.PostgresVersion{
	"14": embeddedpostgres.V14,
	"15": embeddedpostgres.V15,
	"16": embeddedpostgres.V16,
	"17": embeddedpostgres.V17,
	"18": embeddedpostgres.V18,
}

// Main runs the package's tests against a PostgreSQL server.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if dsn := os.Getenv("PGM_TEST_DSN"); dsn != "" {
		adminDSN = dsn

		return m.Run()
	}

	version, ok := versions[cmp.Or(os.Getenv("PGM_TEST_PG_VERSION"), "18")]
	if !ok {
		fmt.Fprintln(os.Stderr, "pgtest: unsupported PGM_TEST_PG_VERSION")

		return 1
	}

	port, err := freePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest:", err)

		return 1
	}

	runtime, err := os.MkdirTemp("", "pgtest")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest:", err)

		return 1
	}
	defer os.RemoveAll(runtime)

	server := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(version).
		Port(port).
		CachePath(filepath.Join(os.TempDir(), "go-pg-migrate-postgres-cache")).
		RuntimePath(runtime).
		Logger(nil))

	if err := server.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: start postgres:", err)

		return 1
	}

	defer func() {
		if err := server.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "pgtest: stop postgres:", err)
		}
	}()

	adminDSN = fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)

	return m.Run()
}

// DSN creates an empty database, drops it when the test ends, and returns its
// connection string.
func DSN(tb testing.TB) string {
	tb.Helper()

	name := fmt.Sprintf("pgtest_%d_%d", os.Getpid(), counter.Add(1))

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := admin.ExecContext(tb.Context(), "CREATE DATABASE "+name); err != nil {
		tb.Fatalf("create database: %v", err)
	}

	tb.Cleanup(func() {
		defer admin.Close()

		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			tb.Errorf("drop database: %v", err)
		}
	})

	dsn, err := url.Parse(adminDSN)
	if err != nil {
		tb.Fatal(err)
	}

	dsn.Path = "/" + name

	return dsn.String()
}

// Open connects to dsn with driver and closes the pool when the test ends.
func Open(tb testing.TB, driver, dsn string) *sql.DB {
	tb.Helper()

	db, err := sql.Open(driver, dsn)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = db.Close() })

	return db
}

// DB returns a connection pool to a fresh database.
func DB(tb testing.TB, driver string) *sql.DB {
	tb.Helper()

	return Open(tb, driver, DSN(tb))
}

func freePort() (uint32, error) {
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "localhost:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	defer listener.Close()

	return uint32(listener.Addr().(*net.TCPAddr).Port), nil //nolint:forcetypeassert,gosec // TCP ports fit in uint32.
}
