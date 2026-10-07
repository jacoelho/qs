# Architecture

This is the architecture entry point. [README.md](../README.md) owns public usage
and caller contracts; the [corpus README](postgrescorpus/README.md) owns
construction-support measurement and fixture maintenance. The
[performance report](PERFORMANCE.md) records correctness, diagnostics,
construction and clone measurements, including unmet CPU budgets.

The [projection package documentation](../projection/doc.go) owns the result
declaration contract.

## Boundaries and representation

The core constructs PostgreSQL SQL and arguments. It has no execution, scanning,
transaction, schema-discovery or dialect layer. Its module has no third-party
dependencies. The separate `integration/` module owns pgx and replaces its qs
dependency with the local checkout. Tagged database tests run and lint through
that module; root module tests need no database driver. Callers own connections,
transactions, argument encoding and database authorization.

The `projection` companion imports qs; the core never imports it. One private
ordered column list owns each expression/opaque-metadata association. Construction,
extension and extraction own slice positions, while nested graphs and application
objects remain shallow. `Named` delegates aliasing to `Expr.As` and records the
declared string; generic metadata is never interpreted, validated or invoked.
The collection accepts empty declarations and leaves usability to the consuming
query context. It proves neither result width nor whole-query mapping correctness.
Callers own one-column inputs, SQL scope, complete output attachment and result
interpretation. Alias replacement, uniqueness validation, query wrappers and
driver-specific scanning would introduce different owners and were rejected.
Driver-independent [examples](../projection/example_test.go) live alongside the
package and run with its tests. No mapper dependency or separate example module
is needed.

`Statement` and `Rowset` are sealed interfaces. SELECT, VALUES, TABLE and set
operations are rowsets; DML is Statement-only even with RETURNING. Query utility
wrappers retain their source rowset and own destination options. SELECT INTO
cannot mutate its source into a non-rowset command. Parameter-free command
contexts reject bound parameters through traversal, never SQL interpolation.

`Expr` is a concrete tagged value. Composite nodes reference package-owned
payloads; heterogeneous clauses store concrete expressions rather than public
rendering callbacks. Typed fields, tuples and JSON documents use the same
representation and renderer. Go payload types do not prove SQL types or schema
validity. Finite grammar states use named enums; unknown states fail validation.
Constructors own tag/payload agreement; one checked private accessor enforces
that invariant. Public nil and foreign inputs retain boundary validation.

CASE uses distinct searched/simple builders with inline private payloads. `End`
stamps the role and snapshots branches. MERGE category selectors produce terminal
`MergeWhen` values; identity overriding precedes non-default insertion. Qualified
joins remain `PendingJoin` until completion owns a nonempty predicate/column list
and allocates the final payload. Distinct private field names prevent explicit
cross-role conversions. Zero values and unchecked names still require rendering
validation. A universal modifier bag was rejected because it admits illegal
method combinations; interface-backed completion would add boxing.

Write destinations own `WriteValue` and `WriteRowValue`. Their distinct private
fields prevent conversion into general expressions and tuple wrappers. `Value`
binds application data, including nil, through the existing parameter boundary;
its result type carries no application-type parameter. A generic constructor
would prove no additional invariant and prevent inference for `Value(nil)`.
The constructor uses the existing parameter representation and adds neither a
value buffer nor another interface layer. `Write` captures an ordinary expression;
`Default` constructs a token usable only at a direct destination position.
Ordinary expressions retain their renderer even
inside a write. The parameter boundary rejects write descriptors while leaving
application values shallow and available through `Value(value)`.
Assignment reuses one value slot; its existing row discriminator selects row
rendering. Explicit scalar targets preserve their complete path; typed Field and
JSON helpers remove the relation qualifier while retaining field/subscript steps.
Row writes own one element buffer and preserve tuple versus ROW syntax.

`InsertTarget` owns only the table descriptor. Optional `Columns` or `Targets`
selection creates `InsertColumnsTarget`, which captures one exactly sized target
list and exposes only rows, SELECT and default sources. Required-first methods
prevent empty target lists; `ColumnsSlice` instead records explicit selection in `insertBase.targetsSelected` and rejects empty
lists when rendered. This flag survives cloning independently of slice nilness.
Assignment INSERTs start directly from `InsertTarget`.
Neither target selector is a statement or rowset.

Each source selection creates an independent `InsertRows`, `InsertSelect`,
`InsertAssignments` or `InsertDefaults`. These are statements, never rowsets.
Each role owns its mutable source and clause storage; private helpers own common
INSERT rendering policy. Selected target lists are structurally immutable and
shared between completions without a buffer copy. Their expressions can retain
live child statements; generated cloning still traverses the list and graph.
`ValuesSlice` copies each row slice and retains empty rows as invalid input;
later rows cannot erase that failure. Completed builders cannot change column
lists, switch source roles or reset.
Defaults omit identity overriding. Supporting both column orderings was rejected
because it would add target mutation and copying rules without a requested benefit.

Conflict selectors distinguish unrestricted, inference and constraint targets.
Completion produces `ConflictNothing` or `ConflictUpdate`; only updates have an
action predicate. Generic OnConflict methods accept the exact completed value
types and normalize into one private optional payload. No interface is retained.
The generic method cannot satisfy a nongeneric consumer interface method.

Targeted selectors keep `DoUpdate(first, rest...)` for statically nonempty calls.
`DoUpdateSlice` accepts dynamic lists on column, index and constraint selectors;
it copies the assignment slice and uses the same conflict representation and
assignment validation. Nil and empty lists fail rendering with `ErrInvalid`,
never become `DO NOTHING`. Nested queries remain live and bound application
values remain shallow. A separate public nonempty-list type was rejected because
it adds construction and zero-value rules without simplifying this boundary.
`SetAllExcluded` treats column names literally on both assignment sides, matching
INSERT columns and conflict column names; explicit `Assign` owns field/subscript
targets.

SQL grammar owners enforce local policy: JSON integer selectors are int4-bounded
and cast to avoid PostgreSQL's unknown-parameter text overload; input/output JSON
formats remain distinct. Assignment destinations have their own grammar.
Parenthesized indirection preserves successive indexing and projection width.
Window definitions validate local inheritance and effective frame ordering.
Unknown widths remain unknown; all width consumers share a bounded inspector.
Immutable row-list membership widths are checked at construction; live subquery
widths are checked at rendering. Relation columns append a literal identifier
component to a separately parsed table path, preserving the existing identifier
representation.
Row-width errors identify one-based row positions and expected/actual counts,
never bound data. Unknown rows do not erase the preceding known width.

`LikePrefix`, `LikeSuffix`, `LikeContains` and their case-insensitive variants
own literal-match escaping: one pass escapes `!`, `%`, and `_`, then adds the
leading or trailing `%` required by the operation. The pattern is bound and the
fixed escape is emitted with `LiteralString("!")`. Empty suffix and contains
inputs intentionally match every non-NULL text value; callers decide whether an
empty filter should be omitted. Existing `Like` and `ILike` retain raw-pattern
semantics. Top-level helpers delegate to the `Expr` methods so the pattern
builder and `ESCAPE` policy have one owner without a new node representation.

## Rendering and lifetime

One renderer owns buffers, arguments, options, limits and the first error. It
traverses in emitted SQL order, including nested statements. Parameter nodes bind
values and emit their placeholder. `Param(any)` retains dynamic value types
and admits untyped nil; a generic type parameter would establish no additional
invariant. Parameters remain shallow; identifiers, literals, operators and trusted
fragments are never rewritten. SQL-string placeholder replacement was rejected
because question marks also occur in PostgreSQL grammar. Rendering never calls
`driver.Valuer`, resolves a schema or independently renders subquery strings.

Options belong to each render. Syntax owners enforce selected feature/version
thresholds. Parameter limits include prefix arguments; depth limits bound nested
traversal and reject cycles. `RenderError` preserves context and wraps sentinels.
Its error-owned path records
static structural scopes, outermost first, with one-based indexes. Indexed child
scopes close immediately; only the transition to the first error adds a step.
Labels are formatted on failure, accumulated during unwinding, and reversed once
at the append boundary. This avoids successful-render path allocation, shared
scratch state, and a second traversal. The path is not a complete syntax tree;
error text is presentation rather than a machine-readable contract.
`ToSQL` discards partial output on failure. Append methods preserve original slice
lengths and visible prefixes, clear appended argument references, and may change
unused backing-array capacity. Cleanup covers written slots in both the original
and final argument arrays. Its bound depends on the renderer appending arguments
without shortening their length.

Builders mutate; structural descriptors use value-style methods and own supplied
lists. Private paths may consume freshly owned lists without another copy.
Nested statement pointers stay live. `Clone` copies the reachable graph with
memoization, preserving shared children and cycles, but bound application values
remain shallow. Automatic attachment snapshots add composition costs; validation
caches would need invalidation for every live mutation.

The standard-library clone generator discovers statement roots and traverses
actual Go field types. Generated walkers register statement identities before
recursion. One semantic policy classifies erased expression/JSON payloads and
atomic values; it checks payload types as well as variant tags. Unknown roots,
interfaces, maps and application-owned pointers fail generation. Atomic
exemptions have checked shapes so new fields require review. Generation removes
manual traversal maintenance, not runtime copying. Check mode detects stale
output without rewriting it.

Read-only renders can run concurrently once the graph and bound values are
stable. Mutation and Reset are unsynchronized. SELECT Reset clears retained
references while reusing clause capacity; other resettable DML releases its lists.
INSERT starts another completed builder. A parent
referencing a reset child observes the reset state. Callers own buffer reuse and
bound-value lifetime through execution.

Reusable rendering can avoid allocations with preconstructed nodes, stable
interface values and adequate destination capacity. Construction, cloning, owned
output and failures have separate costs. Benchmark and allocation tests own
measurable budgets; there is no universal zero-allocation promise.

## Validation and security limits

Structural checks cover malformed nodes, required clauses, identifiers, known
widths, duplicate assignments/CTEs, modifier conflicts, branch reachability and
configured limits. They cannot prove column existence, SQL types, privileges,
aggregate legality, functional dependencies, scalar-query row cardinality or
custom driver encoding. Raw projections have unknown width. Zero write values,
nil rowsets, names, unknown widths and row assignments supplied to INSERT
assignment sources retain runtime checks. The write roles prevent DEFAULT from
entering general expressions through the public typed API; trusted SQL remains
an intentional escape.

Identifiers are quoted; values bind unless an explicit literal is selected.
Unsafe fragments are trusted code. `RequireWhere` is a structural guard and
accepts WHERE TRUE; it is not authorization. Transactions, roles, RLS and tenant
context belong to the execution boundary. TRUNCATE is not filtered by RLS.

## Evaluated API boundaries

These 30 evaluations record selected representations and constraints. Deferred
alternatives are not supported APIs or implementation commitments.

| # | Boundary | Decision or constraint |
|---|---|---|
| 1 | NULL/absence | Keep public containers; false validity binds NULL. Opaque accessors have lower payoff. |
| 2 | CASE | Separate concrete branch roles; reject sticky misuse state. |
| 3 | INSERT source | Immutable column selection before source completion; independent concrete roles; reject shared mutable stages and competing source flags. |
| 4 | Join qualification | Pending qualifiers, completed CROSS/NATURAL; reject general qualifier methods and interface boxing. |
| 5 | Conflict target/action | Concrete selectors and completed actions; exact generic union, normalized optional payload. |
| 6 | MERGE | Category selectors and terminal branches; pre-action override avoids an extra completion wrapper. |
| 7 | Window frames | Keep dynamic pair/inheritance checks; elaborate bound typestate cannot establish effective ordering. |
| 8 | Pagination | Defer one LIMIT/FETCH variant; preserve conflict errors instead of silent replacement. |
| 9 | SEARCH/CYCLE | Defer complete-name constructors; recursion, names and unsigned constants remain render concerns. |
| 10 | SQL/JSON policies | Favor separate function roles over universal behavior frameworks; defaults and formatting stay dynamic. |
| 11 | Assignment target | Defer dedicated destinations; wrapping arbitrary Expr merely relocates validation. |
| 12 | Render options | One normalization boundary; reject another options framework. |
| 13 | Physical DML target | Defer a table-only role; one owner must enforce aliases and operation-specific ONLY rules. |
| 14 | Mutation predicate/cursor | Defer one exclusive variant; duplicating fluent stages adds state complexity. |
| 15 | Function/aggregate role | Custom catalog classification belongs to PostgreSQL; avoid universal call typestate. |
| 16 | Window reference/copy | Keep distinct paths; a global registry introduces scope and ownership errors. |
| 17 | JSON_TABLE columns | Defer concrete completion roles; heterogeneous interfaces add storage costs without removing dynamic policies. |
| 18 | XMLTABLE columns | Keep compact terminal storage; preserve value-column PATH, DEFAULT and nullability. |
| 19 | CTE body | Defer sealed rowset-or-DML capability; nil and top-level placement still need validation. |
| 20 | DEFAULT context | Opaque concrete write values; direct destination rendering, no general-expression conversion. |
| 21 | Query degree | Bounded width inspection; stars, composite expansion and live rowsets prevent universal static arity. |
| 22 | CYCLE constants | Keep bounded inspection; capability bits require fragile propagation through wrappers. |
| 23 | Identifier paths | Explicit literal/path constructors; eager owned path slices add allocation without schema proof. |
| 24 | SELECT quantifier | Favor a local variant over whole-builder typestate. |
| 25 | RETURNING | Defer one projection/alias descriptor; preserve OLD/NEW roles and version gates. |
| 26 | JSON formats | Keep input/output roles; staged constructors must not exclude mixed or empty inputs and encodings. |
| 27 | Utility parameters | Traverse for binds; literal-only types wrongly exclude parameter-free function expressions. |
| 28 | Ordered-set calls | Narrow known helpers; custom function classification stays server-owned. |
| 29 | Version ownership | Syntax owners and per-render options; reject global registries and version-bound graphs. |
| 30 | Live/frozen graphs | Explicit generated graph copying; reject reflection, serialization, automatic snapshots, immutable-subtree sharing and mutation-sensitive caches. |
