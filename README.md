# Querysmith (qs)

PostgreSQL query construction for Go 1.27. Module `github.com/jacoelho/qs`, package
`qs`. The core uses only the standard library and has no execution, scanning,
connection pool, reflection, unsafe code or schema-discovery layer.

## Usage

```go
import qs "github.com/jacoelho/qs"

query, args, err := qs.SelectCols("id", "name").
    From("users").
    Where(qs.Eq("active", true), qs.IsNull("deleted_at")).
    OrderBy(qs.Desc("created_at"), qs.Desc("id")).
    Limit(20).
    ToSQL()
```

```sql
SELECT "id", "name" FROM "users"
WHERE ("active" = $1) AND ("deleted_at" IS NULL)
ORDER BY "created_at" DESC, "id" DESC LIMIT $2
```

Arguments: `[]any{true, 20}`. Check the error, then execute with pgx or a
`database/sql` driver. The caller owns codecs, transactions, result scanning,
roles, RLS and tenant context. `RequireWhere` is a structural guard, not
authorization; WHERE TRUE passes it, and TRUNCATE is not filtered by RLS.

Use `go doc .` for the exported API and [examples](example_test.go) for executable
usage. [Architecture](internal/ARCHITECTURE.md) owns internal invariants and design
decisions; the [corpus guide](internal/postgrescorpus/README.md) owns support
measurement, provenance and fixture regeneration.

## Construction contracts

- `Select` accepts expressions; `SelectCols` accepts column names. `SelectSQL`
  accepts trusted projection syntax without parsing it or inferring width.
- `Col("a.b")` splits a qualified path; `Ident("a.b")` quotes one identifier.
  `Col("id::text")` is a name; use `Col("id").Cast(Text)` for a cast.
- Values bind. Expression-valued methods such as `EqExpr` and `ValuesExpr` accept
  expressions; passing an expression to a value-binding method fails rendering.
  UnsafeSQL and raw fragments accept trusted code, never interpolated input.
- `Field[T]` constrains Go operands and assignments; it does not prove schema,
  SQL type, codec support or nullability. JSON/JSONB helpers preserve distinct
  document/operator roles. Encoded JSON parameters add casts, not marshaling.
- `Null[T]` distinguishes SQL NULL from zero; `Optional[T]` distinguishes absence
  from presence. Check optional presence and use explicit nullable bind helpers.
  Plain equality never becomes IS NULL automatically.
- Empty IN is FALSE, NOT IN is TRUE, And is TRUE and Or is FALSE. A zero Condition
  is invalid. Nonempty NULL-containing membership retains PostgreSQL semantics.

`InnerJoin`, `LeftJoin`, `RightJoin` and `FullJoin` return PendingJoin. Complete it
with On, Using or UsingAs before aliasing, nesting or passing it to FromExpr.
ON requires a first condition; USING requires a first column. Check empty dynamic
lists before indexing the first element. CROSS and natural joins are complete.

Case accepts condition branches through When; CaseOf accepts operand comparisons
through WhenValue. End snapshots branches and the fallback. MERGE match selectors
expose category-appropriate actions; choose conditions and identity overriding
before completion. Completed MergeWhen values have no action/modifier methods.
Zero descriptors, known width errors and unreachable branches fail rendering.

## Rendering and ownership

ToSQL owns its SQL string and argument slice. ToSQLWith selects options per call.
AppendSQL and AppendWith use caller-owned storage, numbering parameters after the
existing arguments. One renderer traverses nested statements in emitted SQL order.
Reusing a subquery binds each occurrence independently.

Zero options select Dollar placeholders, PostgreSQL 18, 65,535 parameters and 256
nesting levels. Question placeholders preserve argument order and PostgreSQL
syntax; the consumer must distinguish binds from PostgreSQL question-mark
operators. Raw fragments do not bind handwritten placeholders. Version checks
cover selected features, not complete server/schema/type validation.

Errors wrap sentinels in RenderError. Failed owned rendering returns empty SQL
and nil arguments. Failed appends preserve original lengths and visible prefixes,
clear appended argument references, and may change unused backing-array capacity.

Builders mutate. List methods generally append; From/FromExpr replace FROM items.
Nested builders stay live. Clone copies the reachable statement structure,
including shared subqueries, but bound application objects remain shallow.
Read-only renders may run concurrently once the graph and bound values are
stable; mutation and Reset are unsynchronized. Keep bound slices/pointers stable
through execution, and do not reuse buffers while a driver or goroutine needs
them. Clear argument references before shortening or reusing slices.

Prebuilt queries with stable interface values and sufficient storage report zero
allocations for successful warm append rendering in measured fixtures.
Construction, cloning, owned output and errors have separate costs.

## Development

```sh
make test
make race
make vet
make generate
make benchmark

# Optional live tests against PostgreSQL 18.
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
make integration
```

Regular tests run offline, exclude pgx from their compiled dependency graph and
include all 28,097 verified frozen PostgreSQL corpus occurrences. Core checks
pass on Go 1.27.0; tagged live tests have passed on PostgreSQL 18.6. The corpus
measures 99.65% construction support with all four 98% gates passing; this does
not prove schema validity, equivalent plans or every server-version behavior.

Make and CI use GOMAXPROCS=2. Ordinary builds use `-p=2 -parallel=8`; race builds
use `-p=1` to bound compiler memory. `TEST_PROCS` overrides the Make default.
The root module pins pgx for tagged tests; go mod tidy considers all build tags.

License: MIT. PostgreSQL-derived fixtures retain their
[upstream notice](internal/postgrescorpus/NOTICE.postgresql).
