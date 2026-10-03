module github.com/lawzava/go-pg-migrate/v3/integration

go 1.25.0

require (
	github.com/fergusstrange/embedded-postgres v1.34.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/lawzava/go-pg-migrate/v3 v3.0.0
	github.com/lib/pq v1.12.3
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/xi2/xz v0.0.0-20171230120015-48954b6210f8 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/lawzava/go-pg-migrate/v3 => ../
