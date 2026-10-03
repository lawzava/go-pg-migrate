// Command example is a migration CLI built on go-pg-migrate.
//
//	example -dsn postgres://... up
//	example -dsn postgres://... up-to 2
//	example -dsn postgres://... down 1
//	example -dsn postgres://... status
//	example -dsn postgres://... baseline 2
//	example -dsn postgres://... resolve 3 applied|reverted
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrate "github.com/lawzava/go-pg-migrate/v3"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "PostgreSQL connection string (default $DATABASE_URL)")

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, *dsn, flag.Args())

	stop()

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn string, args []string) error {
	if len(args) == 0 {
		return errors.New("command required: up, up-to, down, status, baseline, resolve")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	migrator, err := migrate.New(db, migrations, migrate.Config{})
	if err != nil {
		return err
	}

	switch command := args[0]; command {
	case "up":
		return migrator.Up(ctx)
	case "status":
		return printStatus(ctx, migrator)
	default:
		return runWithVersion(ctx, migrator, command, args[1:])
	}
}

// runWithVersion runs a command that takes a version argument.
func runWithVersion(ctx context.Context, migrator *migrate.Migrator, command string, rest []string) error {
	commands := map[string]func(context.Context, int64) error{
		"up-to":    migrator.UpTo,
		"down":     migrator.Down,
		"baseline": migrator.Baseline,
		"resolve": func(ctx context.Context, version int64) error {
			if len(rest) < 2 || (rest[1] != "applied" && rest[1] != "reverted") {
				return errors.New("resolve needs a version and applied or reverted")
			}

			return migrator.Resolve(ctx, version, rest[1] == "applied")
		},
	}

	fn, ok := commands[command]
	if !ok {
		return fmt.Errorf("unknown command %q", command)
	}

	if len(rest) == 0 {
		return fmt.Errorf("%s needs a version", command)
	}

	version, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return fmt.Errorf("version: %w", err)
	}

	return fn(ctx, version)
}

// printStatus writes the status as JSON, which scripts and agents can parse.
func printStatus(ctx context.Context, migrator *migrate.Migrator) error {
	status, err := migrator.Status(ctx)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(status); err != nil {
		return fmt.Errorf("write status: %w", err)
	}

	return nil
}
