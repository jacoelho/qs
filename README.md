# Querysmith (qs)

qs builds PostgreSQL queries from composable Go expressions. You can start with
a query, add filters, joins, or pagination, and render SQL with a separate list
of arguments for your database driver. Your application owns execution and
result scanning.

The library supports SELECT, INSERT, UPDATE, DELETE, MERGE, common table
expressions (CTEs), joins, set operations, window functions, JSON/JSONB, and XML.
It uses only the Go standard library, with no reflection, unsafe code, or
third-party dependencies.

When rendering a query, qs quotes identifiers and binds application values to
parameters. A single renderer numbers those parameters in SQL order across the
entire query, including nested queries. Feature checks target PostgreSQL 12
through 18, with PostgreSQL 18 as the default.

## Contents

1. [Install and build a query](#install-and-build-a-query)
2. [Use values and identifiers](#use-values-and-identifiers)
3. [Add filters and pagination](#add-filters-and-pagination)
4. [Write rows and handle conflicts](#write-rows-and-handle-conflicts)
5. [Build joins and reports](#build-joins-and-reports)
6. [Use PostgreSQL types and drivers](#use-postgresql-types-and-drivers)
7. [Render SQL and handle errors](#render-sql-and-handle-errors)
8. [Develop and find references](#develop-and-find-references)

## Install and build a query

qs requires Go 1.27 or later. To install it in your Go module, run:

```sh
go get github.com/jacoelho/qs
```

This program builds a query and prints its SQL and arguments:

```go
package main

import (
    "fmt"

    "github.com/jacoelho/qs"
)

func main() {
    query, args, err := qs.SelectCols("id", "name").
        From("users").
        Where(qs.Eq("active", true), qs.IsNull("deleted_at")).
        OrderBy(qs.Desc("created_at"), qs.Desc("id")).
        Limit(20).
        ToSQL()
    if err != nil {
        panic(err)
    }
    fmt.Println(query)
    fmt.Println(args)
}
```

The SQL below uses line breaks for readability:

```sql
SELECT "id", "name" FROM "users"
WHERE ("active" = $1) AND ("deleted_at" IS NULL)
ORDER BY "created_at" DESC, "id" DESC LIMIT $2
```

Arguments: `[true 20]`.

After handling any render error, pass `query` and `args...` to your driver:

- With `database/sql`, use `db.QueryContext(ctx, query, args...)`.
- With pgx, use `conn.Query(ctx, query, args...)`.

Your application supplies the connection and context and manages transactions
and result scanning. Install your database driver separately; qs does not
include one.

Use `SelectCols` when selecting column names and `Select` when selecting
expressions, such as a cast or an alias:

```go
q := qs.Select(
    qs.Col("id"),
    qs.Col("name"),
    qs.Col("created_at").Cast(qs.TypeText).As("created"),
).From("users")
```

## Use values and identifiers

Select the constructor that matches the input:

| Input | Constructor | Behavior |
|---|---|---|
| Application value in an expression | `Param(value)` | Binds one argument |
| Qualified column path | `Col("u.id")` | Renders `"u"."id"` |
| Literal identifier | `Ident("u.id")` | Renders `"u.id"` |
| Application value at a write destination | `Value(value)` | Binds one argument |
| SQL expression at a write destination | `Write(expr)` | Renders the expression |
| Server default at a write destination | `Default()` | Renders `DEFAULT` |

Comparison helpers such as `Eq` bind application values, while `EqExpr` compares
a column with an expression. For SQL NULL tests, use `IsNull`:
`Eq("id", nil)` binds a nil argument and does not produce `IS NULL`.

`Param` and scalar comparison helpers accept `any`, including untyped nil:

```go
query, args, err := qs.Select(qs.Param(nil).Cast(qs.TypeText)).ToSQL()
```

```sql
SELECT ($1)::text
```

Arguments: `[<nil>]`.

If the driver needs a specific value type, preserve it with an explicit Go
conversion, such as `qs.Param(int64(1))`.

Standalone `Values` binds values in a rowset, while `ValuesExpr` builds one from
expressions. A rowset is a query that can occur in an ordinary subquery: SELECT,
VALUES, TABLE, or a set operation. Write statements do not qualify as rowsets,
even with RETURNING, so use a CTE to compose them with another query.

`Field[T]` keeps operands typed, `Null[T]` represents nullable values, and
`Optional[T]` distinguishes absent values from present ones. The
[executable examples](example_test.go) show how to use them together.

### Select literal column names

`Relation.Col` treats the column name as one literal identifier, whether or not
the relation has an alias. For example,
`qs.Table("public.settings").Col("a.b")` renders
`"public"."settings"."a.b"`, keeping the dot as part of the column name.

Use `relation.Star()` for a wildcard; `relation.Col("*")` refers to a literal
column named `"*"`. To select a qualified column path, use top-level
`qs.Col(path)`. The same distinction applies to table names:
`TableIdent("*")` names a literal table, while `Table("*")` is invalid.

### Use trusted SQL

`SelectSQL`, `UnsafeSQL`, `Fragment`, and `StatementSQL` let you compose trusted
application-authored SQL with qs expressions. Keep request values in bind
parameters and never pass request text to these constructors. Raw fragments do
not bind placeholders written directly in the SQL text.

Database authorization remains your application's responsibility, including
database roles, row-level security (RLS), tenant context, and transactions.

## Add filters and pagination

### Add optional filters

Each `Where` call appends conditions with AND, and `Or` groups alternatives
within a condition. You can use ordinary Go control flow to add filters only
when the corresponding input is present:

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

Arguments: `[42 jo% jo% admin editor]`.

The example omits filters for empty prefixes and role lists. An empty `In` list
produces FALSE, so omit the condition when an empty list means "no filter" in
your application.

`WhereIf(include, condition)` appends a condition only when `include` is true,
but Go still constructs the condition before the call. Use an `if` statement
when you also want to avoid that construction work.

### Match literal text

`Like` and `ILike` bind SQL patterns in which `%` and `_` retain their wildcard
meaning. When the input should match literal text, use one of these helpers:

| Match | Case-sensitive helper | Case-insensitive helper |
|---|---|---|
| Prefix | `LikePrefix` | `ILikePrefix` |
| Suffix | `LikeSuffix` | `ILikeSuffix` |
| Substring | `LikeContains` | `ILikeContains` |

These helpers escape `%`, `_`, and `!`, then add the wildcards needed for the
selected match. They render a fixed `ESCAPE E'!'` clause, so pass the original
text without escaping it first. You can also call them as expression methods,
such as `qs.Col("name").ILikePrefix(prefix)`.

| Condition | Bound pattern |
|---|---|
| `qs.ILikePrefix("name", "a_%")` | `a!_!%%` |
| `qs.LikeSuffix("email", "@example.com")` | `%@example.com` |
| `qs.ILikeContains("display_name", "50%_")` | `%50!%!_%` |

An empty prefix, suffix, or substring matches every non-NULL text value. Omit
the condition if your application treats empty input as "no filter".

### Use typed fields and array parameters

This example uses the `strings` package and qs:

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

Arguments: `[10 20 [staff active] al% 20]`.

`TypedExpr[int64]` lets Go enforce the type of values passed to `id.In`, which
produces FALSE for an empty list. `ArrayParam` binds a single array argument
and casts it to `text[]`, leaving array encoding to the driver.

Choose the text comparison form according to your application's matching
requirements. `LOWER(column) LIKE` and PostgreSQL `ILIKE` can give different
results for some locales and Unicode input, and Go's `strings.ToLower` can
differ from PostgreSQL `LOWER`.

`count(*) OVER ()` adds the count to each returned row, so an empty page has no
row from which to read it.

### Add limits and offsets

Adding clauses modifies the builder. To create separate pages from a reusable
base query, call `Clone` before adding each page's ordering, limit, and offset:

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

The first page binds `[true 10 0]`, and the next binds `[true 10 10]`. Because
each page uses a clone, the base query retains its original condition without
a limit or offset.

`Limit` and `Offset` bind their values, replacing the previous value on each
call. Use `RemoveLimit` and `RemoveOffset` to clear the corresponding clauses.

For predictable page boundaries, choose an ordering with a unique final key.
Offset pagination can still shift as rows change between requests. For a query
ordered by ID, you can instead use the last returned ID as a cursor:

```go
page := base.Clone().
    Where(qs.Gt("id", lastSeenID)).
    OrderBy(qs.Asc("id")).
    Limit(10)
```

## Write rows and handle conflicts

Write destinations in INSERT, MERGE, and assignments accept `WriteValue`
descriptors: `Value` binds application data, including nil as SQL NULL; `Write`
supplies a SQL expression; and `Default` requests the server default. These
descriptors cannot be used as general expressions or bound application values.

The following examples assume that `users.id` is a primary key. As with SELECT,
handle any render error before passing the SQL and arguments to your driver.

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

If you need an explicit target list, select `Columns` or `Targets` before
choosing `Values`, `From`, or `DefaultValues`. Assignment INSERTs start with
`Set` directly on `InsertInto` and cannot also have an explicit target list.

Each source selection creates an independent completed builder, sharing only
the column selector's immutable target list. Once completed, an INSERT builder
cannot change the target list, switch source roles, or call `Reset`.
Default-value INSERT builders also omit identity overriding.

### Insert columns and values from slices

```go
columns := []string{"id", "name"}
row := []qs.WriteValue{qs.Value(42), qs.Value("Ana")}
query, args, err := qs.InsertInto("users").
    ColumnsSlice(columns).ValuesSlice(row).ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
```

Arguments: `[42 Ana]`.

`ColumnsSlice` and `ValuesSlice` copy slice entries, while retaining live
references to nested statements and leaving bound application objects shared.
Empty column lists and empty rows fail at render time. For positional
insertion, omit the column selector; `ValuesSlice` can start a positional
INSERT or append rows to a completed row INSERT.

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

To assign a scalar expression, use `Set(column, Write(expr))` or
`Assign(target, Write(expr))`. Row assignments use `AssignRow` with
`WriteTuple(...)` or `WriteRow(...)`, or `WriteRowExpr` for an existing row
expression. These constructors preserve tuple and `ROW(...)` syntax and allow
direct DEFAULT elements. Typed field, JSON, NULL, and default assignment
helpers use the same write descriptors.

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

UPDATE and DELETE allow statements without WHERE. Add `RequireWhere()` if your
application requires a predicate or `CURRENT OF` clause. This is a structural
guard that accepts `WHERE TRUE`, so it does not verify authorization or limit
the affected rows.

### Update a row on conflict

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

Select a conflict target that matches the database constraint:

| Target | Constructor | Available actions |
|---|---|---|
| Any conflict | `AnyConflict()` | `DoNothing` |
| Column list | `ConflictColumns("id")` | `DoNothing`, `DoUpdate`, `DoUpdateSlice` |
| Inferred index | `ConflictIndex(IndexColumn("id"))` | `DoNothing`, `DoUpdate`, `DoUpdateSlice` |
| Named constraint | `ConflictConstraint("users_pkey")` | `DoNothing`, `DoUpdate`, `DoUpdateSlice` |

For column and index targets, `TargetWhere` filters index inference; named
constraint targets do not support inference predicates. `ConflictUpdate.Where`
filters the update action itself. Both methods return new descriptors rather
than mutating earlier ones, so retain their results.

Use `DoUpdate` when you have at least one assignment argument, or
`DoUpdateSlice` when the assignment list is built at runtime:

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
ON CONFLICT ("id") DO UPDATE
SET "name" = "excluded"."name", "email" = "excluded"."email"
```

Arguments: `[42 Ana ana@example.com]`.

`DoUpdateSlice` copies the assignment slice entries, retaining live nested
statements and shared bound application objects. An empty list produces
`ErrInvalid` at render time rather than becoming `DO NOTHING`.

`SetAllExcluded` treats column names literally on both sides of each assignment,
matching `Columns` and `ConflictColumns`. For a column named `a.b`, it renders
`"a.b" = "excluded"."a.b"`; use `Ident("a.b")` to return the same literal
column.

The generic `OnConflict` methods accept the two completed action types. Because
they are generic, they cannot satisfy a consumer interface that requires a
nongeneric `OnConflict` method.

### Reuse statement builders

Repeated builder calls have these effects:

| Builder | Method | Effect |
|---|---|---|
| SELECT | `From`, `FromExpr` | Replaces the FROM list |
| UPDATE | `From`, `FromExpr` | Appends FROM items |
| DELETE | `From` | Replaces the target table |
| INSERT SELECT | `From` | Replaces the source query |
| SELECT, UPDATE, DELETE | `Where` | Appends conditions with AND |
| UPDATE, assignment INSERT | `Set` | Appends assignments |
| Row INSERT | `Values`, `ValuesSlice` | Appends a row |

For example, `SelectCols("id").From("a").From("b")` selects from `b` only,
while `Update("target").From("a").From("b")` retains both source tables.

## Build joins and reports

Queries compose across CTEs, joins, and expressions. This example groups paid
orders in a CTE, joins the result to users, and uses CASE to select a spending
tier:

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

Arguments: `[paid 100 1000 vip standard true 20 40]`.

The renderer numbers parameters across the CTE and outer query in SQL order.
`Having` filters on the aggregate expression, and `paidOrders.Ref()` provides
the table reference used to join the CTE's result.

`InnerJoin`, `LeftJoin`, `RightJoin`, and `FullJoin` need an `On`, `Using`, or
`UsingAs` clause before they form a complete relation. CROSS and natural joins
are already complete. For conditional expressions, `Case` uses conditions for
each branch, while `CaseOf` compares branches with a single operand.

## Use PostgreSQL types and drivers

### Cast a value

Use `Cast` when SQL requires an explicit type:

```go
id := "550e8400-e29b-41d4-a716-446655440000"
query, args, err := qs.Select(qs.Param(id).Cast(qs.TypeUUID)).ToSQL()
```

```sql
SELECT ($1)::uuid
```

Arguments: `[550e8400-e29b-41d4-a716-446655440000]`.

To compare a UUID column with this expression, use
`qs.Col("id").EqExpr(qs.Param(id).Cast(qs.TypeUUID))`. The cast affects the SQL
text; qs leaves the Go value unchanged for the driver to encode.

Dedicated descriptors cover common PostgreSQL types. For other built-in types,
domains, or extension types, use `TypeNamed`, such as
`qs.TypeNamed("pg_catalog", "int4range")`. `TypeArray` constructs an array type
from any descriptor.

### Use pgx values

pgx values can pass directly to `Param`, comparison helpers, or standalone
`Values`. For write destinations, pass them through `Value`.

This example requires `github.com/jackc/pgx/v5/pgtype`, an open pgx connection `conn`, and a context `ctx`:

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

pgx handles UUID encoding and scanning, including SQL NULL for a `pgtype.UUID`
with `Valid: false`. Built-in types do not require custom registration; see
[pgx type support](https://pkg.go.dev/github.com/jackc/pgx/v5/pgtype).

### Register composite types

A composite type can contain an array of another composite type. For example,
the `person` type below contains an array of `pet` values:

```sql
CREATE TYPE pet AS (name text, age integer);
CREATE TYPE person AS (id uuid, pets pet[]);
```

After you create the types, register them on each connection in this order:

1. Register `pet`.
2. Register `_pet`, the array type that PostgreSQL creates for `pet`.
3. Register `person`.

pgx needs each dependency registered before it can construct the containing
codec. With a connection pool, perform this registration in
`pgxpool.Config.AfterConnect`. See
[pgx registration of new PostgreSQL types](https://pkg.go.dev/github.com/jackc/pgx/v5/pgtype#hdr-New_PostgreSQL_Type_Support)
for details.

Use the UUID `id`, connection, and context from the previous example:

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

This renders `SELECT ($1)::"person"` with one composite argument. Composite
fields and exported Go struct fields follow PostgreSQL's declared field order.
If you also bind `person[]`, register `_person`. For schema-qualified types,
use a cast such as `qs.TypeNamed("app", "person")` and load the corresponding
schema-qualified types on the connection.

The [database type examples](integration/type_examples_test.go) verify UUID and nested composite results against PostgreSQL.

## Render SQL and handle errors

### Select render options

`ToSQL` returns an owned SQL string and argument slice. To configure a render,
pass `Options` to `ToSQLWith` or `AppendWith`. Zero-valued fields select the
following defaults:

| Option | Default | Valid non-default values |
|---|---|---|
| `PlaceholderStyle` | `Dollar` (`$1`, `$2`, ...) | `Question` (`?`) |
| `PostgreSQL` | PostgreSQL 18 | PostgreSQL 12 through 18 |
| `MaxParameters` | 65,535 | 1 through 65,535 |
| `MaxDepth` | 256 | 1 through 4,096 |

For example, render for PostgreSQL 16:

```go
query, args, err := q.ToSQLWith(qs.Options{PostgreSQL: qs.PostgreSQL16})
```

`Question` changes only the parameter placeholders, preserving PostgreSQL
syntax. It requires a consumer that supports question-mark placeholders. The
parameter limit includes any existing arguments passed to an append method,
and the depth limit bounds nested traversal and rejects cycles.

Feature checks cover known syntax requirements, leaving schema objects, SQL
types, privileges, and driver encoding to PostgreSQL and your driver. qs does
not infer feature requirements for arbitrary `Call` names or trusted SQL, and
PostgreSQL validates unknown projection widths. Operator validation enforces
the standard PostgreSQL limit of 63 bytes without proving that an operator
exists or supports ordering.

Some features require these minimum versions:

| Feature | Minimum PostgreSQL version |
|---|---|
| Normalization | 13 |
| `SubstringSimilar` | 14 |
| SQL/JSON object, array, aggregate, and predicate forms | 16 |
| `AtLocal`, `JSONParse`, `JSONScalar`, `JSONSerialize` | 17 |

The selected `Options.PostgreSQL` value controls feature checks for each render.
Older substring forms retain their existing version support.

### Handle render errors

Use `errors.Is` to identify the cause of a render error:

| Cause | Meaning |
|---|---|
| `ErrInvalid` | Invalid query structure, input, or options |
| `ErrUnsupported` | Feature unavailable in the selected PostgreSQL version |
| `ErrParameterLimit` | Too many arguments |
| `ErrDepth` | Query cycle or nesting limit exceeded |

For more context, use `errors.As` to obtain `*qs.RenderError`, which provides
these fields:

- `Clause` identifies the construct or option that failed.
- `Detail` describes the failure.
- `Path` lists structural locations from outermost to innermost, with one-based indexes.

Each error owns its path, so later renders cannot change it. Paths identify
composition boundaries through static roles and indexes; they do not include
bound values, caller names, SQL fragments, or every internal node. The cause
and path ordering are contracts, but `Error()` text is diagnostic output and
should not be parsed as a machine format.

On failure, `ToSQL` returns empty SQL and nil arguments. Append methods return
the original slice lengths and visible prefixes, although unused buffer
capacity can change.

Row-width errors identify the row and the expected and actual counts:

```go
_, _, err := qs.Values(1).Row(2, 3).ToSQL()
```

```text
qs: invalid query (VALUES): row 2: expected 1 value, got 2 values [path: VALUES → row[2]]
```

INSERT SELECT errors report target and projection counts. Neither diagnostic
includes bound values.

### Reuse buffers

For repeated rendering, `AppendSQL` appends SQL and arguments to caller-owned
buffers, starting parameter numbering after any existing arguments:

```go
q := qs.SelectCols("id").From("users").Where(qs.Eq("active", true))
sqlBuffer := make([]byte, 0, 256)
argBuffer := make([]any, 0, 8)

sqlBuffer, argBuffer, err := q.AppendSQL(sqlBuffer[:0], argBuffer[:0])
```

Keep the buffers and bound values unchanged while the driver uses them. Once
execution finishes, clear argument references before reusing the storage, for
example with `clear(argBuffer)` before the next append.

Successful appends can avoid allocations when the query is prebuilt and the
buffers have enough capacity. Construction, cloning, owned output, and
failures have separate costs, so this is not a zero-allocation guarantee for
every query. The [performance report](internal/PERFORMANCE.md) documents the
measurements and their limits.

### Control query lifetime

Nested builders remain live references, which means a parent query observes
later changes to its children. `Clone` copies the reachable statement graph,
including target expressions with child queries, and preserves shared
subqueries within the copy. Application objects in bound parameters remain
shared with the original.

You can render concurrently once the query graph and bound values are stable.
Mutation and `Reset` are unsynchronized, so do not mutate a builder while
another goroutine renders it. Resetting a child also changes what its parent
observes. INSERT builders do not support `Reset`; select a source again to
create a new completed builder.

## Develop and find references

Run these checks from the repository root:

```sh
make golangci-lint
make test
```

`make golangci-lint` installs the pinned linter in `.bin/` when needed and
checks both the root packages and the PostgreSQL integration module. Pull
request checks run the linter, regular tests, and race tests. Regular tests
need no database or database driver, and Make and CI bound test parallelism.
Use `TEST_PROCS` to override the Make default.

Use these targets for additional development tasks:

| Task | Command |
|---|---|
| Detect data races | `make race` |
| Analyze Go code | `make vet` |
| Measure test coverage | `make coverage` |
| Regenerate code | `make generate` |
| Verify generated code without changes | `make generate-check` |
| Run benchmarks | `make benchmark` |

To run database tests, supply a PostgreSQL connection string:

```sh
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
make integration
```

The `integration/` module owns the pgx dependency and uses the local qs checkout.
Integration CI tests PostgreSQL 12 through 18, passing the detected server
version to rendering. CI also runs root and integration race tests. This
coverage verifies the declared syntax targets without claiming support for
every combination of schema, types, and extensions.

Use these references for more information:

- [Executable examples](example_test.go): examples with expected SQL and arguments.
- `go doc .`: the full exported API.
- [Architecture guide](internal/ARCHITECTURE.md): boundaries, ownership, and design decisions.
- [Performance report](internal/PERFORMANCE.md): measurements and performance limits.
- [PostgreSQL corpus](internal/postgrescorpus/README.md): construction measurements and fixture maintenance.

The pinned corpus verifies 28,111 of 28,197 query occurrences, measuring 99.70%
construction support. This measures SQL construction, not schema validity,
query results, or query plans.

qs uses the [MIT license](LICENSE). PostgreSQL-derived fixtures retain their
[upstream notice](internal/postgrescorpus/NOTICE.postgresql).
