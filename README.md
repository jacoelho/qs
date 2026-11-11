# Querysmith (qs)

qs builds PostgreSQL queries from composable Go expressions. You can start with
a query, add filters, joins, or pagination, and render SQL with a separate list
of arguments for your database driver.

qs renders SQL and nothing else. It does not connect to a database, execute
queries, or include a driver: your application supplies the connection, manages
transactions, and scans results.

The library supports SELECT, INSERT, UPDATE, DELETE, MERGE, common table
expressions (CTEs), joins, set operations, window functions, JSON/JSONB
operators, and SQL/JSON syntax including `JSON_TABLE`. It uses only the Go
standard library, with no unsafe code or third-party dependencies. The root
package uses no reflection; the `projection` companion uses `reflect` when a
declaration is made, not when a row is scanned.

When rendering a query, qs quotes identifiers and binds application values to
`$n` parameters. A single renderer numbers those parameters in SQL order across
the entire query, including nested queries. Statement builders are immutable
values, so a query can be branched and shared between goroutines.

Rendering checks query structure only. Schema objects, SQL types, privileges,
and driver encoding are left to PostgreSQL and your driver; see
[Know what rendering checks](#know-what-rendering-checks).

## Contents

1. [Install qs](#install-qs)
2. [Build a first query](#build-a-first-query)
3. [Bind values and name identifiers](#bind-values-and-name-identifiers)
4. [Filter rows](#filter-rows)
5. [Page results](#page-results)
6. [Write rows](#write-rows)
7. [Compose joins, CTEs, and reports](#compose-joins-ctes-and-reports)
8. [Build and query JSON](#build-and-query-json)
9. [Scan rows with projection declarations](#scan-rows-with-projection-declarations)
10. [Use PostgreSQL types with pgx](#use-postgresql-types-with-pgx)
11. [Render SQL and handle errors](#render-sql-and-handle-errors)
12. [Reuse and share builders](#reuse-and-share-builders)
13. [Develop qs](#develop-qs)
14. [Find references](#find-references)

## Install qs

qs requires Go 1.27 or later. To install it in your Go module, run:

```sh
go get github.com/jacoelho/qs
```

Install your database driver separately; qs does not include one.

## Build a first query

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
        Where(qs.Col("active").Eq(qs.Param(true)), qs.Col("deleted_at").IsNull()).
        OrderBy(qs.Col("created_at").Desc(), qs.Col("id").Desc()).
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

Use `SelectCols` when selecting column names and `Select` when selecting
expressions, such as a qualified name, a cast, or an alias:

```go
q := qs.Select(
    qs.Col("id"),
    qs.Col("name"),
    qs.Col("created_at").Cast(qs.TypeText).As("created"),
).From("users")
```

Every builder method returns a new builder and leaves its receiver unchanged.
Keep the result: `q = q.Where(...)`.

## Bind values and name identifiers

Select the constructor that matches the input:

| Input | Constructor | Behavior |
|---|---|---|
| Application value in an expression | `Param(value)` | Binds one argument |
| Column | `Col("id")` | Renders `"id"` |
| Qualified column | `Col("u", "id")` | Renders `"u"."id"` |
| Column compared with Go values | `TypedCol[int64]("u", "id")` | Renders `"u"."id"`; comparisons bind `int64` values |
| Application value at a write destination | `Value(value)` | Binds one argument |
| SQL expression at a write destination | `Write(expr)` | Renders the expression |
| Server default at a write destination | `Default()` | Renders `DEFAULT` |

If the driver needs a specific value type, preserve it with an explicit Go
conversion, such as `qs.Param(int64(1))`.

### Compare values

Comparisons are methods on `Expr` that take an `Expr`: `Eq`, `Ne`, `Lt`, `Lte`,
`Gt`, `Gte`, `In`, `NotIn`, `Between`, `IsDistinctFrom`, and their relatives.
Bind an application value with `Param`.

A `Field[T]` binds values for you. Build one with `TypedCol[T]` from a column
name or `Typed[T]` from an expression; its comparison methods take Go values of
type `T`:

```go
users := qs.Table("users").As("u")
id := qs.TypedCol[int64]("u", "id")
role := qs.Typed[string](users.Col("role"))

query, args, err := qs.Select(id.Expr()).FromExpr(users).
    Where(
        id.Gt(1000),
        role.In("admin", "editor"),
        users.Col("score").Gte(qs.Param(4.5)),
        users.Col("created_at").Lt(qs.Now()),
        users.Col("deleted_at").IsNull(),
    ).ToSQL()
```

```sql
SELECT "u"."id" FROM "users" AS "u"
WHERE ("u"."id" > $1)
  AND ("u"."role" IN ($2, $3))
  AND ("u"."score" >= $4)
  AND ("u"."created_at" < now())
  AND ("u"."deleted_at" IS NULL)
```

Arguments: `[1000 admin editor 4.5]`.

`Field[T]` proves nothing about the SQL type and adds no cast. To compare two
fields, use the `Field` variants, such as `EqField`. `Field.Expr` returns the
expression for everything else.

### Handle SQL NULL

For SQL NULL tests, use `IsNull` or `IsNotNull`.

A comparison with SQL NULL is never true, so comparison operands reject a bound
untyped nil. This covers `In` lists, `Between` bounds, row values, and
`SeekAfter` cursors. `qs.Col("id").Eq(qs.Param(nil))` fails rendering with
`ErrInvalid` rather than selecting no rows. A typed nil, such as `(*int)(nil)`,
still binds.

Outside comparisons, `Param`, `Value`, and `ParamNull` bind SQL NULL:

```go
query, args, err := qs.Select(qs.Param(nil).Cast(qs.TypeText)).ToSQL()
```

```sql
SELECT ($1)::text
```

Arguments: `[<nil>]`.

`Null[T]` represents nullable values, and `Optional[T]` distinguishes absent
values from present ones. The [executable examples](example_test.go) show how to
use them together.

### Name columns and tables

Every string that names something is exactly one identifier, quoted as written.
qs never splits a name on dots; pass each part of a qualified name as its own
argument:

| Call | Renders |
|---|---|
| `qs.Col("u", "id")` | `"u"."id"` |
| `qs.Col("a.b")` | `"a.b"` |
| `qs.Table("public", "users")` | `"public"."users"` |
| `qs.Star("u")` | `"u".*` |
| `qs.Call("my_fn", qs.Param(1))` | `"my_fn"($1)` |
| `qs.QualifiedCall("app", "my_fn", qs.Param(1))` | `"app"."my_fn"($1)` |
| `qs.Col("name").Collate("pg_catalog", "default")` | `("name" COLLATE "pg_catalog"."default")` |

A dotted string where a qualified name was meant renders one identifier:
`qs.Col("u.id")` is `"u.id"` and `From("public.users")` is `"public.users"`.
Rendering succeeds and the database rejects the name. Quoted names are also
case-sensitive: `qs.Call("lower", x)` finds the built-in function and
`qs.Call("LOWER", x)` does not.

String shorthands such as `SelectCols`, `From`, `GroupBy`, and `ReturningCols`
take one identifier per string. For a qualified name, use the expression form:
`Select`, `FromExpr`, `GroupByExpr`, or `Returning`.

`Relation.Col` qualifies a column by the relation's alias, or by its table name
when it has no alias. `qs.Table("public", "settings").Col("a.b")` renders
`"public"."settings"."a.b"`. Use `relation.Star()` for a wildcard;
`relation.Col("*")` refers to a column named `"*"`.

### Use trusted SQL

`SelectSQL`, `UnsafeSQL`, `Fragment`, and `StatementSQL` let you compose trusted
application-authored SQL with qs expressions.

Never pass request text to these constructors; keep request values in bind
parameters. Raw fragments do not bind placeholders written directly in the SQL
text.

When qs combines a trusted condition with another, it parenthesizes the trusted
condition. This applies to two `Where` conditions, a `SeekAfter` cursor, and
`And`, `Or` and `Not`. `Where(qs.AsCondition(qs.UnsafeSQL("a OR b")))` with a
cursor renders `WHERE (a OR b) AND (...)`. Other operators render trusted SQL
verbatim, so call `Parenthesized` where its operators could bind to a
neighboring operand.

### Reach syntax qs does not model

qs does not model XML, EXPLAIN, TRUNCATE, CREATE TABLE AS, materialized views,
DECLARE CURSOR, EXECUTE, or SELECT INTO.
Reach them, and anything else qs lacks, through three escape hatches:

- `Call` and `QualifiedCall` for functions.
- `UnsafeSQL` and `Fragment` for trusted expression syntax.
- `StatementSQL` for a trusted statement.

```go
score := qs.Call("similarity", qs.Col("name"), qs.Param("ana")).As("score")
query, args, err := qs.Select(qs.Col("id"), score).From("users").ToSQL()

refresh, _, err := qs.StatementSQL(
    qs.UnsafeSQL(`REFRESH MATERIALIZED VIEW "daily_totals"`),
).ToSQL()
```

The first statement renders `SELECT "id", "similarity"("name", $1) AS "score" FROM "users"`
with arguments `[ana]`. The second renders its text unchanged.

Database authorization remains your application's responsibility, including
database roles, row-level security (RLS), tenant context, and transactions.

## Filter rows

### Add optional filters

Each `Where` call returns a builder with its conditions appended with AND, and
`Or` groups alternatives within a condition. You can use ordinary Go control
flow to add filters only when the corresponding input is present:

```go
func filteredUsers(tenantID int, prefix string, roles []string) qs.SelectBuilder {
    q := qs.SelectCols("id", "email").From("users").
        Where(qs.Col("tenant_id").Eq(qs.Param(tenantID)), qs.Col("deleted_at").IsNull())

    if prefix != "" {
        q = q.Where(qs.Or(
            qs.Col("name").ILikePrefix(prefix),
            qs.Col("email").ILikePrefix(prefix),
        ))
    }
    if len(roles) > 0 {
        q = q.Where(qs.TypedCol[string]("role").In(roles...))
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

`WhereIf(include, condition)` appends a condition to a SELECT only when
`include` is true, but Go still constructs the condition before the call. Use
an `if` statement when you also want to avoid that construction work.

UPDATE and DELETE take their whole predicate in one `Where` call and have no
`WhereIf`; see
[Select the rows UPDATE and DELETE change](#select-the-rows-update-and-delete-change).

### Match literal text

`Like` and `ILike` bind SQL patterns in which `%` and `_` retain their wildcard
meaning. When the input should match literal text, use one of these `Expr`
methods:

| Match | Case-sensitive method | Case-insensitive method |
|---|---|---|
| Prefix | `LikePrefix` | `ILikePrefix` |
| Suffix | `LikeSuffix` | `ILikeSuffix` |
| Substring | `LikeContains` | `ILikeContains` |

These methods escape `%`, `_`, and `!`, then add the wildcards needed for the
selected match. They render a fixed `ESCAPE E'!'` clause, so pass the original
text without escaping it first.

| Condition | Bound pattern |
|---|---|
| `qs.Col("name").ILikePrefix("a_%")` | `a!_!%%` |
| `qs.Col("email").LikeSuffix("@example.com")` | `%@example.com` |
| `qs.Col("display_name").ILikeContains("50%_")` | `%50!%!_%` |

An empty prefix, suffix, or substring matches every non-NULL text value. Omit
the condition if your application treats empty input as "no filter".

### Compare typed fields and array parameters

This example uses the `strings` package and qs:

```go
users := qs.Table("users").As("u")
id := qs.Typed[int64](users.Col("id"))
ids := []int64{10, 20}
requiredTags := []string{"staff", "active"}
rawUsername := "Al"

q := qs.Select(
    id.As("id"),
    users.Col("username"),
    qs.CountAll().Over(qs.Window()).As("total"),
).FromExpr(users)
if len(ids) > 0 {
    q = q.Where(id.In(ids...))
}
if len(requiredTags) > 0 {
    q = q.Where(qs.ArrayContains(users.Col("tags"), qs.ArrayParam(requiredTags, qs.TypeText)))
}
if rawUsername != "" {
    q = q.Where(qs.Lower(users.Col("username")).LikePrefix(strings.ToLower(rawUsername)))
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

`Typed[int64]` lets Go enforce the type of values passed to `id.In`, which
produces FALSE for an empty list. `ArrayParam` binds a single array argument
and casts it to `text[]`, leaving array encoding to the driver. A nil slice
binds an empty array rather than NULL, so `= ANY` is FALSE and `<> ALL` is TRUE.

Choose the text comparison form according to your application's matching
requirements. `LOWER(column) LIKE` and PostgreSQL `ILIKE` can give different
results for some locales and Unicode input, and Go's `strings.ToLower` can
differ from PostgreSQL `LOWER`.

`count(*) OVER ()` adds the count to each returned row, so an empty page has no
row from which to read it, and after a `SeekAfter` cursor it counts only the
remaining rows. [Count matching rows](#count-matching-rows) with `CountRows`
instead.

## Page results

### Add limits and offsets

A derived builder never changes its source, so one base query serves every
page:

```go
base := qs.SelectCols("id").From("users").Where(qs.Col("active").Eq(qs.Param(true)))

first := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(0)
next := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(10)

query, args, err := next.ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("active" = $1)
ORDER BY "id" ASC LIMIT $2 OFFSET $3
```

The first page binds `[true 10 0]`, and the next binds `[true 10 10]`. `base`
keeps its condition and gains no ordering, limit, or offset.

`Limit` and `Offset` bind their values, replacing the previous value on each
call. To drop an offset or ordering, branch from a builder that has none.

A query has one row limit. `Limit`, `LimitExpr`, `LimitAll`, and
`LimitWithTies` each replace it, so LIMIT and FETCH FIRST cannot both be set.
`LimitWithTies(n)` renders `FETCH FIRST (n) ROWS WITH TIES`, which also returns
the rows that tie with the last one. It fails rendering without ORDER BY or
with a SKIP LOCKED row lock.

For predictable page boundaries, choose an ordering with a unique final key.
Offset pagination can still shift as rows change between requests, and the
server still reads every skipped row.

### Page with a keyset cursor

`SeekAfter` continues after the last row of the previous page. Pass that row's
ORDER BY key values, one per term:

```go
order := []qs.Order{qs.Col("users", "created_at").Desc(), qs.Col("users", "id").Desc()}
next := base.
    OrderBy(order...).
    SeekAfter(lastCreatedAt, lastID).
    Limit(20)
```

```sql
SELECT "id" FROM "users"
WHERE ("active" = $1) AND (("users"."created_at", "users"."id") < ($2, $3))
ORDER BY "users"."created_at" DESC, "users"."id" DESC LIMIT $4
```

The cursor follows every other WHERE condition, whatever the call order. Each
`SeekAfter` call replaces the cursor.

qs cannot inspect the schema. Pages are complete and disjoint only when every
key is NOT NULL and the last key is unique.

Qualify column keys: `qs.Col("users", "id").Desc()`, or
`users.Col("id").Desc()` for a `Relation`. ORDER BY resolves a bare name to a
same-named output column first, including a derived one such as the `a` that
`SELECT a::text` produces, while WHERE resolves it to the input column.
Qualified names and other expressions resolve the same way in both clauses.
Name a USING join with `UsingAs` and a `Derived` relation with `As` to qualify
their columns.

Rendering fails with `ErrInvalid` when:

- the cursor width differs from the ORDER BY width;
- a term uses NULLS FIRST/LAST, USING, or an ordinal;
- a key is a bare name;
- the query uses DISTINCT ON.

Keys are evaluated in WHERE, so they cannot be output aliases, aggregates, or
window functions. Wrap such a query with `Subquery` and page the outer SELECT
by its alias-qualified columns. Page set operations, VALUES, and TABLE the same
way.

A trusted SQL or `Fragment` key is compared as one operand, so `NOT archived`
keeps its meaning. qs does not inspect such a key: it must not render a bare
name or an integer.

Uniform directions render one row comparison, which a B-tree index on the keys
in ORDER BY order can serve. Mixed directions render an equivalent disjunction
that binds earlier keys again: three keys bind seven arguments.

For the previous page, reverse each term, seek after the first row of the
current page, and reverse the fetched rows:

```go
previous := base.
    OrderBy(order[0].Reverse(), order[1].Reverse()).
    SeekAfter(firstCreatedAt, firstID).
    Limit(20)
```

`go doc . SelectBuilder.SeekAfter` states the full contract, including how a
typed nil cursor value behaves.

### Count matching rows

`CountRows` counts the rows a query selects without its cursor and pagination:

```go
total := qs.CountRows(next)
```

```sql
SELECT count(*) FROM (SELECT "id" FROM "users" WHERE ("active" = $1)) AS "matching"
```

The count omits the `SeekAfter` cursor, the row limit, OFFSET, and row locks,
and keeps WITH, DISTINCT, GROUP BY, and HAVING. A data-modifying WITH fails
rendering. `CountRows` captures the builder it is given; builders derived from
that one later do not change the count.

The count is a separate statement. It agrees with the pages only when both run
in one REPEATABLE READ or SERIALIZABLE transaction.

The count does not sort, but an ORDER BY expression can still change the rows:
a set-returning function such as `unnest(tags)` or an aggregate such as
`count(*)` does. To keep that effect, ORDER BY expressions other than column
names, ordinals, and bound values move into the counted projection as
`qs_order_N` columns. Three consequences follow:

- DISTINCT ON and GROUP BY must not name columns `qs_order_N`.
- Volatile moved expressions run for every counted row.
- A trusted SQL term naming an output column can fail, so order by the aliased
  expression.

## Write rows

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

Each source selection returns a completed builder with the methods of that
source. A completed INSERT builder cannot change the target list or switch
source. Default-value INSERT builders also omit identity overriding.

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

`ColumnsSlice` and `ValuesSlice` copy the slice; bound application values are
not copied. Empty column lists and empty rows fail at render time. For
positional insertion, omit the column selector; `ValuesSlice` can start a
positional INSERT or append a row to a row INSERT.

To insert rows sized by your data, pass them all to `Rows`, for example
`InsertInto("users").Columns("id", "name").Rows(rows...)` with `rows` a
`[][]qs.WriteValue`. `Values` and `ValuesSlice` copy the row list on every
call, so a loop that adds one row per call does work quadratic in the number of
rows; `Rows` copies once. A VALUES rowset has the same pair: `Row` and
`RowExpr` add one row, `Rows` and `RowsExpr` add many. No rows fails at render
time.

### Insert columnar batches

For data already arranged in columns, bind one array per column and expand them
with `Unnest`. This keeps the parameter count proportional to columns:

```go
ids := []int32{42, 43}
names := []string{"Ana", "Ben"}
rows := qs.Select(
    qs.Unnest(qs.ArrayParam(ids, qs.TypeInt4)),
    qs.Unnest(qs.ArrayParam(names, qs.TypeText)),
)
query, args, err := qs.InsertInto("users").Columns("id", "name").From(rows).ToSQL()
```

The driver encodes the arrays.

Supply arrays of equal length. PostgreSQL
[pads shorter results with NULL](https://www.postgresql.org/docs/18/xfunc-sql.html#XFUNC-SQL-FUNCTIONS-RETURNING-SET),
and qs does not inspect bound application data. Empty arrays insert no rows.

Bound batch size according to data volume; fewer parameters do not reduce the
payload size. For larger loads, consider the driver's COPY support.

The [executable upsert example](production_query_example_test.go) combines this
shape with partial-index conflict inference and RETURNING. Its table needs a
unique index on `id WHERE deleted_at IS NULL`.

### Update a row

```go
query, args, err := qs.Update("users").
    Set(qs.Set("name", qs.Value("Ana"))).
    Where(qs.Col("id").Eq(qs.Param(42))).
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

An UPDATE with no assignments fails rendering. For sparse updates, assemble
assignments first and choose explicitly whether an all-absent patch is a no-op
or an error. A present NULL remains an assignment; it is not absence.

### Delete a row

```go
query, args, err := qs.DeleteFrom("users").
    Where(qs.Col("id").Eq(qs.Param(42))).
    ReturningCols("id").
    ToSQL()
```

```sql
DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"
```

Arguments: `[42]`.

### Select the rows UPDATE and DELETE change

`Update` and `DeleteFrom` return a pending statement with no `ToSQL`. One of
three methods completes it by saying which rows it changes:

- `Where(first, rest...)` selects the rows that satisfy every condition.
- `WhereCurrentOf(cursor)` selects the row a cursor is positioned on.
- `AllRows()` selects every row.

A mutation that forgot its predicate does not compile. A completed statement
accepts only RETURNING, WITH, and the prefix and suffix clauses, so the row
selection cannot be changed or combined afterwards.

```go
query, args, err := qs.DeleteFrom("sessions").AllRows().ToSQL()
```

```sql
DELETE FROM "sessions"
```

`Where` takes the whole predicate in one call. Build optional filters as a
slice and pass them with `qs.And(filters...)`.

One check remains at render: a predicate that is TRUE for every row fails with
`ErrInvalid`. `qs.And` and `NotIn` over an empty list are TRUE and would
otherwise reach every row. The check reads TRUE and FALSE literals through
`And`, `Or`, `Not` and parentheses, so `qs.And(qs.True(), qs.True())` and
`qs.Not(qs.False())` fail too; use `AllRows` instead.

The guard is structural. It does not inspect bound values or trusted SQL,
verify authorization, or limit the affected rows.

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
filters the update action itself. Both methods return new descriptors, so
retain their results.

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

`DoUpdateSlice` copies the assignment slice; bound application values are not
copied. An empty list produces `ErrInvalid` at render time rather than becoming
`DO NOTHING`.

`SetAllExcluded` takes one identifier per column, as `Columns` and
`ConflictColumns` do. For a column named `a.b`, it renders
`"a.b" = "excluded"."a.b"`.

The generic `OnConflict` methods accept the two completed action types. Because
they are generic, they cannot satisfy a consumer interface that requires a
nongeneric `OnConflict` method.

## Compose joins, CTEs, and reports

Queries compose across CTEs, joins, and expressions. This example groups paid
orders in a CTE, joins the result to users, and uses CASE to select a spending
tier:

```go
total := qs.Sum(qs.Col("o", "total"))
paidOrders := qs.CTE("paid_orders",
    qs.Select(qs.Col("o", "user_id"), total.As("total_spend")).
        FromExpr(qs.Table("orders").As("o")).
        Where(qs.Col("o", "status").Eq(qs.Param("paid"))).
        GroupByExpr(qs.Col("o", "user_id")).
        Having(total.Gte(qs.Param(100))),
)

tier := qs.Case().
    When(qs.Col("p", "total_spend").Gte(qs.Param(1000)), qs.Param("vip")).
    Else(qs.Param("standard")).
    End().As("tier")

q := qs.Select(qs.Col("u", "id"), qs.Col("u", "email"), qs.Col("p", "total_spend"), tier).
    With(paidOrders).
    FromExpr(qs.InnerJoin(qs.Table("users").As("u"), paidOrders.Ref().As("p")).
        On(qs.Col("u", "id").Eq(qs.Col("p", "user_id")))).
    Where(qs.Col("u", "active").Eq(qs.Param(true))).
    OrderBy(qs.Col("p", "total_spend").Desc(), qs.Col("u", "id").Asc()).
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
are already complete.

For conditional expressions, `Case` uses conditions for each branch, while
`CaseOf` compares branches with a single operand. Both return a value whose
only method adds the first branch, so a CASE with no WHEN has no `End` to call.

A rowset is a query that can occur in an ordinary subquery: SELECT, VALUES,
TABLE, or a set operation. Standalone `Values` binds values in a rowset, while
`ValuesExpr` builds one from expressions. Write statements do not qualify as
rowsets, even with RETURNING, so use a CTE to compose them with another query.

### Group a parameterized expression

Each occurrence of `Param` receives a new placeholder, even when the same `Expr`
is reused. Repeating `date_trunc($1, created_at)` as
`date_trunc($2, created_at)` in GROUP BY does not identify the same grouping
expression to PostgreSQL. Reference the SELECT position instead:

```go
bucket := qs.DateTrunc(qs.Param("day"), qs.Col("created_at"))
query, args, err := qs.Select(bucket.As("bucket"), qs.CountAll()).From("events").
    GroupByExpr(qs.Ordinal(1)).OrderBy(qs.Ordinal(1).Asc()).ToSQL()
```

This binds `"day"` once. Keep the ordinal aligned with the projection order.

### Enrich a page with child aggregates

To page parents independently of their child counts, select the page in a CTE,
then attach a correlated aggregate with `LeftJoinExpr(qs.Lateral(...), qs.True())`.
Aggregate each child collection separately to avoid multiplying rows across
collections. Apply an outer ORDER BY as well; CTE ordering does not guarantee
final result order. The [database examples](integration/production_patterns_test.go)
exercise two collections and a parent with no children. Indexes and query plans
remain application concerns.

## Build and query JSON

qs models PostgreSQL's SQL/JSON syntax as typed builders: the `JSON_OBJECT` and
`JSON_ARRAY` constructors, the `JSON_OBJECTAGG` and `JSON_ARRAYAGG` aggregates,
`JSON()`, `JSON_SCALAR`, `JSON_SERIALIZE`, `IS JSON`, the query functions
`JSON_EXISTS`, `JSON_VALUE` and `JSON_QUERY`, and `JSON_TABLE`. The constructors,
the aggregates and `IS JSON` need PostgreSQL 16. The rest need PostgreSQL 17.

Each builder has only the clauses PostgreSQL accepts for its function, so most
combinations the server rejects do not compile. `JSONValue` has no wrapper
clause, `JSONExists` has no `Returning`, and `JSONQuery` has no `TrueOnError`.

### Build a JSON object

```go
user := qs.JSONObject(
    qs.JSONPair(qs.LiteralString("id"), qs.Col("id")),
    qs.JSONPair(qs.LiteralString("name"), qs.Col("name")),
    qs.JSONPair(qs.LiteralString("settings"), qs.FormatJSON(qs.Col("settings_text"))),
).AbsentOnNull().Returning(qs.TypeJSONB)

query, args, err := qs.Select(user.As("user")).
    From("users").
    Where(qs.Col("team_id").Eq(qs.Param(7))).
    ToSQL()
```

```sql
SELECT JSON_OBJECT(E'id' : "id", E'name' : "name", E'settings' : "settings_text" FORMAT JSON ABSENT ON NULL RETURNING jsonb) AS "user" FROM "users" WHERE ("team_id" = $1)
```

Arguments: `[7]`.

`FormatJSON` marks text that already holds JSON, so PostgreSQL embeds it instead
of quoting it as a string. `AbsentOnNull` omits a member whose value is NULL.
Without a NULL clause, objects keep NULL values and arrays drop them.

`JSONArray` builds an array from values, and `JSONArrayQuery` builds one from a
query that returns one column. `JSONObjectAggregate` and `JSONArrayAggregate` are
the SQL-standard aggregates. Both accept `Filter`, `Over` and `OverNamed`, and
`JSONArrayAggregate` also accepts `OrderBy`. `JSONObjectAgg` and `JSONAgg` call
PostgreSQL's `json_object_agg` and `json_agg` functions instead.

### Read values from a document

```go
doc := qs.Col("doc")
path := qs.LiteralString

query, args, err := qs.Select(
    qs.JSONValue(doc, path("$.customer.name")).As("customer"),
    qs.JSONValue(doc, path("$.total")).
        Returning(qs.TypeNumeric).DefaultOnEmpty(qs.LiteralInt(0)).As("total"),
    qs.JSONQuery(doc, path("$.items[*].sku")).WithWrapper().As("skus"),
).
    From("orders").
    Where(qs.JSONExists(doc, path("$.items[*] ? (@.qty >= $min)")).
        Passing("min", qs.Param(10).Cast(qs.TypeInt4)).
        Condition()).
    ToSQL()
```

```sql
SELECT JSON_VALUE("doc", E'$.customer.name') AS "customer", JSON_VALUE("doc", E'$.total' RETURNING numeric DEFAULT 0 ON EMPTY) AS "total", JSON_QUERY("doc", E'$.items[*].sku' WITH WRAPPER) AS "skus" FROM "orders" WHERE JSON_EXISTS("doc", E'$.items[*] ? (@.qty >= $min)' PASSING ($1)::integer AS "min")
```

Arguments: `[10]`.

`JSONValue` returns one SQL scalar, `JSONQuery` returns JSON, and `JSONExists`
tests whether the path matches anything. `Passing` supplies the value of a
`$name` variable in the path. Cast a parameter you pass this way, because
PostgreSQL otherwise reads it as text.

`DefaultOnEmpty` and `DefaultOnError` take a constant, a function call, or an
operator expression. PostgreSQL rejects a column reference there, and a value
that is a column fails rendering. Under the extended query protocol PostgreSQL
also rejects a value that is only a bound parameter; qs renders it and leaves
that error to the server.

`OmitQuotes` with `WithWrapper` or `WithConditionalWrapper` fails rendering,
because PostgreSQL rejects the pair.

`IsJSON` tests whether text is well-formed JSON. Narrow the test with `Object`,
`Array`, or `Scalar`, and negate it with `Condition().Not()`.

### Turn a document into rows

```go
items := qs.JSONTable(qs.Col("o", "doc"), "$.items[*]",
    qs.JSONOrdinality("position"),
    qs.JSONColumn("sku", qs.TypeText),
    qs.JSONColumn("qty", qs.TypeInt4).Path("$.quantity").DefaultOnEmpty(qs.LiteralInt(1)),
    qs.JSONExistsColumn("gift", qs.TypeBool),
    qs.JSONNested("$.parts[*]", qs.JSONColumn("part", qs.TypeText).Path("$.name")),
)

query, args, err := qs.Select(qs.Col("o", "id"), qs.Star("item")).
    FromExpr(qs.Table("orders").As("o")).
    CrossJoinExpr(qs.Lateral(items.As("item"))).
    ToSQL()
```

```sql
SELECT "o"."id", "item".* FROM ("orders" AS "o" CROSS JOIN LATERAL JSON_TABLE("o"."doc", E'$.items[*]' COLUMNS ("position" FOR ORDINALITY, "sku" text, "qty" integer PATH E'$.quantity' DEFAULT 1 ON EMPTY, "gift" boolean EXISTS, NESTED PATH E'$.parts[*]' COLUMNS ("part" text PATH E'$.name'))) AS "item")
```

Arguments: `[]`.

`JSONTable` is a row source, and `As` or `Ref` makes it a relation. PostgreSQL
lets it read a column of an earlier FROM item with or without `Lateral`, which
only writes the keyword. The row path and every column path are Go strings,
because PostgreSQL accepts only a string constant there. A column without `Path`
reads the member that has the column's name.

Column names and path names must be distinct within one `JSONTable`. A repeated
name, an empty path, or more column aliases in `As` than the table has columns
fails rendering. PostgreSQL names a row path without `PathName`
`json_table_path_0`, so that name fails rendering too until you name the row
path.

## Scan rows with projection declarations

The `github.com/jacoelho/qs/projection` companion package declares result columns
together with where each one is read. One declaration owns output order,
destinations and nesting, and drives both the query's output list and the scan of
each row:

```go
package documents

import (
    "github.com/jacoelho/qs"
    "github.com/jacoelho/qs/projection"
)

type User struct {
    ID    int64
    Name  string
    Email *string
}

type Row struct {
    Employee User
    Manager  *User
}

func users(r qs.Relation, prefix string) projection.Projection[User] {
    return projection.New(
        projection.Column(r.Col("id").As(prefix+"id"), func(u *User) *int64 { return &u.ID }),
        projection.Column(r.Col("name").As(prefix+"name"), func(u *User) *string { return &u.Name }),
        projection.ColumnNull(r.Col("email").As(prefix+"email"), func(u *User, v *string) { u.Email = v }),
    )
}

func employees() (qs.SelectBuilder, projection.Projection[Row]) {
    employee := qs.Table("users").As("employee")
    manager := qs.Table("users").As("manager")
    p := projection.New(
        projection.Embed(users(employee, ""), func(r *Row) *User { return &r.Employee }),
        projection.EmbedNull(users(manager, "manager_"), func(r *Row, u *User) { r.Manager = u }),
    )
    query := qs.SelectSlice(p.Expressions()).FromExpr(
        qs.LeftJoin(employee, manager).On(employee.Col("manager_id").Eq(manager.Col("id"))),
    )
    return query, p
}
```

For one-off queries with unique column names, ordinary `expr.As("label")` and
your driver's struct mapping are sufficient.

Declarations are immutable and safe to share, so declare one once per alias and
reuse it rather than rebuilding it per query.

### Read rows

Read a result set with `p.Collect(rows)`, or range over `p.All(rows)`. Both
accept `pgx.Rows` and `*sql.Rows`, report `rows.Err()` with the row errors, and
leave closing to you. They reuse their working memory from row to row, so prefer
them to calling `Scan` in a loop:

```go
rows, err := conn.Query(ctx, text, args...)
if err != nil {
    return nil, err
}
defer rows.Close()
return p.Collect(rows)
```

For one row, pass your driver's scan function: `p.Scan(row.Scan)` returns a
`Row`. `p.ScanInto(&row, row.Scan)` reads into a value you already hold, such as
the entity a RETURNING clause completes, and leaves the fields the declaration
does not name.

When you reuse one value across rows, each row overwrites the last in place.
Read every row you keep into its own value, and have each setter assign on nil
too, or a NULL keeps the previous row's value. `ColumnOrZero` assigns zero on
NULL, so it needs no such care.

qs still performs no I/O and imports no driver.

### Declare columns and groups

Build a declaration from these constructors:

- `Column` reads a required column. Its destination can be a Scanner that writes
  into the row.
- `ColumnNull` reads a nullable column and passes nil on NULL. Its `T` must work
  from a zero value, so a write-through Scanner cannot be used; hold the value in
  the Scanner and unwrap it in the setter.
- `ColumnOrZero` reads a nullable column and assigns the zero value on NULL. Use it
  where the row does not tell NULL from zero, or where the field's type represents NULL
  itself, such as `sql.NullString`. It has `Column`'s shape, reads as `ColumnNull`
  does, and never returns `ErrNull`.
- `Embed` nests a declaration in a field. `EmbedNull` nests it as an optional group,
  so one declaration serves both sides of a LEFT JOIN.
- `Map` reads a declaration into a value of its own, converts it after the row is read
  and passes the result to a setter. It runs while the result set is open and receives
  no context, so keep it CPU-only. The setter is the only write `Map` makes: one that
  assigns a field composes with other declarations for the same row, and one that
  assigns the whole value replaces what they read.
- `New` concatenates declarations in order.

Declare nullable columns with `ColumnNull` or `ColumnOrZero`. `Column` accepts
NULL outside a group when `T` can hold it, such as `*string`, but in a present
`EmbedNull` group NULL in a `Column` returns an error matching
`projection.ErrNull` that names the column.

An `EmbedNull` group is absent when all its columns are NULL, so a matched row
with only NULLs in the selected columns looks like no match.

A join key that no field holds can still decide presence. Declare it with a
`ColumnNull` whose setter does nothing:

```go
projection.ColumnNull(human.Col("user_id"), func(*Human, *int64) {})
```

`ColumnNull`, `ColumnOrZero`, and a `Column` in a group whose `T` is not a
Scanner read as `var v *T; rows.Scan(&v)` does, with that read's limits on each
driver. Declaring one panics on a pointer `T` (use `ColumnNull[string]` rather
than `*string`) and on the driver types qs detects as unreadable that way.
Detection is best effort.

`Column`, `ColumnOrZero` and `Embed` accessors run on a zero value at
declaration and panic if they return nil, so they must tolerate one and be
deterministic.

`go doc ./projection` states these contracts in full: the per-driver limits of
nullable reads, the types rejected at declaration, the order in which setters
and `Map` conversions run, and what a pointer kept by a setter refers to.

### Keep the output list aligned

Put `p.Expressions()` first in a SELECT or RETURNING list, unchanged. Scanning
is positional: other output before or among them breaks the correspondence, and
a reorder of equal width can go unnoticed.

SQL types are not checked against `T`, and convertible types are coerced by the
driver. A width mismatch fails only when a row is scanned. Each expression must
produce one result column.

Output selected after them is read by destinations your scan function appends.
Use this for a value that belongs to no row, such as a window count repeated on
every row:

```go
var total int64
row, err := p.Scan(func(dest ...any) error { return rows.Scan(append(dest, &total)...) })
```

With `Collect` or `All`, wrap the rows instead:

```go
type counted struct {
    projection.Rows
    total *int64
}

func (c counted) Scan(dest ...any) error { return c.Rows.Scan(append(dest, c.total)...) }

users, err := p.Collect(counted{Rows: rows, total: &total})
```

A value that belongs to each row stays in the declaration: `Embed` the
declaration beside a `Column` in a wrapper row.

Empty projections are valid containers: `qs.SelectSlice` with no expressions
fails rendering, while empty RETURNING adds nothing. Establish nonempty output
before executing DML that must return rows. `qs.Select` requires its first
column, so a literal SELECT list cannot be empty; `qs.SelectNoColumns` states
an intentional zero-column SELECT.

The [executable projection examples](projection/example_test.go) demonstrate
self-joins, `Map`, JSONB, RETURNING, `Collect`, `ScanInto`, `ColumnOrZero` with
a join key that no field holds, and a window count read beside each row.

## Use PostgreSQL types with pgx

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
`qs.Col("id").Eq(qs.Param(id).Cast(qs.TypeUUID))`. The cast affects the SQL
text; qs leaves the Go value unchanged for the driver to encode.

Dedicated descriptors cover common PostgreSQL types. For other built-in types,
domains, or extension types, use `TypeNamed`, such as
`qs.TypeNamed("pg_catalog", "int4range")`. `TypeArray` constructs an array type
from any descriptor.

### Use pgx values

pgx values can pass directly to `Param`, `Field[T]` comparisons, or standalone
`Values`. For write destinations, pass them through `Value`.

This example requires `github.com/jackc/pgx/v5/pgtype`, an open pgx connection
`conn`, and a context `ctx`:

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

The [database type examples](integration/type_examples_test.go) verify UUID and
nested composite results against PostgreSQL.

## Render SQL and handle errors

### Set render limits

`ToSQL` returns an owned SQL string and argument slice. Placeholders are always
`$1`, `$2`, and so on. To change a render's limits, pass `Options` to `ToSQLWith`
or `AppendWith`. Zero-valued fields select the following defaults:

| Option | Default | Valid non-default values |
|---|---|---|
| `MaxParameters` | 65,535 | 1 through 65,535 |
| `MaxDepth` | 256 | 1 through 4,096 |

For example, reject a query that binds more than 1,000 arguments:

```go
query, args, err := q.ToSQLWith(qs.Options{MaxParameters: 1000})
```

The parameter limit includes any existing arguments passed to an append method,
and the depth limit bounds nesting.

### Know what rendering checks

Rendering checks structure only. Schema objects, SQL types, privileges, driver
encoding, and whether your server supports a construct are left to PostgreSQL
and your driver.

qs has no PostgreSQL version option and renders the same SQL for every server.
A construct newer than your server, such as `MergeAction` (PostgreSQL 17) or
`Old` and `New` in RETURNING (PostgreSQL 18), fails at the database.

Operator validation enforces the standard PostgreSQL limit of 63 bytes without
proving that an operator exists or supports ordering.

PostgreSQL keeps the first 63 bytes of an identifier. Checks that names are
distinct, such as CTE names, target columns, and `JSONTable` columns, compare
names by that part, so two long names with the same first 63 bytes fail
rendering as duplicates.

### Handle render errors

Use `errors.Is` to identify the cause of a render error:

| Cause | Meaning |
|---|---|
| `ErrInvalid` | Invalid query structure, input, or options |
| `ErrParameterLimit` | Too many arguments |
| `ErrDepth` | Nesting limit exceeded |

For more context, use `errors.As` to obtain `*qs.RenderError`, which provides
these fields:

- `Clause` identifies the construct or option that failed.
- `Detail` describes the failure.
- `Path` lists structural locations from outermost to innermost, with one-based indexes.

Each error owns its path, so later renders cannot change it. Paths identify
composition boundaries through static roles and indexes; they do not include
bound values, caller names, SQL fragments, or every internal node.

The cause and path ordering are contracts. `Error()` text is diagnostic output
and should not be parsed as a machine format.

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
q := qs.SelectCols("id").From("users").Where(qs.Col("active").Eq(qs.Param(true)))
sqlBuffer := make([]byte, 0, 256)
argBuffer := make([]any, 0, 8)

sqlBuffer, argBuffer, err := q.AppendSQL(sqlBuffer[:0], argBuffer[:0])
```

Keep the buffers and bound values unchanged while the driver uses them. Once
execution finishes, clear argument references before reusing the storage, for
example with `clear(argBuffer)` before the next append.

Successful appends can avoid allocations when the query is prebuilt and the
buffers have enough capacity. Construction, owned output, and failures have
separate costs, so this is not a zero-allocation guarantee for every query.

`make benchmark` reports both costs. `BenchmarkBuildAndRender` builds a
statement and renders it, which is what a request that builds its query pays.
`BenchmarkRender` renders a prebuilt statement into reused buffers.

## Reuse and share builders

Builders, expressions, and relations are immutable values. A builder method
returns a new builder and never changes its receiver, so you can branch a base
query freely and render it from several goroutines without synchronization.

A subquery or CTE captures the builder value it was given. Builders derived
from that value later do not change it:

```go
orders := qs.SelectCols("user_id").From("orders")
buyers := qs.SelectCols("id").From("users").Where(qs.Col("id").InQuery(orders))

orders = orders.Where(qs.Col("total").Gt(qs.Param(100)))
query, args, err := buyers.ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("id" IN (SELECT "user_id" FROM "orders"))
```

qs does not copy bound values. Keep the application objects you bind unchanged
until execution completes.

### Know which calls append and which replace

Repeated calls on one builder combine as follows:

| Builder | Method | Effect |
|---|---|---|
| SELECT | `From`, `FromExpr` | Replaces the FROM list |
| Pending UPDATE | `From`, `FromExpr` | Appends FROM items |
| Pending DELETE | `Using`, `UsingExpr` | Appends USING items |
| INSERT SELECT | `From` | Replaces the source query |
| SELECT | `Where` | Appends conditions with AND |
| Pending UPDATE, pending DELETE | `Where`, `WhereCurrentOf`, `AllRows` | Completes the statement; the result has none of the three |
| SELECT and other queries | `Limit`, `LimitExpr`, `LimitAll`, `LimitWithTies`, `LimitWithTiesExpr` | Replaces the row limit |
| Pending UPDATE, assignment INSERT | `Set` | Appends assignments |
| Row INSERT | `Values`, `ValuesSlice`, `Rows` | Appends rows |

For example, `SelectCols("id").From("a").From("b")` selects from `b` only,
while `Update("target").From("a").From("b")` retains both source tables.

## Develop qs

Run these checks from the repository root:

```sh
make golangci-lint
make test
```

`make golangci-lint` installs the pinned linter in `.bin/` when needed and
checks both the root packages and the PostgreSQL integration module.

Regular tests need no database or database driver. Make and CI bound test
parallelism; use `TEST_PROCS` to override the Make default.

Pull request checks run the linter, the generated-code check, regular tests,
and race tests.

Use these targets for additional development tasks:

| Task | Command |
|---|---|
| Detect data races | `make race` |
| Analyze Go code | `make vet` |
| Measure test coverage | `make coverage` |
| Regenerate code | `make generate` |
| Verify generated code without changes | `make generate-check` |
| Run benchmarks | `make benchmark` |

`make generate` runs `go run ./internal/buildergen`, which writes the methods
every statement builder repeats and the typed tuples. `make generate-check`
runs it with `-check`, which reports stale output without writing.

To run database tests, supply a PostgreSQL connection string:

```sh
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
make integration
```

The `integration/` module owns the pgx dependency and uses the local qs checkout.
The integration suite supports PostgreSQL 15 through 18, and pull request checks
run it against each of them with the race detector. This coverage verifies the
syntax qs renders without claiming support for every combination of schema,
types, and extensions.

## Find references

- [Executable examples](example_test.go): examples with expected SQL and arguments.
- `go doc .`: the full exported API.
- `go doc ./projection`: reusable result declarations and their ownership contracts.
- [Architecture guide](internal/ARCHITECTURE.md): boundaries, ownership, and design decisions.
- [PostgreSQL corpus](internal/postgrescorpus/README.md): construction measurements and fixture maintenance.

The pinned corpus holds 28,197 query occurrences from PostgreSQL 18's regression
SQL. The public API builds 27,887 of them (98.90%) into SQL that reparses to the
source tree; 220 use syntax qs omits by design, 50 are refused at render time and
40 have no construction. This measures SQL construction, not schema validity,
query results, or query plans.

qs uses the [MIT license](LICENSE). PostgreSQL-derived fixtures retain their
[upstream notice](internal/postgrescorpus/NOTICE.postgresql).
