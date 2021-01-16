package main

import (
	"flag"
	"log"

	"github.com/go-pg/pg/v10"
	migrate "github.com/lawzava/go-pg-migrate"
)

func main() {
	db := pg.Connect(&pg.Options{
		User:     "postgres",
		Database: "migrate-test",
	})

	var opt migrate.Options

	flag.UintVar(&opt.VersionNumberToApply, "version", 0,
		"migrate to specified printVersion; 0 will apply all forward migrations")
	flag.BoolVar(&opt.PrintInfoAndExit, "current", false,
		"print last applied version and exit")
	flag.BoolVar(&opt.ForceVersionWithoutMigrations, "force", false,
		"force version of migration to set in database without running any migrations")
	flag.BoolVar(&opt.RefreshSchema, "refresh", false,
		"refresh database, should be set for first run (when DB is empty)")
	flag.Parse()

	m, err := migrate.New(db, opt,
		m1, m2, m3)
	if err != nil {
		log.Fatal(err)
	}

	err = m.Migrate()
	if err != nil {
		log.Fatal(err)
	}
}
