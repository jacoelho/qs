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
[Know what rendering checks](docs/rendering.md#know-what-rendering-checks).

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
    query, args, err := qs.Select("id", "name").
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

## Know three rules

**Keep every result.** Builders are immutable values: a method returns a new
builder and leaves its receiver unchanged, so write `q = q.Where(...)`. A call
whose result is dropped does nothing, and neither the compiler nor rendering
reports it; the [`qsvet` vet tool](docs/builders.md#catch-a-discarded-builder-result)
does.

**A string is one identifier.** `From("public.users")` renders
`"public.users"`, one name with a dot in it. For a qualified name pass each
part: `qs.Table("public", "users")` and `qs.Col("u", "id")`. Clauses such as
`Select`, `From`, `GroupBy`, and `Returning` take names or these values, one
kind per call.

**Application values are bound, never written into the SQL.** `qs.Param(v)`
binds a value in an expression and `qs.Value(v)` binds one at a write
destination. Trusted SQL constructors such as `UnsafeSQL` are the only way
text reaches the statement unquoted; never pass request text to them.

[Bind values and name identifiers](docs/values-and-identifiers.md) covers
these rules in full, including how to pass a list built at run time.

## Common tasks

Each task below is covered in more depth by the guide it links to.

### Add optional filters

`Where` appends conditions with AND, so ordinary Go control flow adds a filter
only when its input is present:

```go
func filteredUsers(tenantID int, prefix string, roles []string) qs.SelectBuilder {
    q := qs.Select("id", "email").
        From("users").
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

See [Filter rows](docs/filtering.md) for literal text matching, typed fields,
and array parameters.

### Page results

One base query serves every page, because a derived builder never changes its
source:

```go
base := qs.Select("id").From("users").Where(qs.Col("active").Eq(qs.Param(true)))

first := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(0)
next := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(10)

query, args, err := next.ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("active" = $1)
ORDER BY "id" ASC LIMIT $2 OFFSET $3
```

Arguments: `[true 10 10]`.

See [Page results](docs/pagination.md) for keyset cursors with `SeekAfter`,
row locks, and `CountRows`.

### Join tables

```go
users := qs.Table("users").As("u")
teams := qs.Table("teams").As("t")

query, args, err := qs.Select(users.Col("id"), teams.Col("name")).
    From(users).
    LeftJoin(teams, teams.Col("id").Eq(users.Col("team_id"))).
    Where(users.Col("active").Eq(qs.Param(true))).
    ToSQL()
```

```sql
SELECT "u"."id", "t"."name"
FROM ("users" AS "u" LEFT JOIN "teams" AS "t" ON ("t"."id" = "u"."team_id"))
WHERE ("u"."active" = $1)
```

Arguments: `[true]`.

See [Compose joins, CTEs, and reports](docs/composition.md) for CTEs, CASE,
lateral joins, and set operations.

### Insert a row

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    Returning("id").
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
    Where(qs.Col("id").Eq(qs.Param(42))).
    Returning("id", "name").
    ToSQL()
```

```sql
UPDATE "users" SET "name" = $1 WHERE ("id" = $2) RETURNING "id", "name"
```

Arguments: `[Ana 42]`.

### Delete a row

```go
query, args, err := qs.DeleteFrom("users").
    Where(qs.Col("id").Eq(qs.Param(42))).
    Returning("id").
    ToSQL()
```

```sql
DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"
```

Arguments: `[42]`.

An UPDATE or DELETE has no `ToSQL` until `Where`, `WhereCurrentOf`, or
`AllRows` says which rows it changes, so a mutation that forgot its predicate
does not compile.

### Update a row on conflict

```go
query, args, err := qs.InsertInto("users").
    Columns("id", "name").
    Values(qs.Value(42), qs.Value("Ana")).
    OnConflict(
        qs.ConflictColumns("id").
            DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name")))),
    ).
    Returning("id").
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
RETURNING "id"
```

Arguments: `[42 Ana]`.

See [Write rows](docs/writes.md) for inserts from slices and arrays, row
selection for UPDATE and DELETE, and conflict targets.

### Handle a render error

`ToSQL` returns an error when the statement's structure is invalid. Identify
the cause with `errors.Is`:

```go
_, _, err := qs.Values(1).Row(2, 3).ToSQL()
if errors.Is(err, qs.ErrInvalid) {
    // err: qs: invalid query (VALUES): row 2: expected 1 value, got 2 values [path: VALUES → row[2]]
}
```

Rendering checks structure only; the database still checks schema, types, and
privileges. See [Render SQL and handle errors](docs/rendering.md).

## Read the guides

| Guide | Covers |
|---|---|
| [Bind values and name identifiers](docs/values-and-identifiers.md) | `Param`, typed fields, shared parameters, NULL, naming, run-time lists, trusted SQL |
| [Filter rows](docs/filtering.md) | Optional filters, literal text matching, array parameters |
| [Page results](docs/pagination.md) | Ordering, limits, keyset cursors, row locks, counts |
| [Write rows](docs/writes.md) | INSERT, UPDATE, DELETE, columnar batches, ON CONFLICT |
| [Compose joins, CTEs, and reports](docs/composition.md) | CTEs, joins, CASE, lateral joins, set operations |
| [Build and query JSON](docs/json.md) | SQL/JSON constructors, document operators, `JSON_TABLE` |
| [Scan rows with projection declarations](docs/projection.md) | The `projection` package: declaring and reading result rows |
| [Use PostgreSQL types with pgx](docs/pgx-types.md) | Casts, pgx values, composite types |
| [Render SQL and handle errors](docs/rendering.md) | Render limits, what rendering checks, error causes, buffer reuse |
| [Reuse and share builders](docs/builders.md) | Immutability, the `qsvet` tool, which calls append or replace |
| [Develop qs](docs/development.md) | Checks, code generation, database tests |

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
