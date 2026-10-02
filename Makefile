.DEFAULT_GOAL := test

.PHONY: golangci-lint test race vet generate coverage benchmark integration postgres-corpus postgres-corpus-update

POSTGRES_ROOT ?=
POSTGRES_COMMIT := 630e607397424196a0a3ebb14a5658c2473ddadf
POSTGRES_SQL_SHA256 := ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d
CORPUS_PYTHON ?= python3
CORPUS_REPORT ?= postgres-coverage.json
CORPUS_EXPORT ?=
TEST_PROCS ?= 2
GOLANGCI_LINT_VERSION := v2.14.0
GOLANGCI_LINT := .bin/golangci-lint/$(GOLANGCI_LINT_VERSION)/golangci-lint
GOLANGCI_LINT_INSTALLER_REF := 114493f9b3e7257d29e4130f2b4a4aadefbb6845

golangci-lint: $(GOLANGCI_LINT)
	GOMAXPROCS=$(TEST_PROCS) "$(GOLANGCI_LINT)" run ./...
	cd integration && GOMAXPROCS=$(TEST_PROCS) "../$(GOLANGCI_LINT)" run --config ../.golangci.yml --build-tags postgres ./...

$(GOLANGCI_LINT):
	mkdir -p "$(dir $(GOLANGCI_LINT))"
	curl --fail --silent --show-error --location --retry 3 --max-time 60 "https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_INSTALLER_REF)/install.sh" --output "$(dir $(GOLANGCI_LINT))install.sh"
	sh "$(dir $(GOLANGCI_LINT))install.sh" -b "$(dir $(GOLANGCI_LINT))" $(GOLANGCI_LINT_VERSION)
	rm "$(dir $(GOLANGCI_LINT))install.sh"

test:
	GOMAXPROCS=$(TEST_PROCS) go test -p=2 -parallel=8 ./...
race:
	GOMAXPROCS=$(TEST_PROCS) go test -race -p=1 -parallel=8 ./...
vet:
	GOMAXPROCS=$(TEST_PROCS) go vet -p=2 ./...
generate:
	python3 scripts/generate.py
coverage:
	GOMAXPROCS=$(TEST_PROCS) go test -p=2 -parallel=8 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
benchmark:
	go test -run='^$$' -bench=. -benchmem -count=3 .
integration:
	cd integration && GOMAXPROCS=$(TEST_PROCS) go test -tags postgres -p=2 -parallel=8 -timeout 5m -v ./...
postgres-corpus:
	@test -n "$(POSTGRES_ROOT)" || (echo 'Set POSTGRES_ROOT to the pinned PostgreSQL source directory.' >&2; exit 1)
	GOMAXPROCS=$(TEST_PROCS) $(CORPUS_PYTHON) -m unittest discover -s scripts -p 'test_postgres_corpus.py'
	GOMAXPROCS=$(TEST_PROCS) $(CORPUS_PYTHON) scripts/postgres_corpus.py "$(POSTGRES_ROOT)" --commit "$(POSTGRES_COMMIT)" --expected-sql-sha256 "$(POSTGRES_SQL_SHA256)" --report "$(CORPUS_REPORT)" --minimum-support 98 --minimum-planner-support 98 --minimum-shape-support 98 --minimum-planner-shape-support 98 $(if $(CORPUS_EXPORT),--export-go "$(CORPUS_EXPORT)")

postgres-corpus-update: CORPUS_EXPORT := internal/postgrescorpus
postgres-corpus-update: postgres-corpus
