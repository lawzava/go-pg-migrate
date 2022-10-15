package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate/v2"
)

func init() {
	const (
		up   = `ALTER TABLE users ADD COLUMN address TEXT`
		down = `ALTER TABLE users DROP COLUMN address`
	)

	migrate.AddMigration(
		&migrate.Migration{
			Name:   "Add Address For Users",
			Number: 3,
			Up: func(tx migrate.Tx) error {
				_, err := tx.Exec(up)
				if err != nil {
					return fmt.Errorf("failed to alter users table to add address: %w", err)
				}

				return nil
			},
			Down: func(tx migrate.Tx) error {
				_, err := tx.Exec(down)
				if err != nil {
					return fmt.Errorf("failed to drop address column for users table: %w", err)
				}

				return nil
			},
		},
	)
}
