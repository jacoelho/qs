# Page results

## Order rows

`OrderBy` takes order terms, such as `qs.Col("created_at").Desc()`. To order
by a list of column names, spread `AscCols` or `DescCols`:
`OrderBy(qs.AscCols("last_name", "first_name")...)`. Each name is one
unqualified column. `OrderBy` appends, so a second call adds terms in another
direction.

## Add limits and offsets

A derived builder never changes its source, so one base query serves every
page:

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

## Page with a keyset cursor

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
`SeekAfter` call replaces the cursor, and a call with no values removes it. One
code path therefore serves every page: `SeekAfter(keys...)` reads the first
page when `keys` is empty. A slice passed without `...` fails rendering.

A Go value is bound. An expression is compared as written, which is how a
cursor value takes a cast: `SeekAfter(qs.Param(lastID).Cast(qs.TypeUUID))`.

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
that names earlier keys again. Each bound Go value keeps its one parameter
there, so three keys bind three arguments. A cursor expression is rendered at
each use: its own `Param` binds again, and a volatile expression is evaluated
more than once.

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

## Lock the rows a query reads

`Lock` appends row-lock clauses: `ForUpdate`, `ForNoKeyUpdate`, `ForShare`, and
`ForKeyShare`, each optionally narrowed with `Of` and made non-blocking with
`NoWait` or `SkipLocked`. This claims one queued job without waiting on rows
another worker holds:

```go
query, args, err := qs.Select("id").
    From("jobs").
    Where(qs.Col("state").Eq(qs.Param("queued"))).
    OrderBy(qs.Col("id").Asc()).
    Limit(1).
    Lock(qs.ForUpdate().SkipLocked()).
    ToSQL()
```

```sql
SELECT "id" FROM "jobs" WHERE ("state" = $1) ORDER BY "id" ASC LIMIT $2 FOR UPDATE SKIP LOCKED
```

## Count matching rows

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
