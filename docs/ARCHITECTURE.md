# Architecture and contracts

This is the architecture entry point. [API](API.md) describes construction,
[coverage](COVERAGE.md) defines the evidence and remaining syntax gaps,
[performance](PERFORMANCE.md) records allocation bounds, and
[validation](VALIDATION.md) records completed checks.
[PostgreSQL corpus support](POSTGRES.md) defines the pinned construction
measurement and its regression gate.

## Boundary

`Statement.ToSQL() (string, []any, error)` is the main boundary. `ToSQLWith`
selects rendering options per call. `AppendSQL` accepts caller-owned buffers;
`AppendWith` renders into them with explicit options. Rendering methods
forward to the free functions; one renderer owns options and output policy.
There is no executor interface, transaction factory, context parameter, scanning
API, model registry, dialect registry or runtime schema inspection.

The core package is independent of pgx. Live tests in `integration/` consume
its output with pgx directly and are excluded unless the `postgres` build tag
is enabled. One root module owns dependency versions; pgx is a test dependency.
Opted-in live tests require `QX_TEST_DSN` and own separate connections and schemas.
Schema cleanup uses a fresh bounded context before closing each connection. No reflection in qx is not a promise about reflection
inside a driver's codecs or a consumer's row-mapping helpers.

Regular Go tests own fixed SQL/argument expectations. The full pinned native
corpus retains every occurrence and construction gap under `internal/postgrescorpus`.
Its update tool independently compares source and rendered PostgreSQL syntax
trees before freezing outputs, and derives expected argument order from the
source tree. Runtime tests do no SQL parsing or generation. Eight test-only packages
own typed builders partitioned by occurrence ID modulo eight; fixed SQL and
provenance live in each package's JSONL file. A compact manifest owns counts,
source/parser pins, hashes and the complete strided ID inventory. A streaming
audit checks actual packages and combines shape coverage across shards.

Each shard decodes at most 256 records, runs a serial parent group with parallel
case children, and waits for those children before decoding the next group.
Builders are constructed after each child's `t.Parallel()` barrier. This bounds
retained case metadata and avoids compiling SQL/provenance as a giant Go
initializer. `-p=2` bounds concurrent package builds and test processes;
`-parallel=8` bounds case concurrency within each process. Sharding alone cannot
bound aggregate compiler memory. Race builds use `-p=1` to bound the additional
instrumentation cost. Make targets and CI also default to `GOMAXPROCS=2`,
limiting compiler and test scheduling on machines with many cores.
Allocation measurements and shared importer
subtests remain serial. There is no shared mutable query registry.

## Representation

The public `Expr` is a concrete tagged value. Identifiers, parameters and simple
literals do not need separately allocated expression-node objects. Binary and
other composite expressions reference small payloads. Heterogeneous clauses
store `[]Expr` rather than a hierarchy of user-implementable rendering callbacks.

`Condition`, `Order`, `Relation`, `Assignment`, `WithQuery`, `WindowSpec` and
`ConflictClause` represent SQL grammar roles. Some fields use `any` internally
for tagged payloads; dispatch is a type switch/assertion over package-owned
variants, not reflection. Typed `Field[T]`, `Null[T]`, `Optional[T]` and tuples
sit over the same renderer, not a second generic execution engine.

Finite grammar states use defined enum types and named constants, including
SQL/JSON behaviours and policies, table-column roles, SELECT quantifiers, CTE
materialization, identity overriding, set operators and window frames. Unknown
states fail validation rather than silently selecting a default. SQL constants
and keyword expressions have distinct tags. CYCLE admits unsigned constants,
inspecting through version gates
without removing them from the rendered expression; its inspection shares the
configured nesting bound.

`JSONDocument` and `JSONBDocument` also wrap `Expr`; their separate method sets
make JSONB-only operations unavailable on json. Marker generics were rejected:
Go cannot restrict methods to one instantiation, and shared methods would admit
operators PostgreSQL does not support on json. Text extraction returns
`Field[string]`, preserving existing operand typing without claiming non-null
results or knowledge of a document's schema. Explicit expression constructors
assert SQL types; encoded parameter constructors add SQL casts.

JSON selector policy belongs to json.go. Integer selectors are bounded to int4
and cast explicitly, avoiding PostgreSQL's text-key overload for unknown
parameters. Paths and key lists copy strings into owned expression lists and
render typed SQL arrays with individual binds; no driver array codec is needed.
JSONPath arguments cast to jsonpath. Existing expression traversal and cloning
own rendering, bounds, parameter numbering and nested-statement lifetime.

`Statement` and `Rowset` are sealed interfaces. SELECT/VALUES/TABLE/set operations
are rowsets; INSERT/UPDATE/DELETE/MERGE are statements even with RETURNING. A
modifying statement's RETURNING result can be referenced through a CTE.

Composite field selection, expansion and array indirection share expression
traversal. Assignment destinations render through a separate grammar role.
Explicit expression grouping preserves successive indirection boundaries;
adjacent subscripts remain one multidimensional operation. A single grouped
expression retains its projection-width information. Function-relation roots
render casts with CAST's keyword syntax, as required by that grammar role.
Expansion makes projection width unknown; explicit zero-column SELECT has known
width zero. INSERT owns one expression target list; string column names are
converted to that representation rather than stored in parallel.
Width inspection follows the set-operation spine iteratively and uses the
render's configured depth bound. Every width consumer uses this same inspector;
opaque projections remain unknown, and rendering rejects cycles or excessive
nesting.

Window definitions own local inheritance validation: a base must be defined
earlier, an inherited window cannot replace PARTITION BY or an existing ORDER BY,
and a framed window cannot be copied. Effective ordering controls GROUPS and
offset RANGE requirements through one frame-order rule. OVER references and
inline Base names remain server-resolved; the renderer has no global window
registry. Explicit exclusion is tracked separately from omission so unknown
values, including zero, cannot silently remove the clause.

SQL/XML and SQL/JSON constructors own concrete descriptors and applicable
policies. JSON input formatting is separate from output formatting. Aggregate
families share private FILTER/OVER traversal and cloning, while each family
owns its permitted modifiers. A universal option bag was rejected because it
would expose combinations absent from PostgreSQL's grammar. SQL type modifiers
are copied integer lists on named types; custom type semantics remain server
owned.

`JSONInputValue` marks the heterogeneous constructor input role. One concrete
type switch owns conversion and rejects nil or foreign embedded inputs. An exact
union generic was rejected because constructors accept mixed expression/formatted
inputs, expanded interface slices and empty argument lists.

Query destination, prepared-query and cursor commands are Statement-only
wrappers. SELECT INTO owns its destination separately and injects syntax through
a private rendering parameter without mutating its source Rowset. CTAS and INTO
share logical top-level CTE traversal with EXPLAIN; physical nesting limits stay
intact. Cursor and materialized-view sources retain stricter contexts. Separate
destination types expose only each command's permitted options. A persistent
INTO flag on SELECT was rejected because it would make a mutating command usable
as a Rowset. External parameters are rejected where PostgreSQL command processing
cannot bind them; the renderer never substitutes application values into SQL.

## Rendering

The renderer contains a SQL byte buffer, an argument slice, options, nesting
counters and the first structural error. It traverses in emitted SQL order.
Parameter nodes store values, not placeholder text or ordinals. Binding appends
the value and emits either `$` plus the argument index with `strconv`, or `?`,
according to the render's typed `PlaceholderStyle`. One outer renderer owns
options and argument order through every nested statement. Options are validated
at the `AppendWith` boundary and never stored on builders.

There is no second formatting pass: quoted identifiers, string literals, JSONB
operators and trusted raw SQL remain untouched. SQL-string rewriting was rejected
because it conflates parameter nodes with question marks in other grammar roles
and adds another pass over output. Formatter callbacks and a dialect registry
are unnecessary for the two supported styles. Question placeholders require a
compatible consumer; they do not change PostgreSQL syntax or execution semantics.

The dispatch entry point uses concrete statement types. This keeps the renderer
out of arbitrary public callbacks and lets the compiler keep the reusable-buffer
path allocation-free in the measured fixtures. Rendering does not allocate a
subquery SQL string, merge per-node argument slices, perform map sorting or
invoke `driver.Valuer`.

`ToSQL` supplies a small initial buffer and copies the finished bytes into an
owned string. Repeated `ToSQL` is not allocation-free. `AppendSQL`/`AppendWith`
can avoid new allocations after construction with preboxed values and adequate capacities.
See PERFORMANCE.md for the actual measured costs, not an unconditional promise.

## Mutability and lifetime

Statement builders mutate. Expressions and supporting SQL descriptors use
value-style immutable construction and copy supplied structural lists. Extending
an immutable list allocates its final combined storage once; captured expressions
may share those private lists because later extensions copy them. Mutable SELECT
lists retain capacity and reserve known batches before conversion. Private row
and membership constructors consume newly owned lists; public expression-list
inputs are copied at their boundary. Embedded
statement pointers remain live: changing a subquery changes its parent until
rendered. This allows cheap composition without hidden snapshots at every call.

`Clone` explicitly copies a complete reachable statement graph and expression
structure. Memoisation preserves shared subqueries and handles statement cycles.
Bound values are shallow references by design; arbitrary application values
cannot be generically deep-copied without imposing more semantics or reflection.
A cycle is preserved by Clone and rejected during rendering by the nesting limit.

Read-only renders may run concurrently once the graph is stable. Mutation and
Reset are not synchronised. Select.Reset clears retained expression/argument
references and reuses the main clause capacities; DML Reset releases its lists.
An outer builder that references a reset inner builder sees the reset state.

## Validation

Rendering detects malformed nodes, missing mandatory clauses, invalid identifiers,
known arity mismatches, duplicate assignments/CTEs, incompatible local modifiers,
invalid MERGE branches, configured feature gates and resource limits. Errors
wrap sentinels in `RenderError`, usable with `errors.Is` and `errors.As`.

This is structural validation, **not a PostgreSQL parser/planner or a schema
proof**. It cannot prove column existence, expression types, aggregate legality,
functional dependencies, privilege/RLS correctness, arbitrary operator support,
scalar-subquery row cardinality, or custom driver encoding. Raw projections have
unknown width. Type descriptors describe emitted SQL, not codecs.

The first error is returned. `ToSQL` discards partial output. `AppendSQL` returns
the original slices and their visible contents on failure, but bytes/reference
slots beyond their original lengths are not preserved. Appended argument slots
are cleared on failure to reduce unintended object retention.

`PostgreSQLVersion` names releases 12 through 18, and zero selects PostgreSQL18.
Unknown version values fail at the rendering boundary. The same enum is used for
all internal feature thresholds, including EXPLAIN options. Version checks cover
selected grammar/function features, not every server release change; arbitrary Call/raw fragments cannot
be version-checked. Parameter limits include an existing argument prefix. Depth
limits protect rendering from excessive nesting and accidental self-reference.

## Extension and security boundaries

Identifiers are always quoted as identifiers. `Col` splits dots; `Ident` treats
components literally. Values always bind unless a caller explicitly chooses a
literal constructor. Standard string literals use escaped E-string syntax.
`Operator` validates operator punctuation; textual custom grammar must use the
explicit unsafe fragment API. All arbitrary SQL fragments remain trusted code.

`Call` is for ordinary PostgreSQL functions, including schema-qualified custom
functions. SQL syntactic constructs such as COALESCE use dedicated helpers or
fragments, not a quoted user-function identifier.

A query builder is not an authorisation mechanism. Execute tenant queries through
the correct transaction and role. Do not infer safety from a WHERE clause or
`RequireWhere`; `WHERE TRUE` is still unrestricted. `TRUNCATE` is not filtered by
RLS. Keep DDL, tenant role changes, GUC setup and transaction lifecycle outside qx.

## Source map

| Area | Files |
|---|---|
| Rendering, errors, identifiers/types | render.go, errors.go, identifier.go, type.go |
| Expressions/predicates and generics | expr.go, literal.go, condition.go, pattern.go, row.go, tuple.go, null.go |
| Functions, aggregates, windows, CASE | function.go, window.go, case.go, order.go |
| Relations and query structure | relation.go, select.go, set.go, cte.go, tail.go |
| DML and conflict/returning rules | insert.go, update.go, delete.go, merge.go, assignment.go, conflict.go |
| PostgreSQL-specific expressions | postgres.go, json.go, sqljson.go |
| Utilities and ownership | utility.go, clone.go |
| Mechanical forwarding methods | fluent.go, statement_methods.go; generated with scripts/generate.py |
| Native construction fixtures | postgres_test.go, recipes_test.go, internal/postgrescorpus/ |
| Shared golden assertions | test_helpers_test.go |
| Invariants, placeholder styles and type errors | errors_test.go, ownership_test.go, render_test.go, safety_test.go, typed_test.go, fuzz_test.go |
| Public API examples and live integration | example_test.go, integration/ |
