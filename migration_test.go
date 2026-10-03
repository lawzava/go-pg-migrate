package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		migrations []Migration
		wantErrs   []string
	}{
		{name: "valid", migrations: migs(1, 2, 20261003120000)},
		{name: "empty list", migrations: nil},
		{name: "zero version", migrations: []Migration{{Version: 0, Name: "a", Up: noop()}}, wantErrs: []string{"version must be positive"}},
		{name: "negative version", migrations: []Migration{{Version: -1, Name: "a", Up: noop()}}, wantErrs: []string{"version must be positive"}},
		{name: "empty name", migrations: []Migration{{Version: 1, Up: noop()}}, wantErrs: []string{"name is empty"}},
		{name: "nil Up", migrations: []Migration{{Version: 1, Name: "a", Down: noop()}}, wantErrs: []string{"Up is nil"}},
		{name: "nil Down is allowed", migrations: []Migration{{Version: 1, Name: "a", Up: noop()}}},
		{
			name:       "duplicate",
			migrations: []Migration{{Version: 1, Name: "a", Up: noop()}, {Version: 1, Name: "b", Up: noop()}},
			wantErrs:   []string{`duplicates version of "a"`},
		},
		{
			name:       "reports every problem",
			migrations: []Migration{{Version: 0}, {Version: 2, Name: "b"}},
			wantErrs:   []string{"version must be positive", "name is empty", "Up is nil"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := Validate(tt.migrations)
			if len(tt.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, ErrInvalidMigration) {
				t.Fatalf("Validate() = %v, want ErrInvalidMigration", err)
			}

			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Validate() = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

type recordingExecutor struct {
	Executor

	queries []string
	args    [][]any
}

func (r *recordingExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	r.queries = append(r.queries, query)
	r.args = append(r.args, args)

	return nil, nil //nolint:nilnil // The result is unused by SQL.
}

func TestSQL(t *testing.T) {
	t.Parallel()

	const query = "CREATE TABLE a (id int); CREATE TABLE b (id int)"

	exec := &recordingExecutor{}
	if err := SQL(query)(t.Context(), exec); err != nil {
		t.Fatal(err)
	}

	if len(exec.queries) != 1 || exec.queries[0] != query {
		t.Fatalf("queries = %q, want one call with %q", exec.queries, query)
	}

	// Arguments force the extended protocol, which rejects multiple statements.
	if len(exec.args[0]) != 0 {
		t.Fatalf("args = %v, want none", exec.args[0])
	}
}
