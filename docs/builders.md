# Reuse and share builders

Builders, expressions, and relations are immutable values. A builder method
returns a new builder and never changes its receiver, so you can branch a base
query freely and render it from several goroutines without synchronization.

A subquery or CTE captures the builder value it was given. Builders derived
from that value later do not change it:

```go
orders := qs.Select("user_id").From("orders")
buyers := qs.Select("id").From("users").Where(qs.Col("id").InQuery(orders))

orders = orders.Where(qs.Col("total").Gt(qs.Param(100)))
query, args, err := buyers.ToSQL()
```

```sql
SELECT "id" FROM "users" WHERE ("id" IN (SELECT "user_id" FROM "orders"))
```

qs does not copy bound values. Keep the application objects you bind unchanged
until execution completes.

## Catch a discarded builder result

Because a method returns a new builder, a call whose result is dropped does
nothing:

```go
q := qs.Select("id").From("users")
q.Where(qs.Col("active").Eq(qs.Param(true))) // no filter is added
```

The compiler accepts this and so does rendering. `qsvet` is a `go vet` tool
that reports it. It lives in its own module, so qs itself keeps no
dependencies:

```sh
go install github.com/jacoelho/qs/qsvet/cmd/qsvet@latest
go vet -vettool="$(command -v qsvet)" ./...
```

```text
repo.go:12:2: result of qs.SelectBuilder.Where is discarded: qs builders are values, so this call has no effect
```

Assign the result to `_` to discard it on purpose. To run the check inside
another analysis tool, such as a `multichecker`, add `discarded.Analyzer` from
`github.com/jacoelho/qs/qsvet/discarded`.

## Know which calls append and which replace

Repeated calls on one builder combine as follows:

| Builder | Method | Effect |
|---|---|---|
| SELECT | `From` | Replaces the FROM list |
| Pending UPDATE | `From` | Appends FROM items |
| Pending DELETE | `Using` | Appends USING items |
| SELECT | `Where` | Appends conditions with AND |
| Pending UPDATE, pending DELETE | `Where`, `WhereCurrentOf`, `AllRows` | Completes the statement; the result has none of the three |
| SELECT and other queries | `Limit`, `LimitExpr`, `LimitAll`, `LimitWithTies`, `LimitWithTiesExpr` | Replaces the row limit |
| Pending UPDATE, assignment INSERT | `Set` | Appends assignments |
| SELECT and other queries, windows, aggregates | `OrderBy` | Appends order terms |
| Row INSERT | `Values`, `Rows` | Appends rows |

For example, `Select("id").From("a").From("b")` selects from `b` only,
while `Update("target").From("a").From("b")` retains both source tables.

## Lay out a statement

The examples write a statement the way formatted SQL reads: the constructor
first, then one clause per line in SQL order, with `ToSQL` on its own line. A
statement that fits on one line stays there, and a long subquery, join, or
expression gets a name before the statement that uses it. gofmt keeps this
layout but does not produce it.
