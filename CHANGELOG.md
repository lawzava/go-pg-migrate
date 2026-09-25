# v2.3.0

- Add `Options.DB` so callers can run migrations against a `*sql.DB` they already hold, instead of
  supplying a connection string for this package to open. `Options.DatabaseURI` is ignored when
  `Options.DB` is set, and a caller-supplied handle is never closed by this package.

The module path remains `github.com/lawzava/go-pg-migrate/v2`. Existing migration APIs remain compatible,
and `Options.DatabaseURI` behaves exactly as before when `Options.DB` is unset.
