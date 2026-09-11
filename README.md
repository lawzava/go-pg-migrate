![Golang](https://github.com/lawzava/go-pg-migrate/actions/workflows/golang.yml/badge.svg?branch=main)
[![Version](https://img.shields.io/github/v/release/lawzava/go-pg-migrate)](https://github.com/lawzava/go-pg-migrate/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/lawzava/go-pg-migrate)](https://goreportcard.com/report/github.com/lawzava/go-pg-migrate)
[![Coverage Status](https://coveralls.io/repos/github/lawzava/go-pg-migrate/badge.svg?branch=main)](https://coveralls.io/github/lawzava/go-pg-migrate?branch=main)
[![Go Reference](https://pkg.go.dev/badge/github.com/lawzava/go-pg-migrate.svg)](https://pkg.go.dev/github.com/lawzava/go-pg-migrate)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://awesome-go.com)


# go-pg-migrate

CLI-friendly package for PostgreSQL migrations management.

## Installation

Requires Go 1.26 or later.

```
go get github.com/lawzava/go-pg-migrate/v2
```

## Usage

Initialize the `migrate` with options payload where choices are:

- `DatabaseURI` database connection string. In a format of `postgres://user:password@host:port/database?sslmode=disable`.

- `VersionNumberToApply` uint value of a migration number up to which the migrations should be applied. 
When the requested migration number is lower than currently applied migration number it will run backward migrations, otherwise it will run forward migrations.
  
- `PrintInfoAndExit` if true, logs the currently applied version and exits without applying migrations.

- `ForceVersionWithoutMigrations` if true, the migrations will not be applied, but they will be registered as applied up to the specified version number.

- `RefreshSchema` if true, public schema will be dropped and recreated before the migrations are applied. Useful for frequent testing and CI environments.

- `LogInfo` overrides info logging with a printf-style callback. By default, messages use the standard library's `log/slog` default logger at INFO level. Use `slog.SetDefault` to configure the handler and output format.

## Example

You will find the example in [examples](examples) directory. The example is CLI-friendly and can be used as a base for CLI-based migrations utility.

## Release

After review and merge to `main`, CI publishes v2.2.0 once tests and lint pass. It skips publication if that release already exists. See [CHANGELOG.md](CHANGELOG.md) for release notes.
