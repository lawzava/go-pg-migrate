package main

import (
	"flag"
	"log"

	migrate "github.com/lawzava/go-pg-migrate/v2"
)

func main() {
	var opt migrate.Options

	flag.StringVar(&opt.DatabaseURI, "database-uri", "postgres://postgres@localhost:5432/migrate-test",
		"database uri to connect to")
	flag.UintVar(&opt.VersionNumberToApply, "version", 0,
		"migrate to specified printVersion; 0 will apply all forward migrations")
	flag.BoolVar(&opt.PrintInfoAndExit, "current", false,
		"print last applied version and exit")
	flag.BoolVar(&opt.ForceVersionWithoutMigrations, "force", false,
		"force version of migration to set in database without running any migrations")
	flag.BoolVar(&opt.RefreshSchema, "refresh", false,
		"refresh database, should be set for first run (when DB is empty)")
	flag.Parse()

	m, err := migrate.New(opt)
	if err != nil {
		log.Fatal(err)
	}

	err = m.Migrate()
	if err != nil {
		log.Fatal(err)
	}
}
