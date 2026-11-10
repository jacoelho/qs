# PostgreSQL corpus

This directory measures how much of PostgreSQL's regression SQL the public qs
API can express. It owns the frozen fixtures, their provenance and the Go
adapter that builds each query. Ordinary Go tests require no Python, parser,
network or database. PostgreSQL's [upstream notice](NOTICE.postgresql)
accompanies the derived data.

## Measurement contract

Each fixture record carries the PostgreSQL parse tree of one query. The
[`parsetree`](parsetree) package builds a `qs.Statement` from that tree through
exported constructors only; a test rejects any use of `UnsafeSQL`, `Fragment` or
an `...SQL` method in the adapter. The statement is rendered and the result is
one of four statuses:

| Status | Meaning |
|---|---|
| `verified` | The rendered SQL, reparsed by the pinned PostgreSQL parser, equals the source tree, and the bound arguments follow the source parameter order. |
| `out_of_scope` | The query uses syntax qs deliberately omits. The reason names every such feature. |
| `gap` | The query is in scope and the adapter found no public construction. The reason names what is missing. |
| `rejected` | The adapter built the query and `ToSQL` refused it. The reason is the qs error. |

The adapter never grades itself. `scripts/postgres_corpus.py` parses the source,
sends each tree to `cmd/corpusrender`, reparses the SQL that comes back and
freezes it as `verified` only when the trees match. Any other answer for a built
query (`ast_mismatch`, `arg_mismatch`, `reparse_error`) blocks the export.
`out_of_scope` and `gap` are the adapter's own classification; the fixed
verified floors keep them from growing unnoticed.

`go test` then repeats the build for every record: a verified record must
render its frozen SQL and arguments byte for byte, an `out_of_scope` or `gap`
record must produce the same reason, and a `rejected` record must still be
refused. The qs error text of a `rejected` record is informative, not compared.

Statements that only carry a query are outside qs. For `EXPLAIN`, `CREATE TABLE
AS`, `CREATE MATERIALIZED VIEW ... AS` and `DECLARE CURSOR` the carried query is
the one built and compared, as it is for bodies of `PREPARE`, views and rule
actions. A top-level `SELECT ... INTO` is built without its destination; a
nested one is out of scope. `original_sql` keeps the full source text.

An UPDATE or DELETE is not a qs statement until its rows are selected, and
`AllRows()` selects every row. The adapter calls it when the source has no
`WHERE`. A `WHERE` that is exactly `TRUE` would be a `gap`: `AllRows()` renders
no `WHERE`, and qs refuses `TRUE` as the only condition. No pinned query has
one.

Parameters are bound to sentinels unique per occurrence and source number. qs
numbers placeholders in rendering order and cannot repeat one, so a source whose
parameters are not `$1..$n` in that order is a `gap`.

The source is REL_18_STABLE commit
`630e607397424196a0a3ebb14a5658c2473ddadf`, all 233 regression SQL files.
The SQL-tree SHA256 is
`ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d`.
The development parser is pglast 8.4 with PostgreSQL 18.4 grammar; its
libpg_query JSON is the tree format. Source/hash and parser pins prevent
omissions from inflating coverage.

Eligible parsable queries are SELECT, VALUES, TABLE, INSERT, UPDATE, DELETE,
MERGE and EXPLAIN, including bodies in PREPARE, views, CTAS, cursors, rule
actions and literal SQL passed to regression EXPLAIN helpers. The scanner
handles psql commands, COPY data, strings, dollar quotes and nested comments.
Excluded units and parser errors are reported separately. Parsable negative
tests remain in the denominator, which is why most `rejected` records are
queries PostgreSQL itself refuses after parsing.

Comparison preserves constants, names, casts, operand/projection order, boolean
nesting, joins, set grouping, windows and ordering. `canonical_ast` removes
source positions and two spellings qs cannot vary: implicit `ASC`, and the
default window frame. This measures construction fidelity, not schema validity,
query results or equivalent plans.

## Pinned counts

| Status | Occurrences |
|---|---:|
| verified | 27,887 |
| out_of_scope | 220 |
| rejected | 50 |
| gap | 40 |
| total | 28,197 |

| Measure | Verified / total |
|---|---:|
| Query occurrences | 27,887 / 28,197 |
| Planner occurrences | 5,062 / 5,150 |
| Distinct query shapes | 13,517 / 13,722 |
| Distinct planner shapes | 3,831 / 3,893 |

Repeated queries count as separate occurrences; a shape is the source statement
with literal payloads removed, and passes only when every occurrence passes. The
planner subset includes EXPLAIN and named planner files selected by the tool.

Out of scope: XML syntax (159 occurrences), `EXPLAIN EXECUTE` (59) and nested
`SELECT INTO` (2).

Gaps: `CURRENT_ROLE` (9) and `USER` (1) have no constructor; parameters out of
rendering order (4); `ROW()` with no fields (3); `WITH RECURSIVE` before `MERGE`
(1); and 22 queries PostgreSQL rejects after parsing, which the typed API
cannot spell. Of those 22, 16 are SQL/JSON clause combinations: a behavior the
function does not take (9), `ENCODING UTF16` or `UTF32` (4),
`RETURNING text FORMAT JSON ENCODING UTF8` (2) and
`JSON_VALUE ... RETURNING ... FORMAT JSON` (1).

Rejected: 11 row comparisons against a multi-column subquery
(`(a, b) = ANY (SELECT x, y ...)`, `ROW(1, 2) = (SELECT 3, 4)`), which
PostgreSQL accepts; 4 `CURRENT_TIME(7)`-style precisions, which PostgreSQL
accepts with a warning; and 35 queries PostgreSQL also rejects, 11 of them
SQL/JSON: a repeated `JSON_TABLE` column or path name (5), `OMIT QUOTES` with
`WITH WRAPPER` (3), a `JSON_ARRAY(query)` whose query does not return one
column (2) and more `JSON_TABLE` column aliases than columns (1).

Of the 550 SQL/JSON occurrences, 523 are verified. Each of the other 27 was run
against PostgreSQL 18.6, which rejected all of them.

The same counts are pinned in [corpus_test.go](corpus_test.go) and, as floors
and denominators, in `scripts/postgres_corpus.py`. A change in any outcome fails
`go test` until the fixtures and both pins are updated deliberately.

## Layout and bounds

| Path | Owns |
|---|---|
| [manifest.json](manifest.json) | Source/parser pins, counts, and each fixture file's hash, ID range and status counts. |
| `testdata/corpus-NN.jsonl` | 2,048 records per file in contiguous ID order; one JSON object per line. |
| [parsetree](parsetree) | Parse tree to `qs.Statement`. |
| `fixture.go`, `manifest.go`, `render.go` | Record decoding, the manifest audit, and the build/render/compare step. |
| [cmd/corpusrender](cmd/corpusrender) | The process the offline tool pipes trees through. |

A record holds its provenance (`source`, `line`, `origin`, `wrapper`,
`statement`, `original_sql`), `shape`, `status`, `reason`, the `tree` with
location fields removed, and for verified records `want_sql`, `want_args` and
`normalization`.

`Audit` checks the fixture inventory, every hash and ID, per-file status counts
and the global shape totals. Tests decode one record at a time and run one
subtest per fixture file in parallel. A malformed tree is an error naming the
occurrence, never a panic; a mutation test over sampled corpus trees holds the
adapter to that.

## Reproduce and update

Run these commands from the repository root with Go 1.27 and Python 3.12:

```sh
python3.12 -m venv /tmp/qs-corpus-venv
/tmp/qs-corpus-venv/bin/python -m pip install -r scripts/requirements-postgres.txt
mkdir -p /tmp/qs-postgres-archive /tmp/qs-postgres-source
curl --fail --location --retry 3 --max-time 180 \
  https://codeload.github.com/postgres/postgres/tar.gz/630e607397424196a0a3ebb14a5658c2473ddadf \
  --output /tmp/qs-postgres-archive/postgres.tar.gz
tar -xzf /tmp/qs-postgres-archive/postgres.tar.gz -C /tmp/qs-postgres-source --strip-components=1
make postgres-corpus POSTGRES_ROOT=/tmp/qs-postgres-source \
  CORPUS_PYTHON=/tmp/qs-corpus-venv/bin/python
```

The target runs the tool's unit tests and then the full acceptance command:

```sh
/tmp/qs-corpus-venv/bin/python scripts/postgres_corpus.py \
  /tmp/qs-postgres-source --repo . \
  --commit 630e607397424196a0a3ebb14a5658c2473ddadf \
  --expected-sql-sha256 ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d \
  --require-pinned-coverage
```

`--require-pinned-coverage` requires the complete pinned source and parser, the
four denominators and every verified floor. It also compares every occurrence
with the checked-in fixtures: provenance, shape and tree must be unchanged, a
verified record must keep its SQL, arguments and normalization, and a `gap` or
`rejected` record may only stay as it is or become verified. An `out_of_scope`
record may take any status: when a feature enters scope, its queries verify,
expose a gap or are refused, and the Go pins then hold the new counts.
`--baseline-fixtures` selects
another baseline. Runs with `--limit` or `--no-render` are diagnostics and
cannot pass the gate.

The generated `postgres-coverage.json` is ignored. It records source locations,
SQL, statuses, exclusions and failure reasons. Override `CORPUS_REPORT` to
choose another path.

To replace fixtures after full verification:

```sh
make postgres-corpus-update POSTGRES_ROOT=/tmp/qs-postgres-source \
  CORPUS_PYTHON=/tmp/qs-corpus-venv/bin/python
```

The export applies the same gate and baseline comparison before it writes
anything, and refuses a partial run, mismatched pins, missing or extra adapter
answers, and any occurrence without a final status. A deliberate regression,
such as a feature moving out of scope, needs the floors in the script lowered
and the affected baseline records reviewed. Ordinary tests never update
fixtures; CI runs the committed ones.
