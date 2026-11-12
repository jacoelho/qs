# Write rows

Write destinations in INSERT, MERGE, and assignments accept `WriteValue`
descriptors: `Value` binds application data, including nil as SQL NULL; `Write`
supplies a SQL expression; and `Default` requests the server default. These
descriptors cannot be used as general expressions or bound application values.

The following examples assume that `users.id` is a primary key. As with SELECT,
handle any render error before passing the SQL and arguments to your driver.

## Insert a row

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

If you need an explicit target list, select `Columns`, by names or by target
expressions, before choosing `Values`, `From`, or `DefaultValues`. Assignment
INSERTs start with `Set` directly on `InsertInto` and cannot also have an
explicit target list.

Each source selection returns a completed builder with the methods of that
source. A completed INSERT builder cannot change the target list or switch
source. Default-value INSERT builders also omit identity overriding.

## Insert columns and values from slices

```go
columns := []string{"id", "name"}
row := []qs.WriteValue{qs.Value(42), qs.Value("Ana")}
query, args, err := qs.InsertInto("users").
    Columns(columns...).
    Rows(row).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") VALUES ($1, $2)
```

Arguments: `[42 Ana]`.

`Columns` and `Rows` copy the slice; bound application values are not copied.
Empty column lists and empty rows fail at render time. For positional
insertion, omit the column selector; `Rows` can start a positional INSERT or
append rows to a row INSERT.

An assignment INSERT takes its assignments the same way as UPDATE: one or
more, or a list passed bare, as in `InsertInto("users").Set(assignments)`; see
[Pass a list built at run time](values-and-identifiers.md#pass-a-list-built-at-run-time).

To insert rows sized by your data, pass them all to `Rows`, for example
`InsertInto("users").Columns("id", "name").Rows(rows...)` with `rows` a
`[][]qs.WriteValue`. `Values` copies the row list on every
call, so a loop that adds one row per call does work quadratic in the number of
rows; `Rows` copies once. A VALUES rowset has the same pair: `Row` and
`RowExpr` add one row, `Rows` and `RowsExpr` add many. No rows fails at render
time.

## Insert columnar batches

For data already arranged in columns, bind one array per column with
`FromArrays`. Each `NamedArray` pairs a target column with the array that fills
it, and the parameter count stays proportional to columns:

```go
ids := []int32{42, 43}
names := []string{"Ana", "Ben"}
query, args, err := qs.InsertInto("users").
    FromArrays(
        qs.NamedArray("id", qs.ArrayParam(ids, qs.TypeInt4)),
        qs.NamedArray("name", qs.ArrayParam(names, qs.TypeText)),
    ).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name") SELECT * FROM unnest(($1)::integer[], ($2)::text[])
```

Arguments: `[[42 43] [Ana Ben]]`.

The driver encodes the arrays. When the rows need more than the arrays, such as
a constant column, a join, or the element position, build the relation with
`UnnestAs` and select from it:

```go
batch := qs.UnnestAs("b",
    qs.NamedArray("name", qs.ArrayParam(names, qs.TypeText)),
    qs.NamedArray("kind", qs.ArrayParam(kinds, qs.TypeText)),
).WithOrdinality()
rows := qs.Select(
    batch.Col("name"),
    batch.Col("kind"),
    batch.Col("ordinality").As("position"),
).
    From(batch)
query, args, err := qs.InsertInto("events").
    FromNamed(rows).
    ToSQL()
```

```sql
INSERT INTO "events" ("name", "kind", "position")
SELECT "b"."name", "b"."kind", "b"."ordinality" AS "position"
FROM unnest(($1)::text[], ($2)::text[]) WITH ORDINALITY AS "b" ("name", "kind")
```

`FromNamed` takes the target columns from the query's output columns: a column
reference names its column, and `As` names anything else. Each target is
written beside the expression that fills it, so the two cannot be reordered
apart as a `Columns` list and a projection can. Rendering fails for an output
column that has neither form, such as a star or an unaliased expression. It
works for any SELECT, for example one over `jsonb_array_elements`:

```go
e := qs.TableFunc(qs.JSONBArrayElements(qs.JSONBParam(payload).Expr())).
    WithOrdinality().
    As("e", "item", "position")
item := qs.JSONBExpr(e.Col("item"))
rows := qs.Select(
    item.TextKey("name").As("name"),
    item.TextKey("kind").As("kind"),
    e.Col("position"),
).
    From(e)
qs.InsertInto("events").FromNamed(rows)
```

```sql
INSERT INTO "events" ("name", "kind", "position")
SELECT ("e"."item" ->> E'name') AS "name", ("e"."item" ->> E'kind') AS "kind", "e"."position"
FROM jsonb_array_elements(($1)::jsonb) WITH ORDINALITY AS "e" ("item", "position")
```

PostgreSQL names the position column of `UnnestAs` `ordinality`. `As` replaces
the alias and the column names together, so pass every name again to rename
one. Use arrays
with a scalar element type: an array of a composite type expands to one column
per field.

Supply arrays of equal length. PostgreSQL
[pads shorter results with NULL](https://www.postgresql.org/docs/18/xfunc-sql.html#XFUNC-SQL-FUNCTIONS-RETURNING-SET),
and qs does not inspect bound application data. Empty arrays insert no rows.

Bound batch size according to data volume; fewer parameters do not reduce the
payload size. For larger loads, consider the driver's COPY support.

The [executable upsert example](../production_query_example_test.go) combines this
shape with partial-index conflict inference and RETURNING. Its table needs a
unique index on `id WHERE deleted_at IS NULL`.

## Update a row

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

To assign a scalar expression, use `Set(column, Write(expr))` or
`Assign(target, Write(expr))`. Row assignments use `AssignRow` with
`WriteTuple(...)` or `WriteRow(...)`, or `WriteRowExpr` for an existing row
expression. These constructors preserve tuple and `ROW(...)` syntax and allow
direct DEFAULT elements. Typed field, JSON, NULL, and default assignment
helpers use the same write descriptors.

An UPDATE with no assignments fails rendering. For sparse updates, assemble
assignments first and choose explicitly whether an all-absent patch is a no-op
or an error. A present NULL remains an assignment; it is not absence.

## Delete a row

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

## Select the rows UPDATE and DELETE change

`Update` and `DeleteFrom` return a pending statement with no `ToSQL`. One of
three methods completes it by saying which rows it changes:

- `Where(first, rest...)` selects the rows that satisfy every condition.
  For a run-time list use `Where(qs.And(conditions...))`; an empty one fails
  rendering.
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

One check remains at render: a predicate that is TRUE for every row fails with
`ErrInvalid`. `qs.And` and `NotIn` over an empty list are TRUE and would
otherwise reach every row. The check reads TRUE and FALSE literals through
`And`, `Or`, `Not` and parentheses, so `qs.And(qs.True(), qs.True())` and
`qs.Not(qs.False())` fail too; use `AllRows` instead.

The guard is structural. It does not inspect bound values or trusted SQL,
verify authorization, or limit the affected rows.

## Update a row on conflict

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

Select a conflict target that matches the database constraint:

| Target | Constructor | Available actions |
|---|---|---|
| Any conflict | `AnyConflict()` | `DoNothing` |
| Column list | `ConflictColumns("id")` | `DoNothing`, `DoUpdate` |
| Inferred index | `ConflictIndex(IndexColumn("id"))` | `DoNothing`, `DoUpdate` |
| Named constraint | `ConflictConstraint("users_pkey")` | `DoNothing`, `DoUpdate` |

For column and index targets, `TargetWhere` filters index inference; named
constraint targets do not support inference predicates. `ConflictUpdate.Where`
filters the update action itself. Both methods return new descriptors, so
retain their results.

`DoUpdate` takes one or more assignments, or an assignment list built at
run time:

```go
columns := []string{"name", "email"}
query, args, err := qs.InsertInto("users").
    Columns("id", "name", "email").
    Values(qs.Value(42), qs.Value("Ana"), qs.Value("ana@example.com")).
    OnConflict(
        qs.ConflictColumns("id").
            DoUpdate(qs.SetAllExcluded(columns...)),
    ).
    ToSQL()
```

```sql
INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3)
ON CONFLICT ("id") DO UPDATE
SET "name" = "excluded"."name", "email" = "excluded"."email"
```

Arguments: `[42 Ana ana@example.com]`.

`DoUpdate` copies the assignment slice; bound application values are not
copied. An empty list produces `ErrInvalid` at render time rather than becoming
`DO NOTHING`.

`SetAllExcluded` takes one identifier per column, as `Columns` and
`ConflictColumns` do. For a column named `a.b`, it renders
`"a.b" = "excluded"."a.b"`.

The generic `OnConflict` methods accept the two completed action types. Like
the other generic methods, they cannot satisfy an interface.
