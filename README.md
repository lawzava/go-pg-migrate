![GolangCI](https://github.com/lawzava/go-pg-migrate/workflows/golangci/badge.svg?branch=main)
[![Version](https://img.shields.io/badge/version-v1.1.1-green.svg)](https://github.com/lawzava/go-pg-migrate/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/lawzava/go-pg-migrate)](https://goreportcard.com/report/github.com/lawzava/go-pg-migrate)
[![Coverage Status](https://coveralls.io/repos/github/lawzava/go-pg-migrate/badge.svg?branch=main)](https://coveralls.io/github/lawzava/go-pg-migrate?branch=main)
[![Go Reference](https://pkg.go.dev/badge/github.com/lawzava/go-pg-migrate.svg)](https://pkg.go.dev/github.com/lawzava/go-pg-migrate)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://awesome-go.com)


# go-pg-migrate

CLI-friendly package for PostgreSQL migrations management poweder by [pgx](https://github.com/jackc/pgx)

## Installation

Requires Go Modules enabled.

```
go get github.com/lawzava/go-pg-migrate
```

## Usage

Initialize the `migrate` with options payload where choices are:

- `VersionNumberToApply` uint value of a migration number up to which the migrations should be applied. 
When the requested migration number is lower than currently applied migration number it will run backward migrations, otherwise it will run forward migrations.
  
- `PrintVersionAndExit` if true, the currently applied version number will be printed into stdout and the migrations will not be applied.

- `ForceVersionWithoutMigrations` if true, the migrations will not be applied, but they will be registered as applied up to the specified version number.

- `RefreshSchema` if true, public schema will be dropped and recreated before the migrations are applied. Useful for frequent testing and CI environments.

## Example

You will find the example in [examples](examples) directory. The example is CLI-friendly and can be used as a base for CLI-based migrations utility.


