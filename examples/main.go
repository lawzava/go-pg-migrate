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

	var (
		lastVersionNumberToApply uint
		forceVersion             bool
		refreshDatabase          bool
	)

	flag.UintVar(&lastVersionNumberToApply, "version", 0, "version of migration to run")
	flag.BoolVar(&forceVersion, "force", false, "version of migration to set in database without running any migrations")
	flag.BoolVar(&refreshDatabase, "refresh", false, "refresh database, should be set for first run (when DB is empty)")
	flag.Parse()

	err := migrate.
		New(db, migrations, lastVersionNumberToApply, forceVersion, refreshDatabase).
		Migrate()
	if err != nil {
		log.Fatal(err)
	}
}
