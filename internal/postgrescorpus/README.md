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

Local corpus verification requires at least 98% support for all four measures:

| Measure | Verified / total | Support |
|---|---:|---:|
| Query occurrences | 28,097 / 28,197 | 99.65% |
| Planner occurrences | 5,124 / 5,150 | 99.50% |
| Distinct query shapes | 13,639 / 13,722 | 99.40% |
| Distinct planner shapes | 3,867 / 3,893 | 99.33% |

Repeated queries count as separate occurrences; a distinct shape passes only
when every occurrence passes. The planner subset includes EXPLAIN and named
planner files selected by the tool. All 100 gaps remain: 46 untranslated queries
and 54 generated-builder rejections. Missing translations include CURRENT_ROLE,
USER, some EXTRACT spellings, empty ROW and non-sequential parameters. The report
is a conservative lower bound because adapter gaps can hide supported syntax.

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
Refresh fixtures locally with the complete comparison and all four thresholds.
CI runs the committed Go fixtures without regenerating them. Review changes to
the manifest, oracles and builders.
