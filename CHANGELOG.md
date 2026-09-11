# v2.2.0

- Require Go 1.26 or later.
- Replace zerolog with standard-library `log/slog`. Default output follows the application's default slog handler. `Options.LogInfo` keeps its printf-style callback contract.
- Update all Go module dependencies to their latest available versions.
- Upgrade golangci-lint to v2.13.2 and migrate its configuration to v2.
- Update GitHub Actions and run tests, race detection, dependency checks, and lint on pull requests.
- Isolate PostgreSQL test runtime files and serialize tests that share the migration registry.

The module path remains `github.com/lawzava/go-pg-migrate/v2`. Existing migration APIs remain compatible.
