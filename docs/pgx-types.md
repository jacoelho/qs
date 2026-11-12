# Use PostgreSQL types with pgx

## Cast a value

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

An interval is a cast string. Write a constant into the statement with
`qs.LiteralString("1 second").Cast(qs.TypeInterval)`, which renders
`(E'1 second')::interval`, or bind the text with
`qs.Param("7 days").Cast(qs.TypeInterval)`. To build one from numbers, call
`make_interval` with a named argument:
`qs.Call("make_interval", qs.NamedArg("secs", qs.Param(30)))` renders
`"make_interval"("secs" => $1)`. Dates and timestamps cast the same way.

Dedicated descriptors cover common PostgreSQL types. For other built-in types,
domains, or extension types, use `TypeNamed`, such as
`qs.TypeNamed("pg_catalog", "int4range")`. `TypeArray` constructs an array type
from any descriptor.

## Use pgx values

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

## Register composite types

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

The [database type examples](../integration/type_examples_test.go) verify UUID and
nested composite results against PostgreSQL.
