# Render SQL and handle errors

## Set render limits

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

## Know what rendering checks

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

## Handle render errors

`ToSQL` returns the error. `MustToSQL` panics instead; keep it for tests and
for statements built from constants, where a render error is a programming
mistake.

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

## Reuse buffers

For repeated rendering, `AppendSQL` appends SQL and arguments to caller-owned
buffers, starting parameter numbering after any existing arguments:

```go
q := qs.Select("id").From("users").Where(qs.Col("active").Eq(qs.Param(true)))
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
