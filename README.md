![golangci](https://github.com/lawzava/go-pg-migrate/workflows/golangci/badge.svg?branch=main)
[![Version](https://img.shields.io/badge/version-v1.0.3-green.svg)](https://github.com/lawzava/go-pg-migrate/releases)
[![GoDoc](https://godoc.org/github.com/lawzava/go-pg-migrate?status.svg)](http://godoc.org/github.com/lawzava/go-pg-migrate)
[![Go Report Card](https://goreportcard.com/badge/github.com/lawzava/go-pg-migrate)](https://goreportcard.com/report/github.com/lawzava/go-pg-migrate)


# go-pg-migrate

CLI-friendly package for [go-pg](https://github.com/go-pg/pg) migrations management.

## Installation

Requires Go Modules enabled.

```
go get github.com/lawzava/go-pg/migrations
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


