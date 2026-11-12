# Scan rows with projection declarations

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
        projection.Column(
            r.Col("id").As(prefix+"id"),
            func(u *User) *int64 { return &u.ID },
        ),
        projection.Column(
            r.Col("name").As(prefix+"name"),
            func(u *User) *string { return &u.Name },
        ),
        projection.ColumnNull(
            r.Col("email").As(prefix+"email"),
            func(u *User, v *string) { u.Email = v },
        ),
    )
}

func employees() (qs.SelectBuilder, projection.Projection[Row]) {
    employee := qs.Table("users").As("employee")
    manager := qs.Table("users").As("manager")
    p := projection.New(
        projection.Embed(users(employee, ""), func(r *Row) *User { return &r.Employee }),
        projection.EmbedNull(users(manager, "manager_"), func(r *Row, u *User) { r.Manager = u }),
    )
    query := qs.Select(p.Expressions()...).From(
        qs.LeftJoin(employee, manager).On(employee.Col("manager_id").Eq(manager.Col("id"))),
    )
    return query, p
}
```

For one-off queries with unique column names, ordinary `expr.As("label")` and
your driver's struct mapping are sufficient.

Declarations are immutable and safe to share, so declare one once per alias and
reuse it rather than rebuilding it per query.

## Read rows

Read a result set with `p.Collect(rows)`, or range over `p.All(rows)`. Both
accept `pgx.Rows` and `*sql.Rows`, report `rows.Err()` with the row errors, and
close the result set when they are done with it, including when a row fails or
you leave the `All` loop early. They reuse their working memory from row to row,
so prefer them to calling `Scan` in a loop:

```go
rows, err := conn.Query(ctx, text, args...)
if err != nil {
    return nil, err
}
return p.Collect(rows)
```

`p.CollectPtr(rows)` is `Collect` returning `[]*Row`, for code that holds rows
by pointer. It allocates each row, which `Collect` does not, so prefer `Collect`
unless you need the pointers.

The two drivers spell `Close` differently, so `projection.Rows` does not list
it: `Collect`, `CollectPtr`, and `All` find it on the value and return `projection.ErrNoClose`,
before reading a row, when it has neither `Close()` nor `Close() error`. An
`All` iterator you never range over closes nothing.

For one row, pass your driver's scan function: `p.Scan(row.Scan)` returns a
`Row`. `p.ScanInto(&row, row.Scan)` reads into a value you already hold, such as
the entity a RETURNING clause completes, and leaves the fields the declaration
does not name.

When you reuse one value across rows, each row overwrites the last in place.
Read every row you keep into its own value, and have each setter assign on nil
too, or a NULL keeps the previous row's value. `ColumnOrZero` assigns zero on
NULL, so it needs no such care.

qs still performs no I/O and imports no driver.

## Declare columns and groups

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

## Keep the output list aligned

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

With `Collect` or `All`, wrap the rows instead. Embed your driver's rows, not
`projection.Rows`, so the wrapper keeps their `Close`:

```go
type counted struct {
    pgx.Rows
    total *int64
}

func (c counted) Scan(dest ...any) error { return c.Rows.Scan(append(dest, c.total)...) }

users, err := p.Collect(counted{Rows: rows, total: &total})
```

A value that belongs to each row stays in the declaration: `Embed` the
declaration beside a `Column` in a wrapper row.

Empty projections are valid containers: `qs.Select` with an empty expression
list fails rendering, while empty RETURNING adds nothing. Establish nonempty
output before executing DML that must return rows. `qs.Select()` does not
compile, so a literal SELECT list cannot be empty; `qs.SelectNoColumns` states
an intentional zero-column SELECT.

The [executable projection examples](../projection/example_test.go) demonstrate
self-joins, `Map`, JSONB, RETURNING, `Collect`, `ScanInto`, `ColumnOrZero` with
a join key that no field holds, and a window count read beside each row.
