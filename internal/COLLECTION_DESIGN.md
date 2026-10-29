# Construction safety and projection composition

Keep concrete grammar roles and required operands. Add `projection.Concat` to
compose complete expression/metadata pairs. Do not replace required-first
constructors with unrestricted variadics: their shorter dynamic callers admit
empty grammar completions that the existing signatures prevent.

This decision supplements the [18 query designs](QUERY_DESIGNS.md) and is linked
from the canonical [architecture entry point](ARCHITECTURE.md). Public API
compatibility was not a constraint. Prevention of incorrect construction was.

## Guarantees and their owners

| Invariant | Owner and enforcement |
| --- | --- |
| INSERT source selection cannot mix rows, SELECT, assignments and defaults | Distinct concrete source roles; unavailable methods fail to compile |
| Qualified JOIN requires completion; CROSS/NATURAL cannot acquire a qualifier | Pending and completed relation roles |
| Untargeted conflict cannot update; DO NOTHING cannot acquire an update predicate | Target and action roles |
| MERGE actions match their category; completed actions cannot change category | Category selectors and terminal action values |
| DEFAULT cannot enter ordinary expressions | Separate write values, checked parameter boundary |
| Required literal collections are nonempty; write tuples have at least two elements | Required-first/required-two signatures |
| Dynamic list contents, zero descriptors, row widths and live children are valid | Grammar owner during rendering; errors return no SQL/arguments, append failures preserve visible prefixes |
| Result expression and metadata retain their association during composition | One private ordered list of column pairs; Concat copies complete pairs |
| Columns exist, types/operators match, privileges permit execution | PostgreSQL and the application's schema/execution boundary |

These are different guarantees. A Go type with private fields can still be
zero-initialized. A nonempty list can contain zero expressions. A child query can
be mutated after attachment. No constructor signature proves database schema,
server state or arbitrary trusted SQL. The library therefore does not claim
that every representable Go value is a valid PostgreSQL query.

Successful rendering is the structural validation boundary, not a schema proof.
Builders and descriptors are construction inputs; callers must handle render
errors before execution. Revalidating live children at render time is necessary;
validation caches or a `Compile` wrapper cannot replace the static guarantees.

## Collection architectures compared

All alternatives used the same dynamic upsert with column, value, conflict-key
and assignment slices. The complete prototypes and fixed SQL/argument oracles
are retained in the [boundary experiment](ergonomics/README.md).

| Architecture | Ergonomics | Prevention and cost | Decision |
| --- | --- | --- | --- |
| Required-first concrete stages, explicit Slice methods where already supported | Literal calls are direct; dynamic conflict keys need a length guard and split | Prevents omitted required arguments; dynamic inputs still render-validated | Keep |
| Uniform variadic concrete stages | Same method for literals and expanded slices | Allows empty target/action/row/function completions; fewer caller lines do not compensate | Reject at required grammar boundaries |
| Slice-only stages | Direct dynamic inputs, slice literals for static calls | Still admits empty lists; makes literal calls longer | Reject |
| Validated nonempty collections | Four conversions and error checks in the upsert | Proves length only after successful construction; zero wrappers, elements and live graphs still need checks | Reject public generic wrapper for this boundary |
| Eager error-returning stages | Detects local defects earlier | Repeated error branches cannot remove graph validation; partial builder ownership needs another contract | Reject without a caller need for intermediate errors |
| Concrete operation specs | Compact aggregate construction | Optional/empty distinctions, mutually exclusive sources and slice ownership become spec rules | Reject a second representation of existing roles |

Four executable prototypes passed 49 probes against the frozen pre-change
baseline: fixed output, copied slice positions, nil/empty inputs, invalid
assignments and width mismatch. These establish observable boundary behavior,
not equivalent compile-time prevention. The unrestricted variadic adapter accepts
empty calls that the baseline cannot compile.

For this one dynamic caller, nonblank source lines were 7 for the baseline, 4
for unrestricted variadics, 19 for validated lists and 4 for the spec. Additional
adapter bodies were 0, 39, 14 and 14 lines respectively. These are disclosed
source counts, not usability scores or performance measurements. They do not
justify relaxing an invariant to improve a metric.

Existing `ColumnsSlice`, `ValuesSlice` and `DoUpdateSlice` remain explicit dynamic
boundaries. They copy supplied positions and reject empty selected input during
rendering; empty input never means omitted targets, DEFAULT VALUES or DO NOTHING.
No new Slice family or compatibility wrapper is added. Ordinary conditional Go
owns optional clauses and application no-op policy.

## jOOQ and Go sources

[jOOQ's dynamic SQL guidance](https://www.jooq.org/doc/latest/manual/sql-building/dynamic-sql/)
constructs reusable expressions and collections independently of the fluent
chain. Its [step-interface documentation](https://github.com/jOOQ/jOOQ/blob/main/jOOQ/src/main/java/org/jooq/SelectSelectStep.java)
warns clients about depending on intermediate interfaces. These support small
composable Go values, not reproducing the Java interface hierarchy.

jOOQ's context-dependent `noCondition` sentinel is not adopted. In qs, an omitted
filter is an explicit branch; a zero condition must not silently remove a
predicate. Schema generation, as used by jOOQ and Jet, can establish additional
schema relationships but introduces generation/freshness obligations. sqlc is a
separate SQL-first choice for known query shapes. Squirrel, goqu, Bob and Bun show
other collection/mutation conventions; their syntax does not establish qs's
ownership or grammar guarantees. The [primary-source comparison](ergonomics/sources.md)
records inspected sources and distinguishes observations from local conclusions.

## Selected composition API

```go
p := projection.Concat(userColumns(user), userColumns(manager))
q := qs.Select(p.Expressions()...).FromExpr(
    qs.LeftJoin(user, manager).On(user.Col("manager_id").EqExpr(manager.Col("id"))),
)
```

A relation-taking factory creates correctly qualified expressions at their
source. It avoids attempting to rewrite qualifiers inside arbitrary expression
graphs. `Concat` joins existing projections; `With` adds individual columns.
Neither interprets metadata, removes duplicates nor guesses aliases. Empty
projections contribute nothing. Column positions are owned; nested queries,
bound values and metadata contents retain the documented shallow lifetime.

Before Concat, callers had to extract expressions and metadata separately,
reconstruct paired columns by index, then call With. Concat removes that indexing
obligation and intermediate buffers while keeping one authoritative paired list.
The [performance report](PERFORMANCE.md) records measurements against that
baseline, including composition followed by SELECT and metadata extraction.

## Verification

The selected API retains the existing static role/cardinality contracts. Six
additional compile-negative callers cover missing Coalesce/Greatest/Least,
SetRow and MERGE insertion operands. Each has a valid companion caller and
checks the missing-argument diagnostic category rather than accepting any error.
Concat tests check literal SQL/arguments, paired metadata order, duplicates,
empty inputs, slice-position independence, live children and shallow metadata.
Executable examples cover the relation-taking self-join factory and absent,
present-NULL and present-value patches with application-owned no-op handling.

QA assessed the coherent test batches before edits. The architecture advisor
recommended retaining required operands under the stronger prevention constraint.
Verification with Go 1.27.3 passed `make test`, `make race`, `make vet`,
`make generate-check` and `make golangci-lint` (root and tagged integration
module). Root lint also passed after pruning unselected benchmark fixtures.
The existing gopls parser rejects the unchanged generic method in `fluent.go`
and reports a stale removed-test diagnostic; on-disk compiler, vet and lint
results provide the available semantic verification.

Verification covers the cumulative change set, including SELECT lock validation,
expression payloads, identifier quoting and projection APIs. The full PostgreSQL
18.6 integration suite was rerun with `-race -count=1`, including
`integration/production_patterns_test.go`; it passed with no skipped cases.
The [project review](PROJECT_REVIEW.md#validation-and-review) records execution
coverage and its limits. Benchmark measurements and reproducible artifacts belong
to the [performance report](PERFORMANCE.md).

The completion critic found no material issue in the final code, ownership
contracts, prevention claims or retained measurement evidence. That review was
read-only and did not independently execute checks. The original projection
composition comparison passed its two gates; all ten pairs are retained. The
later structural window-lock check has an unresolved CPU bound on three warm
workloads. The performance report separates that result from the composition
and expression-layout measurements.
