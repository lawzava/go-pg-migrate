package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate"
)

func init() {
	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Add Email For Users",
			Number: 2,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec("ALTER TABLE users ADD COLUMN email TEXT")
				if err != nil {
					return fmt.Errorf("failed to alter users table to add email: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec("ALTER TABLE users DROP COLUMN email")
				if err != nil {
					return fmt.Errorf("failed to drop email column for users table: %w", err)
				}

				return nil
			},
		},
	)
}
