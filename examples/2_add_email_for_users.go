package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate/v2"
)

func init() {
	const (
		up   = `ALTER TABLE users ADD COLUMN email TEXT`
		down = `ALTER TABLE users DROP COLUMN email`
	)

	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Add Email For Users",
			Number: 2,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec(up)
				if err != nil {
					return fmt.Errorf("failed to alter users table to add email: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec(down)
				if err != nil {
					return fmt.Errorf("failed to drop email column for users table: %w", err)
				}

				return nil
			},
		},
	)
}
