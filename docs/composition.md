# Compose joins, CTEs, and reports

Queries compose across CTEs, joins, and expressions. This example groups paid
orders in a CTE, joins the result to users, and uses CASE to select a spending
tier:

```go
total := qs.Sum(qs.Col("o", "total"))
paidOrders := qs.CTE("paid_orders",
    qs.Select(qs.Col("o", "user_id"), total.As("total_spend")).
        From(qs.Table("orders").As("o")).
        Where(qs.Col("o", "status").Eq(qs.Param("paid"))).
        GroupBy(qs.Col("o", "user_id")).
        Having(total.Gte(qs.Param(100))),
)

tier := qs.Case().
    When(qs.Col("p", "total_spend").Gte(qs.Param(1000)), qs.Param("vip")).
    Else(qs.Param("standard")).
    End().As("tier")

q := qs.Select(qs.Col("u", "id"), qs.Col("u", "email"), qs.Col("p", "total_spend"), tier).
    With(paidOrders).
    From(
        qs.InnerJoin(qs.Table("users").As("u"), paidOrders.Ref().As("p")).
            On(qs.Col("u", "id").Eq(qs.Col("p", "user_id"))),
    ).
    Where(qs.Col("u", "active").Eq(qs.Param(true))).
    OrderBy(qs.Col("p", "total_spend").Desc(), qs.Col("u", "id").Asc()).
    Limit(20).
    Offset(40)

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

Joins have two forms. The functions `qs.InnerJoin`, `qs.LeftJoin`,
`qs.RightJoin`, and `qs.FullJoin` take two relations and need an `On`, `Using`,
or `UsingAs` clause before they form a complete relation, which you pass to
`From`; CROSS and natural joins are already complete. The `SelectBuilder`
methods `Join`, `LeftJoin`, `RightJoin`, `FullJoin`, and `CrossJoin` join a
table, by name or as a relation, onto the last FROM item and take the ON
conditions directly, as in `From("users").LeftJoin("teams", condition)`.

For conditional expressions, `Case` uses conditions for each branch, while
`CaseOf` compares branches with a single operand. Both return a value whose
only method adds the first branch, so a CASE with no WHEN has no `End` to call.

A rowset is a query that can occur in an ordinary subquery: SELECT, VALUES,
TABLE, or a set operation. Standalone `Values` binds values in a rowset, while
`ValuesExpr` builds one from expressions. Write statements do not qualify as
rowsets, even with RETURNING, so use a CTE to compose them with another query.

## Group a parameterized expression

Each occurrence of `Param` receives a new placeholder, even when the same `Expr`
is reused. Repeating `date_trunc($1, created_at)` as
`date_trunc($2, created_at)` in GROUP BY does not identify the same grouping
expression to PostgreSQL. Reference the SELECT position instead:

```go
bucket := qs.DateTrunc(qs.Param("day"), qs.Col("created_at"))
query, args, err := qs.Select(bucket.As("bucket"), qs.CountAll()).
    From("events").
    GroupBy(qs.Ordinal(1)).
    OrderBy(qs.Ordinal(1).Asc()).
    ToSQL()
```

This binds `"day"` once. Keep the ordinal aligned with the projection order.

## Join rows computed per parent

`Lateral` lets a subquery in FROM read columns of the items before it. Join it
with `LeftJoin(qs.Lateral(...), qs.True())` to attach rows computed per parent,
here the three newest items of each order:

```go
orders := qs.Table("orders").As("o")
newest := qs.Select(qs.Col("sku"), qs.Col("created_at")).
    From("items").
    Where(qs.Col("items", "order_id").Eq(orders.Col("id"))).
    OrderBy(qs.DescCols("created_at")...).
    Limit(3)
items := qs.Lateral(qs.Subquery(newest, "i"))

query, args, err := qs.Select(orders.Col("id"), items.Col("sku")).
    From(orders).
    LeftJoin(items, qs.True()).
    ToSQL()
```

```sql
SELECT "o"."id", "i"."sku" FROM ("orders" AS "o" LEFT JOIN LATERAL (SELECT "sku", "created_at" FROM "items" WHERE ("items"."order_id" = "o"."id") ORDER BY "created_at" DESC LIMIT $1) AS "i" ON TRUE)
```

Arguments: `[3]`.

The subquery reads `"o"."id"`, and `ON TRUE` keeps the orders that have no
items.

To page parents independently of their child counts, select the page in a CTE
first, then attach a correlated aggregate to it the same way. Aggregate each
child collection separately to avoid multiplying rows across collections.
Apply an outer ORDER BY as well; CTE ordering does not guarantee final result
order. The [database examples](../integration/production_patterns_test.go)
exercise two collections and a parent with no children. Indexes and query plans
remain application concerns.

## Combine queries with set operations

`Union`, `UnionAll`, `Intersect`, `IntersectAll`, `Except`, and `ExceptAll`
combine rowsets and keep each branch in parentheses. `OrderBy` and `Limit` on
the result apply to the combined rows. Spread a list built at run time:

```go
branches := []qs.Rowset{
    qs.Select("id").From("customers"),
    qs.Select("id").From("suppliers"),
}
query, args, err := qs.Union(branches...).OrderBy(qs.Col("id").Asc()).ToSQL()
```

```sql
(SELECT "id" FROM "customers") UNION (SELECT "id" FROM "suppliers") ORDER BY "id" ASC
```

A list of one renders that rowset alone, in parentheses, with the same
`OrderBy` and `Limit`; an empty list fails rendering.
