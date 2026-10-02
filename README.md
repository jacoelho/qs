# Querysmith (qs)

qs builds PostgreSQL queries from composable Go expressions. Start with a query,
add filters, joins or pagination as needed, then render SQL and arguments for
pgx or `database/sql`. Your application owns execution and result scanning.

- No reflection, unsafe code or third-party dependencies. The query builder uses
  only the standard library.
- Bound values and quoted identifiers. Parameters are numbered across nested
  queries in SQL order.
- Low allocation. Prebuilt queries can render into reusable buffers with
  zero allocations in the measured warm append fixtures. Construction and owned
  `ToSQL` output have separate costs.
- PostgreSQL syntax. SELECT, INSERT, UPDATE, DELETE, MERGE, CTEs, set operations,
  window functions, JSON/JSONB and XML. Per-render feature checks target
  PostgreSQL 12–18; PostgreSQL 18 is the default.

## Install

Requires Go 1.27.

```sh
go get github.com/jacoelho/qs
```

```go
import "github.com/jacoelho/qs"
```

## Build your first query

```go
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

Arguments: `[true 20]`. Check `err` before passing `query` and `args...` to your
driver, such as `db.QueryContext(ctx, query, args...)` or
`conn.Query(ctx, query, args...)` with pgx.

Use `SelectCols` for column names and `Select` for expressions:

```go
q := qs.Select(
    qs.Col("id"),
    qs.Col("name"),
    qs.Col("created_at").Cast(qs.TypeText).As("created"),
).From("users")
```

## Add optional filters

Compose a query with ordinary Go control flow. Each `Where` call appends
conditions with AND; use `Or` to group alternatives.

```go
func filteredUsers(tenantID int, prefix string, roles []string) *qs.SelectBuilder {
    q := qs.SelectCols("id", "email").From("users").
        Where(qs.Eq("tenant_id", tenantID), qs.IsNull("deleted_at"))

    if prefix != "" {
        q.Where(qs.Or(
            qs.ILike("name", prefix+"%"),
            qs.ILike("email", prefix+"%"),
        ))
    }
    if len(roles) > 0 {
        q.Where(qs.In("role", roles...))
    }
    return q
}
```

```go
query, args, err := filteredUsers(42, "jo", []string{"admin", "editor"}).ToSQL()
```

```sql
SELECT "id", "email" FROM "users"
WHERE ("tenant_id" = $1)
  AND ("deleted_at" IS NULL)
  AND (("name" ILIKE $2) OR ("email" ILIKE $3))
  AND ("role" IN ($4, $5))
```

Arguments: `[42 jo% jo% admin editor]`. An empty prefix or role list omits that
filter. `ILike` binds a SQL pattern; `%` and `_` retain their wildcard meaning.
An empty `In` list produces FALSE, so guard the list when empty means “no filter”.
Use `IsNull` for SQL NULL tests; `Eq` binds its value and does not infer IS NULL.

## Compose queries with limits and offsets

Builders mutate. Use `Clone` when branching from a reusable base query, then add
the ordering and pagination for each branch.

```go
base := qs.SelectCols("id").From("users").Where(qs.Eq("active", true))

first := base.Clone().OrderBy(qs.Asc("id")).Limit(10).Offset(0)
next := base.Clone().OrderBy(qs.Asc("id")).Limit(10).Offset(10)

query, args, err := next.ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("active" = $1)
ORDER BY "id" ASC LIMIT $2 OFFSET $3
```

The first page binds `[true 10 0]`; the next binds `[true 10 10]`. The base still
renders `SELECT "id" FROM "users" WHERE ("active" = $1)` with `[true]`.
Limit and offset values are bound parameters. Calls replace their previous
values; `RemoveLimit` and `RemoveOffset` clear them.

Choose an ordering with a unique tie-breaker for predictable page boundaries.
Offset pagination can still shift as rows change between requests. For a feed
ordered by ID, use the last returned ID as a cursor:

```go
page := base.Clone().
    Where(qs.Gt("id", lastSeenID)).
    OrderBy(qs.Asc("id")).
    Limit(10)
```

## Build a report with a CTE

Aggregate paid orders in a CTE, join the result to users, classify spending with
CASE, and paginate the report:

```go
total := qs.Sum(qs.Col("o.total"))
paidOrders := qs.CTE("paid_orders",
    qs.Select(qs.Col("o.user_id"), total.As("total_spend")).
        FromExpr(qs.Table("orders").As("o")).
        Where(qs.Eq("o.status", "paid")).
        GroupBy("o.user_id").
        Having(total.Gte(100)),
)

tier := qs.Case().
    When(qs.Gte("p.total_spend", 1000), qs.Param("vip")).
    Else(qs.Param("standard")).
    End().As("tier")

q := qs.Select(qs.Col("u.id"), qs.Col("u.email"), qs.Col("p.total_spend"), tier).
    With(paidOrders).
    FromExpr(qs.InnerJoin(qs.Table("users").As("u"), paidOrders.Ref().As("p")).
        On(qs.EqColumns("u.id", "p.user_id"))).
    Where(qs.Eq("u.active", true)).
    OrderBy(qs.Desc("p.total_spend"), qs.Asc("u.id")).
    Limit(20).Offset(40)

query, args, err := q.ToSQL()
```

```sql
WITH "paid_orders" AS (
    SELECT "o"."user_id", sum("o"."total") AS "total_spend"
    FROM "orders" AS "o"
    WHERE ("o"."status" = $1)
    GROUP BY "o"."user_id"
    HAVING (sum("o"."total") >= $2)
)
SELECT "u"."id", "u"."email", "p"."total_spend",
       CASE WHEN ("p"."total_spend" >= $3) THEN $4 ELSE $5 END AS "tier"
FROM ("users" AS "u" JOIN "paid_orders" AS "p" ON ("u"."id" = "p"."user_id"))
WHERE ("u"."active" = $6)
ORDER BY "p"."total_spend" DESC, "u"."id" ASC
LIMIT $7 OFFSET $8
```

Arguments: `[paid 100 1000 vip standard true 20 40]`. One renderer numbers binds
through the CTE and outer query. `Having` uses the aggregate expression;
`paidOrders.Ref()` gives the CTE a composable table reference.

`InnerJoin`, `LeftJoin`, `RightJoin` and `FullJoin` require completion with `On`,
`Using` or `UsingAs`. CROSS and natural joins are already complete relations.
Use `Case` for condition branches and `CaseOf` for comparisons to one operand.

## Insert and update on conflict

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").Values(42, "Ana").
    OnConflict(qs.ConflictColumns("id").
        DoUpdate(qs.SetExpr("name", qs.Excluded("name")))).
    ReturningCols("id").
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
RETURNING "id"
```

Arguments: `[42 Ana]`. `Set` binds a Go value; `SetExpr` assigns an SQL expression.
`ReturningCols` requests columns for your driver to scan.

## Cast a value to UUID

Use `Cast(qs.TypeUUID)` when SQL needs an explicit UUID type:

```go
id := "550e8400-e29b-41d4-a716-446655440000"
query, args, err := qs.Select(qs.Param(id).Cast(qs.TypeUUID)).ToSQL()
```

```sql
SELECT ($1)::uuid
```

Arguments: `[550e8400-e29b-41d4-a716-446655440000]`. To compare a UUID column
with this expression, use `qs.Col("id").EqExpr(qs.Param(id).Cast(qs.TypeUUID))`.
The cast is SQL syntax; qs leaves the Go value unchanged for the driver to encode.

Dedicated descriptors cover common PostgreSQL types, rather than the full native
catalog. Use `TypeNamed` for other built-in types, domains or extension types,
for example `qs.TypeNamed("pg_catalog", "int4range")`. `TypeArray` constructs
arrays from any descriptor.

## Use pgx types

Pass pgx values directly to `Param`, `Eq` or `Values`. For example, with
`github.com/jackc/pgx/v5/pgtype` imported and an open `conn` and `ctx`:

```go
var id pgtype.UUID
if err := id.Scan("550e8400-e29b-41d4-a716-446655440000"); err != nil {
    return err
}
query, args, err := qs.Select(qs.Param(id).Cast(qs.TypeUUID)).ToSQL()
if err != nil {
    return err
}
var result pgtype.UUID
if err := conn.QueryRow(ctx, query, args...).Scan(&result); err != nil {
    return err
}
```

pgx provides UUID encoding and scanning. A `pgtype.UUID` with `Valid: false`
encodes as SQL NULL. Built-in types need no custom registration; see
[pgx type support](https://pkg.go.dev/github.com/jackc/pgx/v5/pgtype).

### Custom composites with nested arrays

Register the nested composite, its array type, then the containing composite.
Suppose your schema contains:

```sql
CREATE TYPE pet AS (name text, age integer);
CREATE TYPE person AS (id uuid, pets pet[]);
```

Register the types on each connection after creating them. With a pool, perform
registration in `pgxpool.Config.AfterConnect`. Dependencies must be registered
first so pgx can build the containing codec; see
[new PostgreSQL type support](https://pkg.go.dev/github.com/jackc/pgx/v5/pgtype#hdr-New_PostgreSQL_Type_Support).
Using the UUID `id` and connection from the preceding example:

```go
for _, name := range []string{"pet", "_pet", "person"} {
    typ, err := conn.LoadType(ctx, name)
    if err != nil {
        return err
    }
    conn.TypeMap().RegisterType(typ)
}

person := pgtype.CompositeFields{
    id,
    pgtype.FlatArray[pgtype.CompositeFields]{
        {"Fido", int32(3)},
        {"Rex", int32(5)},
    },
}
query, args, err = qs.Select(qs.Param(person).Cast(qs.TypeNamed("person"))).ToSQL()
if err != nil {
    return err
}
type Pet struct {
    Name string
    Age  int32
}
var personResult struct {
    ID   pgtype.UUID
    Pets []Pet
}
if err := conn.QueryRow(ctx, query, args...).Scan(&personResult); err != nil {
    return err
}
```

This renders `SELECT ($1)::"person"` with one composite argument. Composite fields
and scanned public struct fields follow PostgreSQL's declared field order.
`_pet` is the array type PostgreSQL creates for `pet`; register `_person` too if
you bind `person[]`. Use `qs.TypeNamed("app", "person")` for a schema-qualified
cast, and load the corresponding schema-qualified types on the connection.
The [live type examples](integration/type_examples_test.go) verify UUIDs and the
nested composite round trip against PostgreSQL.

## Values, expressions and trusted SQL

Use `Param` for values inside expressions, `Col` for qualified column paths and
`Ident` for literal identifier parts. For example, `Col("u.id")` renders
`"u"."id"`, while `Ident("u.id")` renders `"u.id"`.

Methods ending in `Expr` accept expressions. Value-binding methods such as `Eq`
and `Values` accept application values; use `EqExpr` and `ValuesExpr` to compose
expressions instead. `Field[T]`, `Null[T]` and `Optional[T]` provide typed operands,
explicit SQL NULL values and optional fields; see the [executable examples](example_test.go).

`SelectSQL`, `UnsafeSQL`, `Fragment` and `StatementSQL` support trusted
application-authored SQL. Keep request values in bind parameters. Raw fragments
do not bind handwritten placeholders.

## Reuse buffers for repeated rendering

`ToSQL` returns an owned string and argument slice. `AppendSQL` appends to
caller-owned storage and starts numbering after any existing arguments:

```go
q := qs.SelectCols("id").From("users").Where(qs.Eq("active", true))
sqlBuffer := make([]byte, 0, 256)
argBuffer := make([]any, 0, 8)

sqlBuffer, argBuffer, err := q.AppendSQL(sqlBuffer[:0], argBuffer[:0])
```

Keep the buffers and bound values stable while a driver uses them. Clear argument
references before reusing their storage. Successful warm appends can avoid
allocations when the query is prebuilt and the buffers have enough capacity.

Nested builders remain live. `Clone` copies the statement graph, including shared
subqueries; application values in parameters remain shallow. Read-only rendering
can run concurrently once the graph and values are stable. Mutation and `Reset`
are unsynchronized.

## Render options and errors

`ToSQLWith` and `AppendWith` take per-call `Options`. Zero options select Dollar
placeholders, PostgreSQL 18, 65,535 parameters and 256 nesting levels. `Question`
placeholders preserve PostgreSQL syntax and need a consumer that supports it.
Feature checks do not validate your schema, SQL types or privileges.

Check rendering errors before execution. Errors wrap sentinels such as
`ErrInvalid`, `ErrUnsupported` and `ErrParameterLimit` in `RenderError`; use
`errors.Is` to inspect them. Failed `ToSQL` calls return empty SQL and nil
arguments. Failed appends preserve their input lengths and visible prefixes.

## More examples and development

[example_test.go](example_test.go) contains runnable examples with checked SQL and
arguments. Use `go doc .` for the full exported API and the
[architecture guide](internal/ARCHITECTURE.md) for design decisions.

```sh
make golangci-lint
make test
make race
make vet
make generate
make benchmark

# Live tests against PostgreSQL 18.
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
make integration
```

`make golangci-lint` installs the pinned linter in `.bin/` when needed and checks
all packages, including the PostgreSQL integration code. PR checks run this
target followed by `make test`; race tests, coverage and benchmarks are available
through their Make targets.

The `integration/` module owns the pgx dependency for database tests and uses the
local qs checkout. Installing qs does not install pgx; choose and install your
database driver in your application.

Regular tests run offline. The [PostgreSQL corpus](internal/postgrescorpus/README.md)
verifies 28,111 query occurrences, measuring 99.70% construction support against
the pinned PostgreSQL regression queries. This measures SQL construction, not
schema validity or query plans. Make and CI bound test parallelism;
`TEST_PROCS` overrides the Make default.

License: MIT. PostgreSQL-derived fixtures retain their
[upstream notice](internal/postgrescorpus/NOTICE.postgresql).
