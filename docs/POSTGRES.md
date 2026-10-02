# PostgreSQL regression-query support

The pinned PostgreSQL 18 regression corpus measures **99.65% native
query construction support**, including **99.50% in the planner subset**. These
percentages describe the finite corpus below, not every possible PostgreSQL
query or equivalent execution plans.

All four **98%** gates pass on the full, unchanged corpus, including distinct
query shapes and planner shapes. The 100 remaining failures stay in the
denominator; no raw SQL constructors count toward support.

| Measure | Reconstructed | Total | Support |
|---|---:|---:|---:|
| Query occurrences | 28,097 | 28,197 | 99.65% |
| Planner occurrences | 5,124 | 5,150 | 99.50% |
| Distinct query shapes | 13,639 | 13,722 | 99.40% |
| Planner query shapes | 3,867 | 3,893 | 99.33% |

The [machine-readable report](postgres-coverage.json) records every included
query's source, line, wrapper, shape, rendered SQL or failure reason. It also
records excluded units, the parser version and the source hash. Unsupported
descendants remain failures in the denominator. Repeated upstream queries count
as separate occurrences; the distinct-shape figures expose that repetition.
A shape passes only when every occurrence in that group passes.

## Corpus and comparison contract

The source is PostgreSQL REL_18_STABLE commit
[`630e607397424196a0a3ebb14a5658c2473ddadf`](https://github.com/postgres/postgres/tree/630e607397424196a0a3ebb14a5658c2473ddadf/src/test/regress),
covering all 233 SQL files in `src/test/regress/sql`. The development-only parser
is pglast 8.4, using PostgreSQL 18.4 grammar. The parser adds no dependency to the
Go library.
The SQL-tree SHA256 is
`ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d`;
the regression gate requires it, preventing source omissions from inflating
the percentage.

Eligible statements are parsable SELECT, VALUES, TABLE, INSERT, UPDATE, DELETE,
MERGE and EXPLAIN. Extraction also includes query bodies in PREPARE, views,
CREATE TABLE AS, cursors and rule actions, and literal SQL passed to the
regression suite's EXPLAIN helper functions. Extracting a query body does not
claim support for its DDL or session wrapper. Planner coverage includes every
EXPLAIN plus the named planner-related files listed in the report.

The scanner recognises SQL strings, dollar quotes, nested comments, psql query
terminators and COPY data. DDL, session/control commands, COPY payloads, psql
commands/substitution and parser-invalid inputs are separately recorded. The
corpus intentionally contains errors: a parsable negative test still counts as
a query, and qs's structural rejection remains a failure. Comment-derived error
hints and linked expected-output files do not establish individual outcomes.
Likewise, `construction_error` means the generated builder failed validation,
not that PostgreSQL rejects the source query. Translation defects can cause
these failures even when the library already supports the syntax.

For each eligible query, the development tool emits calls to public qs
constructors, compiles and runs the resulting Go program, parses qs's output,
and compares its syntax tree with the original. Direct use of `UnsafeSQL`,
`Fragment` or `StatementSQL` is forbidden in generated builders. Ordinary
validated function/operator constructors are permitted.

The comparison preserves constants, names and qualification, casts, expression
operands, projection order, join structure, set grouping, windows and ordering.
It removes source positions and narrowly equivalent syntax: implicit ASC,
equivalent default window frames, EXPLAIN boolean spellings and AND nesting
with unchanged leaf order. Oracle tests reject deliberately changed queries.
This proves construction fidelity. Schema validity, results and planner choices
require execution tests; the separate pgx suite covers selected behaviours.

## Offline SQL and argument fixtures

`go test ./...` runs the full frozen native corpus in `internal/postgrescorpus`:
28,097 verified occurrences assert fixed expected SQL and arguments. All 28,197
records retain original SQL, file/line, source revision/hash, parser version,
wrapper and normalization provenance. The 100 unsupported or rejected cases
remain coverage metadata; the tests do not require them to remain unsupported.
PostgreSQL's copyright notice accompanies the derived fixtures.

The expected SQL is frozen only after the complete pinned upstream AST comparison
passes. Parameter values use distinct deterministic sentinels; their expected
order comes independently from the source AST. Ordinary Go tests invoke no
Python, parser, database, download or regeneration step. Handwritten SQL/argument
and rejection tests remain complementary contracts.

Eight test-only packages own builders partitioned by occurrence ID modulo eight.
Each embeds only its own JSONL oracle. The manifest pins the actual package
inventory, hashes, counts and IDs; the streaming audit combines distinct shapes
across the whole corpus. Source SQL and provenance are data rather than large Go
initializers.

The scheduler decodes serial groups of at most 256 records, runs each case in
parallel and waits for the group before decoding more. Each builder is constructed
after `t.Parallel()`. Failures identify the occurrence ID, source file and line,
original SQL, expected output and actual output. Use `-p=2 -parallel=8` to bound
package/build concurrency and case concurrency respectively. Race builds use
`-p=1` because instrumentation increases compiler memory. Existing allocation
measurements remain serial because
`testing.AllocsPerRun` changes process-wide scheduling; compiler type-check
subtests sharing one importer also remain serial.
Make targets and CI use `GOMAXPROCS=2`; `TEST_PROCS` overrides the Make default.

Live execution is optional:

```sh
QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable' \
  GOMAXPROCS=2 go test -tags postgres -race -p=2 -parallel=8 -timeout=5m ./integration
```

Every live file has the `postgres` build tag. Missing `QS_TEST_DSN` fails when
these tests are explicitly enabled. pgx remains a root-module test dependency;
regular tests exclude it from their compiled dependency graph.

## API improvements

Opaque JSON and JSONB document types expose distinct method sets. Key/index and
document/text extraction map to `->`, `->>`, `#>` and `#>>`; text results use
`Field[string]`. JSONB also offers containment, key existence, deletion,
concatenation and JSONPath predicates. Integer selectors explicitly cast to
PostgreSQL integer, avoiding unknown-parameter overload ambiguity. Encoded
document parameters, path arrays and JSONPath values also carry SQL casts.
See [the typed JSON examples](../README.md#typed-json-and-jsonb).

Native SQL/JSON object, array and aggregate constructors separate input formats,
output formats and applicable policies. SQL/XML constructors and XMLTABLE cover
namespaces, paths, defaults and ordinality. Composite fields, array indirection,
structured assignment/INSERT targets and integer type modifiers share existing
expression traversal and cloning.

Explicit zero-column SELECTs, unaliased derived tables and shorthand window
frames retain their PostgreSQL grammar distinctions. Keyword substring, trim,
overlay and normalization forms use dedicated constructors. Exact numeric and
bit-string literals avoid float64 rounding; radix and underscore spellings have
version checks.

EXECUTE, CREATE TABLE AS, materialized-view creation, cursor declarations and
SELECT INTO are Statement-only commands with applicable destination options.
Typed EXPLAIN serialization supports the bare option and NONE, TEXT and BINARY
modes. Rendering options belong to each call, including AppendWith, and propagate
through nested queries. Warmed typed JSON and mixed JSON/XML rendering measure
zero allocations with reused buffers. Construction and owned output still
allocate; see [performance](PERFORMANCE.md).

## Remaining gaps

The report is a conservative lower bound: an unsupported translation may expose
a missing adapter even when existing qs constructors can express the query.
The remaining 100 failures comprise 46 untranslated queries and 54 generated
builders rejected by validation. Every successfully rendered query parses and
matches its source tree. Missing translations include CURRENT_ROLE, USER,
some EXTRACT field spellings, empty ROW and non-sequential parameter references.
Validation failures remain in the denominator without assuming the source query
is invalid. Raw escape hatches do not count toward support.

Ordinary JOIN ON/USING, cross joins, unconstrained varchar casts, JSON_TABLE
EXISTS columns and multi-column IN are supported. The earlier failure groups
for these features were adapter defects: cross joins used the wrong constructor,
varchar required an unnecessary length, EXISTS inherited a value-only wrapper,
and row IN lost its width. Correct translations recover 100 of those 103 queries.
Two residual SQL/JSON cases are rejected by PostgreSQL itself; the third contains
an unrelated USER value-function translation gap.

| Largest remaining failure groups | Occurrences |
|---|---:|
| Generated scalar subquery has the wrong projection width | 11 |
| CURRENT_ROLE not translated | 9 |
| Unsupported SQL/JSON ON ERROR behaviour | 8 |
| SQL value-function precision outside 0..6 | 4 |
| INSERT value count differs from target columns | 4 |
| EXTRACT microsecond spelling not translated | 4 |

## Reproduce and maintain

With Go 1.27 and Python 3.12 or newer:

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

To update the checked-in Go fixtures explicitly, use the same pinned checkout:

```sh
make postgres-corpus-update POSTGRES_ROOT=/tmp/qs-postgres-source \
  CORPUS_PYTHON=/tmp/qs-corpus-venv/bin/python
```

The underlying `--export-go internal/postgrescorpus` option refuses partial,
uncompiled or unverified runs, a mismatched source hash/parser, missing or
extra probe IDs, argument mismatches and raw SQL constructors. Review the fixture
and report changes before committing them. Ordinary tests never update goldens.

CI repeats the full compiled comparison and requires at least 98% overall
occurrence, planner occurrence, distinct-shape and planner distinct-shape support.
Partial smoke checks and uncompiled extraction
cannot satisfy the gate. CI also regenerates and compares the frozen Go fixtures.
CI uploads its report; local results do not claim a
completed remote workflow run.
