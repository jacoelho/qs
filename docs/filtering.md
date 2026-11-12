# Filter rows

## Add optional filters

Each `Where` call returns a builder with its conditions appended with AND, and
`Or` groups alternatives within a condition. You can use ordinary Go control
flow to add filters only when the corresponding input is present:

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

A boolean column or parameter is an expression, not a condition: convert it
with `Condition`, as in `Where(qs.Col("active").Condition())`. In the other
direction a condition has `As`, `Asc`, and `Desc`, so
`qs.Col("deleted_at").IsNull().As("deleted")` goes in a select list and
`qs.Col("pinned").Condition().Desc()` in ORDER BY. `Condition.Expr` returns the
expression for any other position.

The example omits filters for empty prefixes and role lists. An empty `In` list
produces FALSE, so omit the condition when an empty list means "no filter" in
your application.

`Where` is variadic, so it takes conditions you collected in a slice as
`Where(conditions...)`; no conditions add no clause.

`WhereIf(include, condition)` appends a condition to a SELECT only when
`include` is true, but Go still constructs the condition before the call. Use
an `if` statement when you also want to avoid that construction work.

UPDATE and DELETE take their whole predicate in one `Where` call and have no
`WhereIf`; see
[Select the rows UPDATE and DELETE change](writes.md#select-the-rows-update-and-delete-change).

## Match literal text

`Like` and `ILike` bind SQL patterns in which `%` and `_` retain their wildcard
meaning. When the input should match literal text, use one of these `Expr`
methods:

| Match | Case-sensitive method | Case-insensitive method |
|---|---|---|
| Prefix | `LikePrefix` | `ILikePrefix` |
| Suffix | `LikeSuffix` | `ILikeSuffix` |
| Substring | `LikeContains` | `ILikeContains` |

These methods escape `%`, `_`, and `!`, then add the wildcards needed for the
selected match. They render a fixed `ESCAPE E'!'` clause, so pass the original
text without escaping it first.

| Condition | Bound pattern |
|---|---|
| `qs.Col("name").ILikePrefix("a_%")` | `a!_!%%` |
| `qs.Col("email").LikeSuffix("@example.com")` | `%@example.com` |
| `qs.Col("display_name").ILikeContains("50%_")` | `%50!%!_%` |

An empty prefix, suffix, or substring matches every non-NULL text value. Omit
the condition if your application treats empty input as "no filter".

## Compare typed fields and array parameters

This example uses the `strings` package and qs:

```go
users := qs.Table("users").As("u")
id := qs.Typed[int64](users.Col("id"))
ids := []int64{10, 20}
requiredTags := []string{"staff", "active"}
rawUsername := "Al"

q := qs.Select(
    id.As("id"),
    users.Col("username"),
    qs.CountAll().Over(qs.Window()).As("total"),
).
    From(users)
if len(ids) > 0 {
    q = q.Where(id.In(ids...))
}
if len(requiredTags) > 0 {
    q = q.Where(qs.ArrayContains(users.Col("tags"), qs.ArrayParam(requiredTags, qs.TypeText)))
}
if rawUsername != "" {
    q = q.Where(qs.Lower(users.Col("username")).LikePrefix(strings.ToLower(rawUsername)))
}
query, args, err := q.OrderBy(id.Asc()).Limit(20).ToSQL()
```

```sql
SELECT "u"."id" AS "id", "u"."username", count(*) OVER () AS "total"
FROM "users" AS "u"
WHERE ("u"."id" IN ($1, $2))
  AND ("u"."tags" @> ($3)::text[])
  AND (lower("u"."username") LIKE $4 ESCAPE E'!')
ORDER BY "u"."id" ASC LIMIT $5
```

Arguments: `[10 20 [staff active] al% 20]`.

`Typed[int64]` lets Go enforce the type of values passed to `id.In`, which
produces FALSE for an empty list. `ArrayParam` binds a single array argument
and casts it to `text[]`, leaving array encoding to the driver. A nil slice
binds an empty array rather than NULL, so `= ANY` is FALSE and `<> ALL` is TRUE.

`EqAny` compares with a single array argument instead of one parameter per
value, so the statement text is the same for any number of ids. A `Field[T]`
takes the slice and the element type; an `Expr` takes an array expression:

```go
qs.Select(id.Expr()).From(users).Where(id.EqAny(ids, qs.TypeInt8))
qs.Select("id").From("users").Where(qs.EqAny(qs.Col("id"), qs.ArrayParam(ids, qs.TypeInt8)))
```

```sql
SELECT "u"."id" FROM "users" AS "u" WHERE ("u"."id" = ANY(($1)::bigint[]))
SELECT "id" FROM "users" WHERE ("id" = ANY(($1)::bigint[]))
```

To join against the values instead, turn arrays into rows with `UnnestAs`,
which names each column beside the array that fills it:

```go
wanted := qs.UnnestAs("wanted", qs.NamedArray("id", qs.ArrayParam(ids, qs.TypeInt8)))
q := qs.Select(users.Col("id"), users.Col("username")).
    From(qs.InnerJoin(users, wanted).On(users.Col("id").Eq(wanted.Col("id"))))
```

```sql
SELECT "u"."id", "u"."username"
FROM ("users" AS "u" JOIN unnest(($1)::bigint[]) AS "wanted" ("id") ON ("u"."id" = "wanted"."id"))
```

Choose the text comparison form according to your application's matching
requirements. `LOWER(column) LIKE` and PostgreSQL `ILIKE` can give different
results for some locales and Unicode input, and Go's `strings.ToLower` can
differ from PostgreSQL `LOWER`.

`count(*) OVER ()` adds the count to each returned row, so an empty page has no
row from which to read it, and after a `SeekAfter` cursor it counts only the
remaining rows. [Count matching rows](pagination.md#count-matching-rows) with `CountRows`
instead.
