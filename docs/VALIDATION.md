# Delivery validation

## Validation baseline — 2 October 2026

These results describe the tree before removal of historical demo and reference
fixtures. Later cleanup checks are recorded separately below.

Environment: Go 1.27.0, darwin/arm64; isolated PostgreSQL 18.6 over a temporary
local socket. The shipped module is tested directly, without an alternate
modfile. Core unit/example tests, race detection and vet pass. Core Go statement
coverage is 81.2%, unrelated to PostgreSQL query-construction percentages.

Regular tests now include the entire frozen PostgreSQL occurrence corpus. The
28,097 verified cases assert fixed SQL and independently derived argument order;
all 28,197 records preserve source provenance and the 100 construction gaps.
Eight test-only builder packages use fixed JSONL oracles and a streaming metadata
audit. Serial parent groups retain at most 256 records per shard while individual
cases run in parallel. The audit verifies every strided ID, actual package/file
inventory, hashes, status totals and global shape coverage across shard boundaries.
The full default suite passed with an empty module cache, `GOPROXY=off`,
`GOSUMDB=off` and a deliberately invalid `QX_TEST_DSN`. Its dependency graph
contains no external packages or live integration package. Native fixtures need
no Python, parser or database during tests.

Cold builds of the complete offline suite were measured on an Apple M2 Max
(12 cores), with `GOMAXPROCS=2`, fresh build caches and empty module cache.
Both used `-count=1 -shuffle=on -parallel=8`; ordinary tests used `-p=2`, race
tests used `-p=1`. CPU scheduling priority was lowered for these measurements.

| Cold command | Result | Time | Sampled aggregate peak RSS | Peak cache + build workspace |
|---|---|---:|---:|---:|
| Ordinary full suite | Pass | 38.75 s | 2.46 GiB | 0.80 GiB |
| Race full suite | Pass | 151.12 s | 3.12 GiB | 0.96 GiB |

RSS was sampled every 200 ms and summed over the test command's process group;
shared resident pages may be counted more than once. Disk usage was sampled each
second. These are local measurements, not a verified Linux/CI memory ceiling.
The earlier monolithic fixture build reached 5.21 GiB maximum RSS and failed after
168 seconds while writing its archive (`no space left on device`). Eight shards
alone did not sufficiently limit race compiler memory: `-p=2` passed in 87.53 s
but peaked at 4.59 GiB, motivating the race target's `-p=1` limit. Regular Make
targets limit builds to two packages; race targets use one. CI uses the same
limits and `GOMAXPROCS=2`.

The handwritten core/example suites pass three race/shuffle repetitions with
`-parallel=8`. All eligible top-level and table tests run in parallel. The six
allocation-test ancestors and compiler type-check subtests sharing an importer
remain serial. Version options and all feature thresholds use `PostgreSQLVersion`
and named PostgreSQL12..18 constants; invalid versions preserve atomic errors,
zero defaults to18, and compile-negative cases enforce typed API boundaries.
Typed INSERT examples also pass SQL/argument goldens and two race/shuffle
repetitions of their external-client compile guards: a field assignment accepts
one correctly typed value and rejects missing, extra or wrongly typed values.
The existing variadic INSERT width mismatch remains a rendering error.

Live tests use `-tags postgres` in the root module. The latest tagged suite passed
two race/shuffle repetitions with eight parallel tests. Each test owns a schema
and connection; fresh bounded cleanup removed every `qx_test_` schema. The local
server was stopped after verification. Missing DSN in an opted-in run fails
explicitly. CI uses the same tag and bounded parallelism.

The optional pgx integration suite passes with race detection. Cases cover typed
JSON extraction/operators/updates, SQL/JSON construction and policies, SQL/XML
and XMLTABLE, composite fields/assignments, successive domain-array and geometric
subscripting, SQL grammar expressions, zero-column SELECTs, radix literals,
unaliased lateral queries, window frames, EXPLAIN serialization and query utility
commands. Independent live indirection fixtures distinguish grouped results from
flattened NULL results. FROM casts execute through both table-function forms.
Live cases also distinguish ON/USING/natural joins from Cartesian products,
bounded varchar truncation from unconstrained casts, JSON_TABLE key existence
from non-null values, and row IN/NOT IN ordering and NULL/empty-set behaviour.
Integration dependency checksums are checked in.
Time precision checks measure millisecond alignment rather than displayed
fractional digits. The temporary database server was stopped after verification.

The rendering API uses `ToSQL`/`ToSQLWith` and caller-owned `AppendSQL`/`AppendWith`
methods, with breaking renames documented in
[API.md](API.md#rendering-api-migration). External-client type checks cover the
new names, typed placeholder styles and JSON operand/document roles. Independent
Dollar/Question goldens cover nested queries, quotes/semicolons/JSONB operators,
argument order, prefix numbering, reuse, depth limits and atomic errors. Both
styles pass warmed zero-allocation checks, including a mixed JSON/XML fixture
with 13 arguments. Construction and owned-output allocation costs are measured
separately in [PERFORMANCE.md](PERFORMANCE.md). Native PostgreSQL/pgx execution
uses Dollar; Question output is construction-tested.

The complete pinned PostgreSQL regression corpus passes all four 98% gates:
28,097 of 28,197 query occurrences (99.65%), 5,124 of 5,150 planner occurrences
(99.50%), 13,639 of 13,722 distinct shapes (99.40%) and 3,867 of 3,893 planner
shapes (99.33%). Source revision, SQL-tree hash, extraction scope and denominators
are unchanged. No raw constructor is accepted in generated builders. All
successfully rendered queries reparse and match the source syntax tree; 46
untranslated queries and 54 generated-builder rejections remain failures.
Correcting four adapter defects recovers 100 queries already expressible through
native APIs. A builder rejection alone does not prove the source SQL invalid.
[POSTGRES.md](POSTGRES.md) defines the measurement and remaining gaps. This proves
construction fidelity, not universal schema validity or equivalent query plans.

Fifteen grouped corpus-tool tests pass, covering extraction, comparison boundaries,
source hashes, compiled adapter round trips, mismatched row-width rejection,
unsupported JSON encoding/policy rejection, negative AST oracles and
partial/uncompiled/all-four threshold checks, deterministic parameter sentinels,
probe ID/argument integrity and complete strided JSONL exports. Native harness
controls reject corrupt streams, inventories, factory associations and oracles;
they accept long records and a final JSON record without a newline. The full
compiled upstream comparison regenerated the new fixtures with unchanged source,
counts, SQL and statuses; only the report timestamp differs. Named QA assessed
the test batches and reviewed the final streaming harness;
the independent advisor confirmed query-utility contexts and explicit indirection
grouping. Generated Go files are reproducible. All three
existing fuzz targets passed fresh 15-second campaigns with two workers each.

The code review replaces numeric state tags with defined enums for SQL/JSON,
XMLTABLE, SELECT quantifiers, CTE materialization, identity overriding, set
operations and window frames. Regression tests cover unknown-state rejection,
CYCLE constant grammar and version gates, configured-depth width validation,
local window inheritance and frame ordering, dotted conflict-column names,
unsupported EXTRACT units, and nil/zero RenderError methods. Named QA assessed
the test batches before integration. The final changes pass race tests, vet,
diff-scoped lint, live integration tests, corpus checks, generation,
three 15-second fuzz campaigns, and three benchmark repetitions.
Reusable-buffer benchmark fixtures still report zero allocations.

The modernization pass reserves SELECT batches, combines immutable descriptor
lists once and removes redundant XML/JSON capture copies. Ownership tests cover
independent sibling branches, caller-list mutation, shallow bound payloads,
SELECT replacement/reset and retained clones. Row and predicate construction use
private owned paths while public list inputs still copy. Tests preserve output,
argument order, invalid-parameter rejection and empty IN/NOT IN behaviour.
Both-style method checks cover prefixes, atomic failure, typed-nil receivers and
warmed rendering. Benchmarks use `testing.B.Loop`; concurrency tests use
`sync.WaitGroup.Go`, and error tests use `errors.AsType`. The refreshed corpus
report matches every earlier field except its generation timestamp, including
individual query outcomes and failure reasons.

No release or remote CI result is claimed.

## Repository cleanup — 2 October 2026

Historical reference suites, demo packages, their inventories and obsolete
validation snapshots were removed. The full frozen PostgreSQL corpus, its
provenance and upstream notice are unchanged. Native regressions retain dotted
conflict-column quoting and empty-condition identities. Shared assertions live
in `test_helpers_test.go`; projection-wrapper fixtures use generic identifiers
and fixed SQL/argument expectations.

Fresh checks passed:

- Full offline tests with an empty module cache, networking disabled and an
  invalid live-test DSN; all 28,097 verified corpus occurrences ran.
- Full race/shuffle tests with the same offline constraints.
- Vet across the default packages and the tagged integration package.
- Tagged integration compilation without executing database tests.
- Diff-scoped lint for the core and tagged integration packages: zero issues.
- Core statement coverage: 75.5%, after removing the historical reference suites.
- Local Markdown links and a repository scan for removed demo/reference material.

The database suite was not rerun for this cleanup; its latest executed baseline
is recorded above. No library API or implementation changed.

```sh
GOMAXPROCS=2 go test -count=1 -shuffle=on -p=2 -parallel=8 ./...
GOMAXPROCS=2 go test -race -count=1 -shuffle=on -p=1 -parallel=8 ./...
GOMAXPROCS=2 go vet -p=2 ./...
GOMAXPROCS=2 go test -tags postgres -run='^$' -p=2 ./integration
GOMAXPROCS=2 go vet -tags postgres -p=2 ./integration
```
