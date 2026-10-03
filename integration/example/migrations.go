package main

import migrate "github.com/lawzava/go-pg-migrate/v3"

// migrations is the full, ordered history. Append new migrations; never edit
// one that has been applied anywhere.
var migrations = []migrate.Migration{
	{
		Version: 1,
		Name:    "create users",
		Up:      migrate.SQL(`CREATE TABLE users (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name TEXT NOT NULL)`),
		Down:    migrate.SQL(`DROP TABLE users`),
	},
	{
		Version: 2,
		Name:    "add users.email",
		Up:      migrate.SQL(`ALTER TABLE users ADD COLUMN email TEXT`),
		Down:    migrate.SQL(`ALTER TABLE users DROP COLUMN email`),
	},
	{
		// CREATE INDEX CONCURRENTLY cannot run in a transaction.
		Version: 3,
		Name:    "index users.email",
		Up:      migrate.SQL(`CREATE UNIQUE INDEX CONCURRENTLY users_email_key ON users (email)`),
		Down:    migrate.SQL(`DROP INDEX CONCURRENTLY users_email_key`),
		NoTx:    true,
	},
}
