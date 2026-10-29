# Query and projection design comparison

This review compares 18 designs for dynamic PostgreSQL query construction and
result declarations. Correctness is a gate; ergonomics and measured CPU/heap
cost then determine the choice. The baseline is the working tree after the
[production-query review](PROJECT_REVIEW.md), including identifier optimization,
not the earlier HEAD commit. Measurement provenance is recorded separately.

## Workload and selection rules

The target caller builds filtered document pages, composes CTEs, inserts batches,
and reuses expression/metadata declarations. Construction, owned rendering,
reusable rendering and projection extraction are separate lifecycles. The
measurement set includes a realistic CTE and 100-row INSERT, plus projection
extraction both alone and followed by SELECT construction.

SQL, parameter order, errors, owned structural positions, shallow application
values, live nested builders and graph cloning must remain correct. Alternatives
with different lifetimes can be useful, but their speed is not a like-for-like
comparison. Public API changes are permitted when they simplify the caller's
complete task; compatibility alone is not a reason to retain a design.

## Designs explored

CPU/allocation mechanisms below are hypotheses unless linked to measurements.
These are design comparisons, not 18 implemented or benchmarked libraries.

| # | Design | Caller ergonomics | Cost mechanism | Correctness/ownership trade-off | Selection |
|---|---|---|---|---|---|
| 1 | Mutable tagged AST | SQL-shaped calls and ordinary conditional Go | Reuses builder storage; existing warm rendering allocates nothing | Mutation explicit; children live; Clone separates graphs | Keep as dynamic-query representation |
| 2 | Immutable whole-clause copies | Branch without explicit Clone | Changed clauses copy storage; chained growth can repeatedly copy prefixes | Immutable outer clauses do not freeze children | Reject as a CPU/allocation improvement |
| 3 | Persistent linked/tree clauses | Cheap branching from shared bases | Additional nodes, pointer traversal and flattening | Ordering, scope and bounded traversal remain necessary | Defer without branch-heavy workload evidence |
| 4 | Query-modifier pipeline | Reusable filter bundles | Capture/dispatch costs atop a representation | Modifier ordering and conflicting clauses need policy | Ordinary Go helpers already compose qs clauses |
| 5 | Schema-generated typed AST | Column/type completion | Generation still leaves runtime expression construction | Adds schema drift and generation workflow | Suitable separate companion, no core replacement |
| 6 | SQL-first generated functions | Small typed API for fixed endpoints | Static SQL removes runtime AST/render work | Dynamic shapes need separate query variants | Prefer in applications with permanently fixed SQL |
| 7 | Direct one-pass emitter | Compact fixed-order assembly | Can eliminate AST objects | Later replacement, reuse, live children and atomic errors become harder | Reject as general qs replacement |
| 8 | Compiled/frozen template with bind slots | Convenient repeated shapes | Amortizes traversal/validation | Must define freeze, typed slots, options, prefixes and lifetime | Defer pending repeated-shape workload evidence |
| 9 | Arena/indexed AST | Explicit query storage owner | Fewer heap objects and potentially better locality | Arena identity/lifetime and importing foreign nodes burden composition | Reject for current dynamic-composition workload |
| 10 | Right-sized payloads through one slot | No caller change | Unary nodes store one child; all composites share the existing tagged payload slot | Update traversal and generated Clone together; measure typed-payload access | Selected and implemented |
| 11 | Fixed-arity function payloads | No caller change | Combine common short argument lists with node storage | Larger nodes may penalize zero/large arity | Defer unless profile supports it |
| 12 | Inline small clause buffers | No caller change | Avoid initial slice allocation | Larger builders, self-referential storage, Reset retention | Lower priority than smaller payloads |
| 13 | Two-pass exact-size rendering | No caller change | Avoid output growth, but traverse twice | Avoid duplicate validation and observations of mutable state | Reject without large owned-output evidence |
| 14 | Caller-owned SQL/argument buffers | Familiar explicit reuse | Existing AppendSQL avoids output allocation with adequate capacity | Caller owns buffers through execution; prefixes survive failure | Keep existing API |
| 15 | Caller-owned projection append | Familiar append idiom | Avoid mandatory intermediate extraction slices | Copy positions, keep contents shallow; expose no internal buffers | Selected and implemented |
| 16 | Projection iterator/indexed access | Convenient for streaming consumers | Avoid extraction storage | SELECT/RETURNING still need slices; adaptation may erase savings | Defer until a real streaming consumer exists |
| 17 | Projection structure-of-arrays/borrowed views | Direct slice handoff | Borrowing removes copies | Borrowing permits mutation of declarations; owned outputs retain copies and require more construction buffers | Reject borrowed views |
| 18 | Renderer/builder pooling | Hides reuse | May amortize some allocations | Retained arguments, reset completeness, contention, capacity bounds | Reject before exhausting explicit caller reuse |

An additional fixed-arity shortcut was considered: encode two-condition AND/OR
groups as ordinary binary expressions. It saves a slice box but loses indexed
operand error paths and changes indirection parenthesization. It is rejected in
that form; an allocation reduction does not justify degraded diagnostics.

## Project evidence

These primary sources anchor the design alternatives. No external project was
benchmarked against qs; differences in execution, scanning and validation would
otherwise make the comparison misleading.

- [Squirrel SELECT builder](https://github.com/Masterminds/squirrel/blob/1ded5784535dcffa4e175d4efbd1ca2706927758/select.go#L162-L181): generic builder representation with value-style fluent calls.
- [goqu dataset copies](https://github.com/doug-martin/goqu/blob/21b6e6d1cb1befe839044764d8ad6b1c6f0b5ef4/select_dataset.go#L200-L241): immutable dataset changes.
- [Bob's layers](https://github.com/stephenafamo/bob/blob/02f57ef504662b042a5dc182d39a3ce349043ce2/README.md#L54-L86): query modifiers and schema-generated typing.
- [sqlc workflow](https://github.com/sqlc-dev/sqlc/blob/3ad8b20fb02b5bea81591b673da0758b8685d951/README.md#L1-L12) and [generated caller](https://docs.sqlc.dev/en/latest/howto/select.html): fixed SQL produces typed Go operations.
- [Jet design](https://github.com/go-jet/jet/blob/d18395ad6a34916c28748f8105e05ce826a852e7/README.md#L51-L64): schema-generated expressions and result mapping.
- [Bun query state](https://github.com/uptrace/bun/blob/3c2bd0543e9390225f73bba4004d789d10c25f31/query_base.go#L97-L116): mutable queries integrated with database/model concerns.

## Caller comparison

For a one-shot dynamic page, retain the owned-output path:

```go
q := qs.Select(declaration.Expressions()...).From("documents").
    Where(qs.Eq("tenant_id", tenantID), qs.Gt("id", afterID)).
    OrderBy(qs.Asc("id")).Limit(20)
sql, args, err := q.ToSQL()
```

The append candidate removes intermediate extraction allocation when a caller
already owns reusable buffers:

```go
columns = declaration.AppendExpressions(columns[:0])
q := qs.Select(columns...).From("documents").
    Where(qs.Eq("tenant_id", tenantID), qs.Gt("id", afterID)).
    OrderBy(qs.Asc("id")).Limit(20)
sql, args, err = q.AppendSQL(sql[:0], args[:0])
```

In both cases handle `err` before execution. SELECT still copies expression
positions; construction is not allocation-free. The caller retains SQL and
arguments until execution finishes. Metadata extraction follows the same append
ownership contract. Ordinary owned extraction remains the simpler default.

For a fixed endpoint, SQL-first generation removes query construction entirely:

```sql
-- name: ListDocuments :many
SELECT id, title FROM documents
WHERE tenant_id = $1 AND id > $2
ORDER BY id LIMIT 20;
```

That design trades arbitrary runtime clause composition for a schema/codegen
workflow. It is a valid application choice rather than a faster implementation
of all qs contracts.

## Decision and verification

Selected designs **1 + 10 + 14 + 15**: one mutable AST and renderer, right-sized
payloads, existing SQL buffer reuse, and projection append extraction. Every
composite uses the existing value slot. Removing the separate binary/subscript
node pointer reduces `Expr` from 48 to 40 bytes on the measured platform.
Constructors, rendering, assignment normalization, width inspection and generated
cloning use one representation per kind. The single-slot follow-up was measured
against the tree after projection composition, separately from the original
unary-node experiment; the performance report records both baselines.

`AppendExpressions` and `AppendMetadata` preserve prefixes and shallow contents;
empty projections return the destination unchanged, including nilness. Existing
owned extraction remains available. This combination improves the measured
dynamic workload without schema generation, a second renderer, cache invalidation
or additional graph lifetime rules.

Owned extraction keeps exact-sized allocation; append extraction grows caller
storage. Routing owned extraction through append-with-nil was rejected by the
predeclared CPU gate. These short loops have different allocation contracts and
share the same authoritative paired column list. The [performance report](PERFORMANCE.md#query-and-projection-design-comparison)
owns timings, confidence intervals, workload definitions and retained evidence,
including the rejected implementation. These are library measurements, not
database throughput results.

QA reviewed the tests before edits. The architecture advisor favored these
ownership boundaries and identified the rejected binary-node diagnostic change.
The completion critic inspected representation consumers, cloning and projection
ownership without independently executing tests and found no material issues.

Final verification with Go 1.27.3 passed:

- `make test`, including the complete PostgreSQL construction corpus.
- `make race` and `make vet`.
- `make generate-check`.
- `make golangci-lint`, root and tagged integration module, zero issues.
- Full PostgreSQL 18.6 integration suite with `-race -count=1`.
- `git diff --check`.

The linter required a local constant for the generator's repeated unary payload
tag; generated output and measured runtime library code were unchanged. gopls
reported its existing generic-method parse error in unchanged `fluent.go`; the
compiler, tests and linter supply semantic verification. The temporary database
was stopped after testing. PostgreSQL 12–17 execution and production throughput
remain unverified locally.
