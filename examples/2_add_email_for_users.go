package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate"

	"github.com/go-pg/pg/v10"
)

func m2() *migrate.Migration {
	return &migrate.Migration{
		Name:   "Add Email For Users",
		Number: 2,
		Forwards: func(tx *pg.Tx) error {
			_, err := tx.Exec("ALTER TABLE users ADD COLUMN email TEXT")
			if err != nil {
				return fmt.Errorf("failed to alter users table to add email: %w", err)
			}

			return nil
		},
		Backwards: func(tx *pg.Tx) error {
			_, err := tx.Exec("ALTER TABLE users DROP COLUMN email")
			if err != nil {
				return fmt.Errorf("failed to drop email column for users table: %w", err)
			}

			return nil
		},
	}
}
