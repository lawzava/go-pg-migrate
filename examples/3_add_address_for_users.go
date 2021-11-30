package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate/v2"
)

func init() {
	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Add Address For Users",
			Number: 3,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec("ALTER TABLE users ADD COLUMN address TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add address: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec("ALTER TABLE users DROP COLUMN address")
				if err != nil {
					return fmt.Errorf("failed to drop address column for users table: %w", err)
				}

				return nil
			},
		},
	)
}
