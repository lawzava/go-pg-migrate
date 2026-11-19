// Package migrate applies versioned PostgreSQL schema migrations written in Go.
//
// Migrations are a plain slice passed to New. Each one runs in its own
// transaction together with its ledger row, so a failure leaves neither a
// half-applied change nor an unrecorded one. Migrations marked NoTx run
// outside a transaction; a failed one is marked dirty instead. A PostgreSQL
// advisory lock serializes concurrent runners, so many replicas can call Up at
// startup.
//
// History is checked before anything runs. An applied version with no migration
// in code returns ErrUnknownVersion. A service that migrates at startup and may
// run an older image against a newer ledger can set Config.AllowUnknownVersions
// to leave such versions alone.
//
//	migrations := []migrate.Migration{
//		{
//			Version: 1,
//			Name:    "create users",
//			Up:      migrate.SQL(`CREATE TABLE users (id BIGINT PRIMARY KEY, name TEXT NOT NULL)`),
//			Down:    migrate.SQL(`DROP TABLE users`),
//		},
//	}
//
//	m, err := migrate.New(db, migrations, migrate.Config{})
//	if err != nil {
//		return err
//	}
//	return m.Up(ctx)
//
// The package imports only the standard library. Bring any database/sql
// PostgreSQL driver, such as github.com/jackc/pgx/v5/stdlib or
// github.com/lib/pq.
package migrate
