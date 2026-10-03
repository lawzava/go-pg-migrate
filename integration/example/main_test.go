package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	migrate "github.com/lawzava/go-pg-migrate/v3"
	"github.com/lawzava/go-pg-migrate/v3/integration/pgtest"
	"github.com/lawzava/go-pg-migrate/v3/migratetest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

func TestMigrationsRoundTrip(t *testing.T) {
	t.Parallel()

	migratetest.RoundTrip(t, pgtest.DB(t, "pgx"), migrations, migrate.Config{})
}

func TestCLI(t *testing.T) {
	t.Parallel()

	dsn := pgtest.DSN(t)

	for _, args := range [][]string{{"up-to", "2"}, {"up"}, {"down", "1"}} {
		if err := run(t.Context(), dsn, args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}

	stdout := os.Stdout

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = write
	err = run(t.Context(), dsn, []string{"status"})
	os.Stdout = stdout

	_ = write.Close()

	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := io.Copy(&out, read); err != nil {
		t.Fatal(err)
	}

	var status []migrate.Entry
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, out.String())
	}

	want := []migrate.State{migrate.StateApplied, migrate.StatePending, migrate.StatePending}
	for i, entry := range status {
		if entry.State != want[i] {
			t.Errorf("version %d state = %s, want %s", entry.Version, entry.State, want[i])
		}
	}
}
