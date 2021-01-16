package main

import (
	"flag"
	"log"
	"migrate"

	"github.com/go-pg/pg/v10"
)

func main() {
	db := pg.Connect(&pg.Options{
		User:     "postgres",
		Database: "migrate-test",
	})

	migrations := []*migrate.Migration{
		m1(), m2(), m3(),
	}

	var opt migrate.Options

	flag.UintVar(&opt.VersionNumberToApply, "version", 0,
		"migrate to specified printVersion; 0 will apply all forward migrations")
	flag.BoolVar(&opt.PrintVersionAndExit, "current", false,
		"print last applied version and exit")
	flag.BoolVar(&opt.ForceVersionWithoutMigrations, "force", false,
		"force version of migration to set in database without running any migrations")
	flag.BoolVar(&opt.RefreshSchema, "refresh", false,
		"refresh database, should be set for first run (when DB is empty)")
	flag.Parse()

	err := migrate.New(db, migrations, opt).Migrate()
	if err != nil {
		log.Fatal(err)
	}
}
