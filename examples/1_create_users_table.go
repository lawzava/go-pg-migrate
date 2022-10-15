package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate/v2"
)

func init() {
	const (
		up   = `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`
		down = `DROP TABLE users`
	)

	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Create Users Table",
			Number: 1,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec(up)
				if err != nil {
					return fmt.Errorf("failed to create users table: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec(down)
				if err != nil {
					return fmt.Errorf("failed to drop users table: %w", err)
				}

				return nil
			},
		},
	)
}
