package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate"
)

func init() {
	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Create Users Table",
			Number: 1,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
				if err != nil {
					return fmt.Errorf("failed to create users table: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec("DROP TABLE users")
				if err != nil {
					return fmt.Errorf("failed to drop users table: %w", err)
				}

				return nil
			},
		},
	)
}
