# Architecture

This is the architecture entry point. [README.md](../README.md) owns public usage
and caller contracts; the [corpus README](postgrescorpus/README.md) owns
construction-support measurement and fixture maintenance.

## Boundaries and representation

The core constructs PostgreSQL SQL and arguments. It has no execution, scanning,
transaction, schema-discovery or dialect layer. The standard library is its only
runtime dependency. Tagged integration tests use pgx directly; callers own
connections, transactions, argument encoding and database authorization.

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

SQL grammar owners enforce local policy: JSON integer selectors are int4-bounded
and cast to avoid PostgreSQL's unknown-parameter text overload; input/output JSON
formats remain distinct. Assignment destinations have their own grammar.
Parenthesized indirection preserves successive indexing and projection width.
Window definitions validate local inheritance and effective frame ordering.
Unknown widths remain unknown; all width consumers share a bounded inspector.

## Rendering and lifetime

One renderer owns buffers, arguments, options, limits and the first error. It
traverses in emitted SQL order, including nested statements. Parameter nodes bind
values and emit their placeholder; identifiers, literals, operators and trusted
fragments are never rewritten. SQL-string placeholder replacement was rejected
because question marks also occur in PostgreSQL grammar. Rendering never calls
`driver.Valuer`, resolves a schema or independently renders subquery strings.

Options belong to each render. Syntax owners enforce selected feature/version
thresholds. Parameter limits include prefix arguments; depth limits bound nested
traversal and reject cycles. `RenderError` preserves context and wraps sentinels.
`ToSQL` discards partial output on failure. Append methods preserve original slice
lengths and visible prefixes, clear appended argument references, and may change
unused backing-array capacity.

Builders mutate; structural descriptors use value-style methods and own supplied
lists. Private paths may consume freshly owned lists without another copy.
Nested statement pointers stay live. `Clone` copies the reachable graph with
memoization, preserving shared children and cycles, but bound application values
remain shallow. Automatic attachment snapshots add composition costs; validation
caches would need invalidation for every live mutation.

Read-only renders can run concurrently once the graph and bound values are
stable. Mutation and Reset are unsynchronized. SELECT Reset clears retained
references while reusing clause capacity; DML Reset releases its lists. A parent
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
custom driver encoding. Raw projections have unknown width. General `Expr` can
still place DEFAULT in a forbidden context; this remains a validation gap.

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
| 3 | INSERT source | Defer an exclusive source variant; stages sharing one mutable pointer do not enforce exclusivity. |
| 4 | Join qualification | Pending qualifiers, completed CROSS/NATURAL; reject general qualifier methods and interface boxing. |
| 5 | Conflict target/action | Defer concrete selectors; inference and action predicates have different grammar. |
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
| 20 | DEFAULT context | Defer expression-context checks; a standalone token complicates heterogeneous expression lists. |
| 21 | Query degree | Bounded width inspection; stars, composite expansion and live rowsets prevent universal static arity. |
| 22 | CYCLE constants | Keep bounded inspection; capability bits require fragile propagation through wrappers. |
| 23 | Identifier paths | Explicit literal/path constructors; eager owned path slices add allocation without schema proof. |
| 24 | SELECT quantifier | Favor a local variant over whole-builder typestate. |
| 25 | RETURNING | Defer one projection/alias descriptor; preserve OLD/NEW roles and version gates. |
| 26 | JSON formats | Keep input/output roles; staged constructors must not exclude mixed or empty inputs and encodings. |
| 27 | Utility parameters | Traverse for binds; literal-only types wrongly exclude parameter-free function expressions. |
| 28 | Ordered-set calls | Narrow known helpers; custom function classification stays server-owned. |
| 29 | Version ownership | Syntax owners and per-render options; reject global registries and version-bound graphs. |
| 30 | Live/frozen graphs | Explicit Clone; reject automatic snapshots, persistent-builder rewrites and mutation-sensitive caches. |
