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
            qs.ILikePrefix("name", prefix),
            qs.ILikePrefix("email", prefix),
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
  AND (("name" ILIKE $2 ESCAPE E'!') OR ("email" ILIKE $3 ESCAPE E'!'))
  AND ("role" IN ($4, $5))
```

Arguments: `[42 jo% jo% admin editor]`. An empty prefix or role list omits that
filter. `ILike` binds a SQL pattern; `%` and `_` retain their wildcard meaning.
An empty `In` list produces FALSE, so guard the list when empty means “no filter”.
Use `IsNull` for SQL NULL tests; `Eq` binds its value and does not infer IS NULL.

`LikePrefix` and `ILikePrefix` treat `%`, `_`, and `!` in input literally.
For example, `qs.ILikePrefix("name", "a_%")` renders
`("name" ILIKE $1 ESCAPE E'!')` and binds `a!_!%%`. The helpers also work
as `qs.Col("name").ILikePrefix(prefix)`. Their escape clause is fixed; use
`Like`/`ILike` when supplying a pattern yourself. An empty prefix matches
non-NULL text; the guard above instead omits that filter.

For a literal suffix or substring, use `LikeSuffix`, `ILikeSuffix`,
`LikeContains` or `ILikeContains`. They escape `%`, `_` and `!`, then add the
wildcards required by the operation. The input is treated as text, not as a
SQL pattern. Empty suffix and contains inputs therefore match every non-NULL
text value; guard them when an empty input means “no filter”.

Typed fields and relation aliases keep dynamic filters readable while retaining
the Go type of values expanded by `IN`:

```go
users := qs.Table("users").As("u")
id := qs.TypedExpr[int64](users.Col("id"))
ids := []int64{10, 20}
requiredTags := []string{"staff", "active"}
rawUsername := "Al"

q := qs.Select(
    id.As("id"),
    users.Col("username"),
    qs.CountAll().Over(qs.Window()).As("total"),
).FromExpr(users)
if len(ids) > 0 {
    q.Where(id.In(ids...))
}
if len(requiredTags) > 0 {
    q.Where(qs.ArrayContains(users.Col("tags"), qs.ArrayParam(requiredTags, qs.TypeText)))
}
if rawUsername != "" {
    q.Where(qs.Lower(users.Col("username")).LikePrefix(strings.ToLower(rawUsername)))
}
query, args, err := q.OrderBy(id.Asc()).Limit(20).ToSQL()
```

```sql
SELECT "u"."id" AS "id", "u"."username", count(*) OVER () AS "total"
FROM "users" AS "u"
WHERE ("u"."id" IN ($1, $2))
  AND ("u"."tags" @> ($3)::text[])
  AND (lower("u"."username") LIKE $4 ESCAPE E'!')
ORDER BY "u"."id" ASC LIMIT $5
```

Arguments: `[10 20 [staff active] al% 20]`. `TypedExpr[int64]` makes the
expanded `IN` values type-checked by Go. `ArrayParam` binds one driver-encoded
array and casts it to `text[]`; it does not expand one placeholder per tag.
`LikePrefix` escapes pattern metacharacters in the raw username. Pass the
original text; do not pre-escape it. The empty-list guards are application
policy: `Field.In` with no values renders FALSE, while an application that
treats an empty filter as “no filter” should omit it. The `LOWER(column) LIKE`
form and PostgreSQL `ILIKE` are not equivalent for every locale or Unicode
input; `strings.ToLower` and PostgreSQL `LOWER` can also differ. Choose based
on the matching contract. A `count(*) OVER ()` value is present on each
returned row, so an empty page has no row from which to read the count.

```go
query, args, err := qs.SelectCols("id").From("users").
    Where(qs.LikeSuffix("email", "@example.com")).ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("email" LIKE $1 ESCAPE E'!')
```

Arguments: `[%@example.com]`.

```go
query, args, err := qs.SelectCols("id").From("users").
    Where(qs.ILikeContains("display_name", "50%_")).ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("display_name" ILIKE $1 ESCAPE E'!')
```

Arguments: `[%50!%!_%]`. The `%` and `_` from the raw input are escaped and
match literally.

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

## Write rows and handle conflicts

Write values with `Value`, SQL expressions with `Write`, and server defaults with
`Default`. Every example below renders SQL and a separate argument list. The
`users` examples assume `id` is a primary key.

### Insert a row

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    ReturningCols("id").
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2) RETURNING "id"
```

Arguments: `[42 Ana]`.

### Update a row

```go
query, args, err := qs.Update("users").
    Set(qs.Set("name", qs.Value("Ana"))).
    Where(qs.Eq("id", 42)).
    ReturningCols("id", "name").
    ToSQL()
```

```sql
UPDATE "users" SET "name" = $1 WHERE ("id" = $2) RETURNING "id", "name"
```

Arguments: `[Ana 42]`.

### Delete a row

```go
query, args, err := qs.DeleteFrom("users").
    Where(qs.Eq("id", 42)).
    ReturningCols("id").
    ToSQL()
```

```sql
DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"
```

Arguments: `[42]`.

### Upsert with explicit assignments

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    OnConflict(qs.ConflictColumns("id").
        DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).
    ReturningCols("id").
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
RETURNING "id"
```

Arguments: `[42 Ana]`.

### Upsert dynamic columns

Build the assignment list from the columns that an operation actually updates:

```go
columns := []string{"name", "email"}
query, args, err := qs.InsertInto("users").
    Columns("id", "name", "email").
    Values(qs.Value(42), qs.Value("Ana"), qs.Value("ana@example.com")).
    OnConflict(qs.ConflictColumns("id").
        DoUpdateSlice(qs.SetAllExcluded(columns...))).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3)
ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name", "email" = "excluded"."email"
```

Arguments: `[42 Ana ana@example.com]`.

`DoUpdateSlice` copies the assignment slice. Changing its entries afterward does
not change the action; nested queries remain live and bound values remain shallow.
An empty list returns `ErrInvalid` when rendered, with empty SQL and nil arguments.
It does not become `DO NOTHING`.

### Upsert with an inferred index target

This assumes a unique index on `users.id`.

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    OnConflict(qs.ConflictIndex(qs.IndexColumn("id")).
        DoUpdateSlice(qs.SetAllExcluded("name"))).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
```

Arguments: `[42 Ana]`.

### Upsert with a named constraint

This assumes `users_pkey` is the primary-key constraint on `users.id`.

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    OnConflict(qs.ConflictConstraint("users_pkey").
        DoUpdateSlice(qs.SetAllExcluded("name"))).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
ON CONFLICT ON CONSTRAINT "users_pkey" DO UPDATE SET "name" = "excluded"."name"
```

Arguments: `[42 Ana]`.

### Upsert a literal dotted column

`Columns`, `ConflictColumns` and `Ident` treat the dot as part of one identifier.
Use `SetAllExcluded` when the update list is built from those literal column names.

```go
query, args, err := qs.InsertInto("settings").
    Columns("id", "a.b").
    Values(qs.Value(42), qs.Value("new")).
    OnConflict(qs.ConflictColumns("id").
        DoUpdateSlice(qs.SetAllExcluded("a.b"))).
    Returning(qs.Ident("a.b")).
    ToSQL()
```

```sql
INSERT INTO "settings" ("id", "a.b") VALUES ($1, $2)
ON CONFLICT ("id") DO UPDATE SET "a.b" = "excluded"."a.b"
RETURNING "a.b"
```

Arguments: `[42 new]`. This assumes a primary key on `settings.id` and a literal
text column named `a.b`.

Check `err` before passing the SQL and arguments to your driver. `Value(value)`
binds application data, including `nil`; `Write(expr)` uses a SQL expression at a
destination; and `Default()` requests the destination's SQL default.

### Insert runtime column and value slices

```go
columns := []string{"id", "name"}
row := []qs.WriteValue{qs.Value(42), qs.Value("Ana")}
query, args, err := qs.InsertInto("users").
    ColumnsSlice(columns).ValuesSlice(row).ToSQL()
// INSERT INTO "users" ("id", "name") VALUES ($1, $2)
// args: [42 Ana]
```

`ValuesSlice` also starts positional inserts and appends rows to completed
inserts. These methods copy slice entries, retain live nested statements, and
keep parameter payloads shallow. Empty column lists and empty rows fail at
render time; omit `ColumnsSlice` for positional insertion.

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

## Builder composition and write roles

Statement builders mutate. Repeated calls behave as follows:

| Receiver | Method | Repeated call |
|---|---|---|
| `SelectBuilder` | `From`, `FromExpr` | Replaces the FROM list |
| `UpdateBuilder` | `From`, `FromExpr` | Appends FROM items |
| `DeleteBuilder` | `From` | Replaces the target table |
| `InsertSelect` | `From` | Replaces the source query |
| SELECT, UPDATE, DELETE builders | `Where` | Appends conditions with AND |
| `UpdateBuilder`, `InsertAssignments` | `Set` | Appends assignments |
| `InsertRows` | `Values` | Appends a row |

For example, `SelectCols("id").From("a").From("b")` selects from only
`b`, while `Update("target").From("a").From("b")` retains both source tables.

`WhereIf(include, condition)` controls whether a predicate is appended; Go still
constructs `condition` before the call. Use an ordinary `if` when constructing
an optional predicate is expensive:

```go
if includeIDs {
    q.Where(qs.In("id", ids...))
}
```

Conflict descriptors use value-style methods: retain the result of `TargetWhere`
or `ConflictUpdate.Where`. `TargetWhere` filters index inference; `Where` filters
the update action. These calls do not mutate an earlier descriptor.

Select an optional column list before the INSERT source. `Columns(first, rest...)`
and `Targets(first, rest...)` return an immutable `InsertColumnsTarget` exposing
only `Values`, `From` and `DefaultValues`. Assignment INSERTs select `Set` directly
on `InsertInto`; they cannot combine assignments with an explicit target list.

Each source selection creates an independent completed builder: rows append
`Values`, SELECT replaces `From`, assignments append `Set`, and defaults have no
source method. Reusing either target selector creates separate builders sharing
only the immutable target list. Target expressions can retain live child queries;
`Clone` provides graph isolation. Completed builders have no `Columns`, `Targets`
or `Reset`; defaults also omit identity overriding.

Scalar assignments use `Set(column, Value(value))` for application data and
`Set(column, Write(expr))` or `Assign(target, Write(expr))` for SQL expressions.
Row assignments use `AssignRow(targets, WriteTuple(...))` or `WriteRow(...)`;
`WriteRowExpr` accepts an existing ordinary row expression. These constructors
preserve tuple versus `ROW(...)` spelling and allow direct DEFAULT elements.
Write values cannot become general expressions or bound application values.
Typed field, JSON, NULL and default assignment helpers use the same write owner.

Conflict selectors expose only legal actions. `AnyConflict` permits `DoNothing`;
column/index selectors also permit inference predicates and nonempty `DoUpdate`.
Constraint selectors omit inference predicates. All three targeted selectors also
accept `DoUpdateSlice`, whose nonempty-list requirement is checked when rendering.
Only completed updates expose `Where`. Generic `OnConflict` accepts exactly the
two completed action types, with type inference; it cannot implement a consumer
interface's nongeneric `OnConflict` method.

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

Pass pgx values directly to `Param`, `Eq` or standalone `Values`. Destination
writes use `Value(value)`. For example, with
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

Comparison methods such as `Eq` bind application values; `EqExpr` composes an
expression. Standalone `Values` binds values and `ValuesExpr` builds an ordinary
rowset from expressions. INSERT and MERGE destination values and scalar
assignments accept `WriteValue`, constructed with `Value`, `Write` or `Default`.
`Field[T]`, `Null[T]` and `Optional[T]` provide typed operands,
explicit SQL NULL values and optional fields; see the [executable examples](example_test.go).

`SelectSQL`, `UnsafeSQL`, `Fragment` and `StatementSQL` support trusted
application-authored SQL. Keep request values in bind parameters. Raw fragments
do not bind handwritten placeholders.

`Param` accepts `any`, including an untyped nil:

```go
query, args, err := qs.Select(qs.Param(nil).Cast(qs.TypeText)).ToSQL()
// SELECT ($1)::text
// args: [<nil>]
```

When migrating explicit type arguments, preserve the argument type:
`qs.Param[int64](1)` becomes `qs.Param(int64(1))`.

The scalar `Eq`, `Ne`, `Lt`, `Lte`, `Gt`, and `Gte` helpers also accept
`any`, including untyped nil. `qs.Eq("id", nil)` renders `("id" = $1)`
with one nil argument; use `IsNull` for a SQL NULL test. Migrate explicit type
arguments without changing the payload type:

```go
// Before: qs.Eq[int64]("id", 1)
condition := qs.Eq("id", int64(1))
```

For an explicitly instantiated function value, preserve its typed signature
with an application-owned wrapper when needed:

```go
eqID := func(column string, value int64) qs.Condition {
    return qs.Eq(column, value)
}
```

`Relation.Col` treats its column name literally, with or without an alias.
`qs.Table("public.settings").Col("a.b")` produces
`"public"."settings"."a.b"`; `.Col("*")` names the literal column `"*"`.
Use `.Star()` for a wildcard and top-level `qs.Col(path)` for a dotted path.
This changes the formerly inconsistent unaliased `Table.Col` behavior.
`TableIdent("*")` names a literal table, while `Table("*")` is invalid.

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
are unsynchronized. INSERT builders use a fresh completed source instead of `Reset`.

## Render options and errors

`ToSQLWith` and `AppendWith` take per-call `Options`. Zero options select Dollar
placeholders, PostgreSQL 18, 65,535 parameters and 256 nesting levels. `Question`
placeholders preserve PostgreSQL syntax and need a consumer that supports it.
Feature checks do not validate your schema, SQL types or privileges.

Check rendering errors before execution. Errors wrap sentinels such as
`ErrInvalid`, `ErrUnsupported` and `ErrParameterLimit` in `RenderError`; use
`errors.Is` to inspect them. Failed `ToSQL` calls return empty SQL and nil
arguments. Failed appends preserve their input lengths and visible prefixes.

Row-width errors report one-based indexes and counts without bound values:

```go
_, _, err := qs.Values(1).Row(2, 3).ToSQL()
// qs: invalid query (VALUES): row 2: expected 1 value, got 2 values
```

INSERT SELECT errors report target and projection counts. Unknown projection
widths are left for PostgreSQL to validate.

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

## Render diagnostics and syntax targets

`errors.Is` identifies a render failure's cause. `errors.As` exposes
`*qs.RenderError`, whose `Clause` and `Detail` describe the leaf failure.
`Path` contains structural locations from outermost to innermost, with one-based
indexes. Each returned error owns its path, so subsequent renders cannot change
it. Paths contain static roles and indexes, never bound values, caller names or
SQL fragments. They identify composition boundaries, not every internal node.
The cause and path ordering are contracts; `Error()` text is diagnostic output,
not a machine-parsing format.

Dedicated syntax checks use each render's `Options.PostgreSQL`: normalization
requires PostgreSQL 13, `SubstringSimilar` requires 14, and `AtLocal` requires 17.
`JSONParse`, `JSONScalar`, and `JSONSerialize` require PostgreSQL 17; the
SQL/JSON object, array, aggregate and predicate forms retain their PostgreSQL 16
minimum. See the [PostgreSQL 17 release notes](https://www.postgresql.org/docs/17/release-17.html)
for the parser, scalar and serialization additions.
Older substring spellings keep their existing availability. Arbitrary `Call`
names and trusted SQL are not inspected to infer feature requirements.
Symbolic operators follow standard PostgreSQL builds' 63-byte name limit;
validation does not establish that an operator exists or supports ordering.

Root tests exercise version gates without a database. Integration CI runs
PostgreSQL 12–18 with the detected server version passed to rendering, alongside
root and integration race tests. This covers declared syntax targets rather than
certifying every possible combination of schema, types and extensions.
