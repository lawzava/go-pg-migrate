package migrate

import (
	"time"

	"github.com/go-pg/pg/v10"
)

type Migration struct {
	Name   string
	Number uint

	Forwards  func(tx *pg.Tx) error
	Backwards func(tx *pg.Tx) error
}

type migration struct {
	ID        uint      `pg:",pk"`
	CreatedAt time.Time `pg:"default:NOW(),notnull"`
	Name      string    `pg:",notnull"`
	Number    uint      `pg:",notnull,unique"`

	Forwards  func(tx *pg.Tx) error `pg:"-"`
	Backwards func(tx *pg.Tx) error `pg:"-"`
}

func mapMigrations(m []*Migration) []*migration {
	migrations := make([]*migration, len(m))

	for i := range m {
		// nolint:exhaustivestruct // ID & created_at are filled by go-pg
		migrations[i] = &migration{
			Name:      m[i].Name,
			Number:    m[i].Number,
			Forwards:  m[i].Forwards,
			Backwards: m[i].Backwards,
		}
	}

	return migrations
}
