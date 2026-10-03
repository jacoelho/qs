# PostgreSQL corpus

This directory owns the frozen native construction fixtures and their provenance.
Ordinary Go tests require no Python, parser, network or database. PostgreSQL's
[upstream notice](NOTICE.postgresql) accompanies the derived data.

## Measurement contract

Every supported occurrence is compiled through public qs constructors, rendered,
reparsed with the pinned PostgreSQL parser and compared with its source AST.
UnsafeSQL, Fragment and StatementSQL are forbidden in generated builders.
Parameter sentinels are distinct; expected argument order comes independently
from the source AST. Fixed SQL and arguments are frozen only after comparison.

The source is REL_18_STABLE commit
`630e607397424196a0a3ebb14a5658c2473ddadf`, all 233 regression SQL files.
The SQL-tree SHA256 is
`ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d`.
The development parser is pglast 8.4 with PostgreSQL 18.4 grammar. Source/hash and
parser pins prevent omissions from inflating coverage.

Eligible parsable queries are SELECT, VALUES, TABLE, INSERT, UPDATE, DELETE,
MERGE and EXPLAIN, including bodies in PREPARE, views, CTAS, cursors, rule actions
and literal SQL passed to regression EXPLAIN helpers. Extracted bodies do not
claim support for their surrounding DDL/session commands. The scanner handles
psql commands, COPY data, strings, dollar quotes and nested comments. Excluded
units and parser errors are reported separately. Parsable negative tests remain
in the denominator; builder rejection does not prove invalid source SQL.

Comparison preserves constants, names, casts, operand/projection order, joins,
set grouping, windows and ordering. `canonical_ast` removes source positions and
narrowly equivalent spellings such as implicit ASC, default window frames,
EXPLAIN booleans and AND grouping with unchanged leaf order. This measures
construction fidelity, not schema validity, query results or equivalent plans.

Pinned corpus acceptance uses fixed denominators and support floors. The
percentages below are descriptive; a percentage threshold cannot replace these
counts:

| Measure | Verified / total | Support |
|---|---:|---:|
| Query occurrences | 28,111 / 28,197 | 99.70% |
| Planner occurrences | 5,124 / 5,150 | 99.50% |
| Distinct query shapes | 13,645 / 13,722 | 99.44% |
| Distinct planner shapes | 3,867 / 3,893 | 99.33% |

Repeated queries count as separate occurrences; a distinct shape passes only
when every occurrence passes. The planner subset includes EXPLAIN and named
planner files selected by the tool. The 86 remaining gaps comprise 33 untranslated
queries and 53 generated-builder rejections. Missing translations include
CURRENT_ROLE, USER, empty ROW and non-sequential parameters. EXTRACT(FORTNIGHT)
remains rejected. The report is a conservative lower bound because adapter gaps
can hide supported syntax.

`--require-pinned-coverage` is the full-probe acceptance mode. It requires the
complete pinned source and parser, all four denominators above, and every
support floor. It also compares every occurrence with the existing fixture
baseline, preserving verified IDs, provenance, normalization, SQL and argument
oracles. `--baseline-fixtures` selects that baseline and defaults to
`internal/postgrescorpus`. Export mode always enables the fixed gate and the
baseline comparison before writing any shard.

Runs with `--limit` or `--no-probe` remain useful diagnostics, but cannot pass
the pinned acceptance gate.

## Inventory and bounds

[manifest.json](manifest.json) owns source/parser pins, counts, shard hashes and
strided IDs. Eight test-only packages partition builders by ID modulo eight;
each owns its JSONL SQL/argument oracle and provenance. Unsupported records are
metadata, not assertions that support must remain absent. The audit checks
actual inventory, every ID/hash and shape totals across shards.

Serial groups decode at most 256 records, run parallel case children, and wait
before decoding the next group. Builders are constructed after t.Parallel.
Use `GOMAXPROCS=2`, `-p=2 -parallel=8`; race builds use `-p=1` to bound compiler
memory. Allocation checks and compiler probes sharing an importer remain serial.

## Reproduce and update

Run these commands from the repository root with Go 1.27 and Python 3.12+:

```sh
python3 -m venv /tmp/qs-corpus-venv
/tmp/qs-corpus-venv/bin/python -m pip install -r scripts/requirements-postgres.txt
curl --fail --location --retry 3 --max-time 180 \
  https://codeload.github.com/postgres/postgres/tar.gz/630e607397424196a0a3ebb14a5658c2473ddadf \
  --output /tmp/qs-postgres.tar.gz
mkdir -p /tmp/qs-postgres-source
tar -xzf /tmp/qs-postgres.tar.gz -C /tmp/qs-postgres-source --strip-components=1
make postgres-corpus POSTGRES_ROOT=/tmp/qs-postgres-source \
  CORPUS_PYTHON=/tmp/qs-corpus-venv/bin/python
```

The full direct acceptance command is:

```sh
/tmp/qs-corpus-venv/bin/python scripts/postgres_corpus.py \
  /tmp/qs-postgres-source --repo . \
  --commit 630e607397424196a0a3ebb14a5658c2473ddadf \
  --expected-sql-sha256 ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d \
  --require-pinned-coverage
```

The generated `postgres-coverage.json` is ignored. It records source locations,
SQL, argument checks, exclusions and failures. Override `CORPUS_REPORT` to choose
another local path.

To replace fixtures after full verification:

```sh
make postgres-corpus-update POSTGRES_ROOT=/tmp/qs-postgres-source \
  CORPUS_PYTHON=/tmp/qs-corpus-venv/bin/python
```

Export rejects partial/uncompiled runs, mismatched pins, missing/extra probe IDs,
argument mismatches and raw constructors. Ordinary tests never update goldens.
Refresh fixtures locally only after the fixed gate and per-ID baseline comparison
pass. The exporter checks the baseline before modifying the output directory.
CI runs the committed Go fixtures without regenerating them. Review changes to
the manifest, oracles and builders.
