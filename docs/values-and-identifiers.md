# Bind values and name identifiers

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

## Compare values

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

query, args, err := qs.Select(id.Expr()).
    From(users).
    Where(
        id.Gt(1000),
        role.In("admin", "editor"),
        users.Col("score").Gte(qs.Param(4.5)),
        users.Col("created_at").Lt(qs.Now()),
        users.Col("deleted_at").IsNull(),
    ).
    ToSQL()
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
fields, use the `Field` variants, such as `EqField`. For any other operand,
leave the field with `Field.Expr`, or state the operand's type with `Typed`:

```go
total := qs.TypedCol[int64]("o", "total")

total.Expr().Ne(qs.Coalesce(qs.Col("o", "estimate"), qs.LiteralInt(0)))
total.LteField(qs.Typed[int64](qs.Col("o", "credit").Sub(qs.Col("o", "spent"))))
```

These render `("o"."total" <> coalesce("o"."estimate", 0))` and
`("o"."total" <= ("o"."credit" - "o"."spent"))`. `Field.Cast` adds a cast and
returns an expression, as `Expr.Cast` does.

## Bind one value once

`Param` binds again at each use, so a value used three times is three
arguments. `SharedParam` binds a value that renders as the same `$n` wherever
the returned expression appears in one statement, including nested queries:

```go
tenant := qs.SharedParam("acme")
query, args, err := qs.Select("id").
    From("documents").
    Where(qs.Col("tenant").Eq(tenant), qs.Col("owner_tenant").Eq(tenant)).
    ToSQL()
```

```sql
SELECT "id" FROM "documents" WHERE ("tenant" = $1) AND ("owner_tenant" = $1)
```

Arguments: `[acme]`.

Each `SharedParam` call is a distinct parameter, so two calls with equal values
bind twice. PostgreSQL infers one type for a parameter from all of its uses:
when two uses would infer different types, `Cast` the ones that differ.

Where PostgreSQL cannot infer a type at all, such as a select list, a CASE
branch or `$1 IS NULL`, keep the cast with the value and reuse both:
`id := qs.SharedParam(v).Cast(qs.TypeUUID)` renders `($1)::uuid` at every use.

## Handle SQL NULL

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
values from present ones. The [executable examples](../example_test.go) show how to
use them together.

## Name columns and tables

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

A clause that names columns or tables, such as `Select`, `From`, `GroupBy`, and
`Returning`, takes names or values: one identifier per string, or `qs.Expr`
and `qs.Relation` values. For a qualified name, pass the value, as in
`Select(qs.Col("u", "id"))` and `From(qs.Table("public", "users"))`. One call
takes one kind, so `Select("id", qs.Col("u", "name"))` does not compile.

`Select` takes column names or expressions, such as a qualified name, a cast,
or an alias. One call takes one kind:

```go
q := qs.Select(
    qs.Col("id"),
    qs.Col("name"),
    qs.Col("created_at").Cast(qs.TypeText).As("created"),
).
    From("users")
```

A relation's alias used as a column is PostgreSQL's whole-row reference:
with `qs.Table("sessions").As("s")`, `qs.ToJSONB(qs.Col("s"))` renders
`to_jsonb("s")`, the row as one JSON object.

`Relation.Col` qualifies a column by the relation's alias, or by its table name
when it has no alias. `qs.Table("public", "settings").Col("a.b")` renders
`"public"."settings"."a.b"`. Use `relation.Star()` for a wildcard;
`relation.Col("*")` refers to a column named `"*"`.

## Pass a list built at run time

Two rules cover every clause that takes a list.

A clause that names columns or tables is variadic, so spread the list:
`Select(columns...)`, `From(tables...)`, `Returning(columns...)`. `Where` on
SELECT, `OrderBy`, `Rows`, and the set operations are variadic too.

A clause that needs at least one element takes one or more elements, or the
list itself with no `...`: `Set(qs.Set("name", qs.Value("Ana")))` or
`Set(assignments)`. This holds for `Set` on INSERT and UPDATE, `DoUpdate`,
MERGE's `ThenUpdate` and `ThenInsert`, `ConflictColumns`, `ConflictIndex`,
`FromArrays`, and `UnnestAs`. A call with no arguments, such as `Set()`, does
not compile, and an empty list fails at render time. `Set` is the exception:
it appends, so an empty list adds nothing and rendering fails only when the
statement ends with no assignments.

These methods are generic, so they cannot satisfy an interface and cannot be
taken as a method value without their type argument: write `q.From[string]`,
not `q.From`.

## Use trusted SQL

`SelectSQL`, `UnsafeSQL`, `Fragment`, and `StatementSQL` let you compose trusted
application-authored SQL with qs expressions.

Never pass request text to these constructors; keep request values in bind
parameters. qs numbers parameters itself, so trusted SQL that contains a
numbered placeholder such as `$1` fails rendering with `ErrInvalid`: it would
share a slot with a bound value. Put the value beside the text instead:

```go
qs.Fragment(qs.UnsafeSQL("similarity(name, "), qs.Param(name), qs.UnsafeSQL(") > 0.3"))
```

Each `UnsafeSQL` text must also be lexically complete. Rendering fails when it
ends inside a string, a quoted identifier, a comment, or a dollar-quoted
string, because the open token would swallow whatever qs renders next. A `$`
inside a string, a quoted identifier, a comment, a dollar-quoted body, or an
identifier such as `t$1` is left alone. Strings are read as PostgreSQL reads
them with `standard_conforming_strings` on.

`Fragment` adds nothing between its parts, so it also fails rendering when two
neighbours would be read as one token: `UnsafeSQL("x = t")` followed by a
`Param` would render `t$1`, and `UnsafeSQL("a -")` followed by a negative
literal would render a `--` comment. Put a space in the trusted text.

When qs combines a trusted condition with another, it parenthesizes the trusted
condition. This applies to two `Where` conditions, a `SeekAfter` cursor, and
`And`, `Or` and `Not`. `Where(qs.UnsafeSQL("a OR b").Condition())` with a
cursor renders `WHERE (a OR b) AND (...)`. Other operators render trusted SQL
verbatim, so call `Parenthesized` where its operators could bind to a
neighboring operand.

## Reach syntax qs does not model

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

qs has helpers for the functions its own features need, not for every
PostgreSQL function. Call the rest by name: `qs.Call("md5", name)` renders
`"md5"("name")` and `qs.Call("right", name, qs.LiteralInt(4))` renders
`"right"("name", 4)`. `Trim` is `btrim` with one argument; to name the
characters or the end to trim, use
`qs.TrimSyntax(name, qs.TrimBothDirection, qs.LiteralString("-"))`, which
renders `TRIM(BOTH E'-' FROM "name")`.

Database authorization remains your application's responsibility, including
database roles, row-level security (RLS), tenant context, and transactions.
