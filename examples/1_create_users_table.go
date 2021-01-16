package main

import (
	"fmt"

	migrate "github.com/lawzava/go-pg-migrate"

	"github.com/go-pg/pg/v10"
)

func m1() *migrate.Migration {
	return &migrate.Migration{
		Name:   "Create Users Table",
		Number: 1,
		Forwards: func(tx *pg.Tx) error {
			_, err := tx.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
			if err != nil {
				return fmt.Errorf("failed to create users table: %w", err)
			}

			return nil
		},
		Backwards: func(tx *pg.Tx) error {
			_, err := tx.Exec("DROP TABLE users")
			if err != nil {
				return fmt.Errorf("failed to drop users table: %w", err)
			}

			return nil
		},
	}
}
