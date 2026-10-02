# Querysmith (qs)

PostgreSQL query construction for Go. Module `github.com/jacoelho/qs`, package
`qs`; applications can import it as `q` when that reads better. The module
targets Go 1.27. No runtime dependencies, reflection, `unsafe`, connection pool,
execution layer, scanning layer or schema introspection.

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

Arguments: `[]any{true, 20}`. The caller executes the query:

```go
if err != nil {
    return err
}
rows, err := tx.Query(ctx, query, args...) // pgx connection or transaction
if err != nil {
    return err
}
defer rows.Close()
// Scan with the driver; check rows.Err() after iteration.
```

`database/sql` callers use their driver's `QueryContext` instead. Argument
encoding remains the driver's responsibility: a PostgreSQL array or custom
value is not automatically portable between all drivers.

## Delivery status

The core tests pass on **Go 1.27.0**. The optional pgx integration suite has
passed against **PostgreSQL 18.6**, including typed JSON extraction,
operators and updates. This source tree has not been published as a release.
See [validation](docs/VALIDATION.md) for the scope and historical evidence.

[Coverage and exclusions](docs/COVERAGE.md) distinguish tested native features,
composition recipes, and functionality outside this library's scope.
A raw SQL escape hatch is not counted as first-class feature coverage.
The pinned PostgreSQL regression corpus measures [99.65% query construction
support](docs/POSTGRES.md), including 99.50% in its planner subset. All four 98%
gates pass, including distinct query shapes. The report defines the denominator
and records remaining gaps.

## Selection choices

`Select` accepts expressions. `SelectCols("id", "name")` is shorthand for
`Select(Col("id"), Col("name"))`. Use `[]qs.Expr` for reusable projections.

`SelectSQL`/`ColumnsSQL` join trusted raw projection fragments with commas; the
builder does not parse them or infer their column counts. `Col("id::text")`
is an identifier; write `Col("id").Cast(Text)` for a cast expression.
Projection order follows insertion order. See [selection choices](docs/API.md#select-versus-selectsql).

## NULL, absence and zero

```go
type Patch struct {
    Email qs.Optional[qs.Null[string]]
}

qs.None[qs.Null[string]]()          // Absent: do not change the email.
qs.Some(qs.NullOf[string]())       // Present SQL NULL.
qs.Some(qs.NonNull(""))           // Present empty string, not NULL.
qs.Some(qs.NonNull("a@example.com"))
```

Use `ParamNull`, `SetNullable`, `IsDistinctFrom` or `IsNotDistinctFrom` to
explicitly unwrap `Null[T]`. Binding the wrapper with plain `Param` is a build
error. An absent optional value must be checked before binding.

```go
q := qs.Update("users").Where(qs.Eq("id", id))
if patch.Email.Present {
    q.Set(qs.SetNullable("email", patch.Email.Value))
}
```

An entirely absent patch should be an application no-op; an UPDATE without any
assignment is a build error. Equality is never silently rewritten based on a
value: use `IsNull("email")`, or use `IsNotDistinctFrom` for NULL-safe equality.
`qs.Eq[any]("email", nil)` deliberately emits ordinary `= $n` with a nil argument.

Empty membership and conjunction contracts:

| Operation | SQL result |
|---|---|
| `In[int]("id")` | `FALSE` |
| `NotIn[int]("id")` | `TRUE` |
| `And()` | `TRUE` |
| `Or()` | `FALSE` |
| zero `Condition{}` | Rendering error |

NULL-containing non-empty membership retains PostgreSQL's three-valued logic.
A nil slice supplied to `In` is an empty set; a nil slice supplied to
`ArrayParam` is one driver-encoded value and can be SQL NULL. These are different
operations, not interchangeable conveniences.

## Optional stronger typing

```go
id := qs.Typed[int64]("u.id")
name := qs.Typed[string]("u.name")

q := qs.Select(id.Expr(), name.Expr()).FromExpr(qs.Table("users").As("u"))
q.Where(id.Eq(42))
// id.Eq("42") does not compile.
```

`Field[T]` relates operands/assignment values in Go; it is not a schema proof.
It does not prove nullability after an outer join, custom operator availability,
column existence or codec support. Typed tuples of degree 2–4 and arbitrary
heterogeneous `Row(...)` comparisons support keyset predicates. Mixed sort
directions and nullable cursor keys require explicit handling by the caller.

## Typed JSON and JSONB

```go
doc := qs.JSONBCol("payload")
q := qs.Select(doc.PathText("items", "0", "name").As("name")).
    From("events").
    Where(doc.Key("items").Index(-1).TextKey("name").Eq("Ada"),
        doc.Contains(qs.JSONBParam(`{"active":true}`)))

// TextKey returns Field[string]; .Eq(42) does not compile.
// JSONCol supports extraction; JSONBCol also supports JSONB operators.
```

`Key`/`Index` emit `->`; `TextKey`/`TextIndex` emit `->>`.
`Path`/`PathText` emit `#>`/`#>>`. Keys, paths and values bind as arguments;
integer indices explicitly cast to PostgreSQL integer, including zero and
negative indices. Paths and key lists use individually bound `text[]`
constructors, so they do not require a driver's array codec. Empty paths are
valid. Indices outside the PostgreSQL integer range are build errors.

JSONB methods include `Contains`, `ContainedBy`, `HasKey`, `HasAnyKeys`,
`HasAllKeys`, `DeleteKey`, `DeleteIndex`, `DeleteKeys`, `DeletePath`, `Concat`,
`PathExists` and `PathMatches`. `PathExists` (`@?`) tests whether a path returns
items; `PathMatches` (`@@`) evaluates a JSONPath predicate and can return SQL NULL.

```go
q := qs.Update("events").
    Set(doc.Set(doc.Concat(qs.JSONBParam(`{"active":true}`)))).
    Where(doc.PathText("items", "0", "name").Eq("Ada"))
```

`JSONParam`/`JSONBParam` accept encoded strings or byte slices and add SQL casts;
they do not marshal or validate JSON. `JSONExpr`/`JSONBExpr` assert the type of
an existing expression without casting. Missing keys and out-of-range indices
produce SQL NULL. JSON null stays a JSON value through document extraction;
text extraction turns it into SQL NULL. `Field[string]` describes the Go
payload type, not SQL non-nullability.

## CTEs and global parameter numbering

```go
recent := qs.CTE("recent", qs.SelectCols("user_id").
    From("orders").Where(qs.Gt("total", 100)))

q := qs.SelectCols("u.id").
    With(recent).
    FromExpr(qs.InnerJoin(qs.Table("users").As("u"), recent.Ref()).
        On(qs.EqColumns("u.id", "recent.user_id")))
```

A single renderer traverses CTEs, projections, joins, subqueries, conditions and
returning expressions in SQL order. A nested statement is never independently
built and concatenated. Reusing a subquery in two locations binds each
occurrence independently. Recursive terms use `CTERef("name")` or a structural
table reference, not a pointer cycle. CTEs support materialisation controls,
`SEARCH`, `CYCLE` and data-modifying bodies where PostgreSQL permits them.

### Types at query boundaries

Use an explicit cast when PostgreSQL has no typed column or operator context:
`qs.Param(1).Cast(qs.Int4)`.
For example, an otherwise-unconstrained parameter projected by a CTE can resolve
to SQL text before an outer numeric operation is analysed. Go generics do not
transmit PostgreSQL parameter type OIDs. The builder deliberately does not guess
a database type from an arbitrary Go value.

## Mutation and advanced query support

For a type-safe single row, pair each column with its value:

```go
id, name := qs.Typed[int]("id"), qs.Typed[string]("name")
q := qs.InsertInto("users").Set(id.Set(42), name.Set("Ana"))
// INSERT INTO "users" ("id", "name") VALUES ($1, $2)
```

`Field[T].Set(T)` requires exactly one value of type `T` at compile time. INSERT
derives both lists from these assignments, so their counts cannot diverge.
Variadic `Columns(...string).Values(...any)` supports dynamic and multiple rows;
Go cannot check their lengths at compile time. Known width mismatches fail when
rendering with `ErrInvalid`.

```go
q := qs.InsertInto("users").
    Columns("id", "name").Values(42, "Ana").
    OnConflict(qs.ConflictColumns("id").
        DoUpdate(qs.SetExpr("name", qs.Excluded("name")))).
    ReturningCols("id")
```

The supported structural families include all ordinary join kinds, lateral and
derived tables, table functions and ordinality, grouping sets, rollup/cube,
all six set-operation variants, aggregate filters/ordering, window frames,
locking, `INSERT ... SELECT`, conflict inference and update predicates,
`UPDATE ... FROM`, `DELETE ... USING`, row assignments, `MERGE` action categories,
`RETURNING` old/new rows, `EXPLAIN`, `TRUNCATE`, arrays, JSONB/SQL-JSON, ranges and
full-text expressions. See [API](docs/API.md), [coverage](docs/COVERAGE.md) and
[architecture](docs/ARCHITECTURE.md) for the boundaries of each family.

`MERGE` has no recursive-WITH method. PostgreSQL UPDATE/DELETE have no direct
ORDER BY/LIMIT methods; the cookbook tests demonstrate selecting-CTE recipes.
`Call`, `Operator`, `QualifiedOperator` and trusted fragments extend the
expression vocabulary without runtime registration or reflection.

## Rendering and placeholders

Construction stores query structure and values; rendering produces SQL and
arguments. Choose the placeholder style for each render:

```go
q := qs.SelectCols("id").From("users").Where(qs.Eq("name", "O'Reilly; ? $1"))

query, args, err := q.ToSQL() // $1, $2, ... by default.
query, args, err = q.ToSQLWith(qs.Options{PlaceholderStyle: qs.Question})
// SELECT "id" FROM "users" WHERE ("name" = ?)
// args: []any{"O'Reilly; ? $1"}
```

Both styles retain the same argument order through nested queries and CTEs.
Rendering options do not change the builder. Quotes, semicolons, literals and
JSONB operators (`?`, `?|`, `?&`, `@?`) remain intact because only parameter
nodes emit placeholders. Use the default `qs.Dollar` with PostgreSQL/pgx;
`qs.Question` requires a consumer that understands question-mark parameters
and PostgreSQL operators. The SQL dialect remains PostgreSQL.

For caller-owned storage, use `q.AppendWith(buf, args, options)` or the free
`qs.AppendWith(buf, args, q, options)` function.
The [API migration table](docs/API.md#rendering-api-migration) lists the removed
`Build` names and their replacements.

## Safety and ownership

`Col("a.b")` means `"a"."b"`; `Ident("a.b")` means one identifier `"a.b"`.
Dynamic sort keys should be selected from an application allowlist. Raw fragments
are trusted syntax and must not contain hand-numbered binds or interpolated
request values. The renderer does **not** rewrite question marks:

```go
condition := qs.AsCondition(qs.Fragment(
    qs.Col("metadata"), qs.UnsafeSQL(" ? "), qs.Param("reference"),
))
```

Builders are mutable. List methods generally append; `From`/`FromExpr` replace
the FROM list, and singular options replace earlier values. `Clone` makes an
independent statement graph, including nested builders, but deliberately does
not deep-copy arbitrary bound values. Expressions, relations, CTE descriptors,
window specifications and conflict clauses use immutable value-style methods.
Nested statement pointers remain live references until rendering; clone a base
query before branching.

Read-only concurrent renders are supported after the graph and bound values are
stable. Concurrent mutation, reset or modification of parameter slices is not.
`ToSQL` owns its SQL string and argument slice, not the objects referred to by
individual arguments. Keep bound byte slices, arrays and pointers stable until
the caller's execution completes.

```go
buf, args, err = q.AppendSQL(buf[:0], args[:0])
```

`AppendSQL` uses caller-owned storage; placeholder numbering starts after the
existing arguments. Errors return the original slices, lengths and prefix
contents. Unused backing-array capacity may have been modified. When reusing
arguments after a shorter query, clear old entries first to avoid retaining
references: `clear(args); args = args[:0]`. Do not reuse buffers while another
goroutine or the driver still needs their contents.

`ToSQLWith`/`AppendWith` accept placeholder, version and resource options.
`PostgreSQLVersion` has named constants `PostgreSQL12` through `PostgreSQL18`.
Zero selects PostgreSQL18; unknown values return `ErrInvalid`. Selected features
have lower-version gates. This
is not complete version/schema/type validation, especially for trusted SQL and
custom functions.
`RequireWhere` is a structural mutation guard, not authorisation. RLS, roles,
tenant context and transactions stay in the execution layer. `TRUNCATE` is not
row-filtered by RLS; granting it requires a separate privilege decision.

## Development

```sh
GOMAXPROCS=2 go test -p=2 -parallel=8 ./...
GOMAXPROCS=2 go test -race -p=1 -parallel=8 ./...
GOMAXPROCS=2 go vet -p=2 ./...
python3 scripts/generate.py
go test -run='^$' -bench=. -benchmem -count=3 .

# Optional live tests; requires a reachable PostgreSQL 18 instance.
export QS_TEST_DSN='postgres://user:password@localhost/database?sslmode=disable'
GOMAXPROCS=2 go test -tags postgres -race -p=2 -parallel=8 -timeout=5m -v ./integration
```

Regular tests run offline and do not compile pgx. Live test files use the
`postgres` build tag; opted-in runs require `QS_TEST_DSN`. The root module pins
pgx for those tests, while the library imports only the standard library.
`go mod tidy` maintains dependencies across build tags.

Regular tests include all 28,097 verified PostgreSQL corpus occurrences with
fixed SQL and arguments. Eight packages split the typed builders; each streams
its own frozen JSONL data in bounded groups. `-p=2` limits concurrent package
builds and test processes; `-parallel=8` limits concurrent cases per process.
Race builds use `-p=1` because instrumentation increases compiler memory.
Make targets default to `TEST_PROCS=2`; CI also sets `GOMAXPROCS=2`.
See [the corpus contract](docs/POSTGRES.md) for provenance and regeneration.

License: MIT. PostgreSQL-derived fixtures retain their
[upstream notice](internal/postgrescorpus/NOTICE.postgresql).
