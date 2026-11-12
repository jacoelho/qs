# Develop qs

Run these checks from the repository root:

```sh
make golangci-lint
make test
```

`make golangci-lint` installs the pinned linter in `.bin/` when needed and
checks the root packages, the PostgreSQL integration module and the `qsvet`
module. `make test` and `make race` also run the `qsvet` tests, and
`make qsvet` checks this repository with the analyzer.

Regular tests need no database or database driver. Make and CI bound test
parallelism; use `TEST_PROCS` to override the Make default.

Pull request checks run the linter, the generated-code check, the analyzer,
regular tests, and race tests.

Use these targets for additional development tasks:

| Task | Command |
|---|---|
| Detect data races | `make race` |
| Analyze Go code | `make vet` |
| Measure test coverage | `make coverage` |
| Regenerate code | `make generate` |
| Verify generated code without changes | `make generate-check` |
| Run benchmarks | `make benchmark` |

`make generate` runs `go run ./internal/buildergen`, which writes the methods
every statement builder repeats and the typed tuples. `make generate-check`
runs it with `-check`, which reports stale output without writing.

To run database tests, supply a PostgreSQL connection string:

```sh
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
make integration
```

The `integration/` module owns the pgx dependency and uses the local qs checkout.
The integration suite supports PostgreSQL 15 through 18, and pull request checks
run it against each of them with the race detector. This coverage verifies the
syntax qs renders without claiming support for every combination of schema,
types, and extensions.
