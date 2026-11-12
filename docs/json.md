# Build and query JSON

qs models PostgreSQL's SQL/JSON syntax as typed builders: the `JSON_OBJECT` and
`JSON_ARRAY` constructors, the `JSON_OBJECTAGG` and `JSON_ARRAYAGG` aggregates,
`JSON()`, `JSON_SCALAR`, `JSON_SERIALIZE`, `IS JSON`, the query functions
`JSON_EXISTS`, `JSON_VALUE` and `JSON_QUERY`, and `JSON_TABLE`. The constructors,
the aggregates and `IS JSON` need PostgreSQL 16. The rest need PostgreSQL 17.

Each builder has only the clauses PostgreSQL accepts for its function, so most
combinations the server rejects do not compile. `JSONValue` has no wrapper
clause, `JSONExists` has no `Returning`, and `JSONQuery` has no `TrueOnError`.

## Build a JSON object

```go
user := qs.JSONObject(
    qs.JSONKey("id", qs.Col("id")),
    qs.JSONKey("name", qs.Col("name")),
    qs.JSONKey("settings", qs.FormatJSON(qs.Col("settings_text"))),
).AbsentOnNull().Returning(qs.TypeJSONB)

query, args, err := qs.Select(user.As("user")).
    From("users").
    Where(qs.Col("team_id").Eq(qs.Param(7))).
    ToSQL()
```

```sql
SELECT JSON_OBJECT(E'id' : "id", E'name' : "name", E'settings' : "settings_text" FORMAT JSON ABSENT ON NULL RETURNING jsonb) AS "user" FROM "users" WHERE ("team_id" = $1)
```

Arguments: `[7]`.

`JSONKey` takes a key that is a string constant and renders it as a literal;
`JSONPair` takes a key that is an expression. `FormatJSON` marks text that
already holds JSON, so PostgreSQL embeds it instead of quoting it as a string.
`AbsentOnNull` omits a member whose value is NULL. Without a NULL clause, objects keep NULL values and arrays drop them.

`JSONArray` builds an array from values, and `JSONArrayQuery` builds one from a
query that returns one column. `JSONObjectAggregate` and `JSONArrayAggregate` are
the SQL-standard aggregates. Both accept `Filter`, `Over` and `OverNamed`, and
`JSONArrayAggregate` also accepts `OrderBy`. `JSONObjectAgg` and `JSONAgg` call
PostgreSQL's `json_object_agg` and `json_agg` functions instead.

## Navigate a document with operators

`JSONBExpr` and `JSONExpr` wrap an expression that is already `jsonb` or `json`
and give it PostgreSQL's operators as methods: `Key`, `Index`, `Path`, and their
text forms `TextKey`, `TextIndex`, and `PathText`, which return a
`Field[string]`. A `jsonb` document also has `Contains`, `HasKey`, `Concat`,
`DeleteKey`, `DeletePath`, `PathExists`, and their relatives.

```go
doc := qs.JSONBExpr(qs.Col("e", "payload"))
kind := doc.TextKey("kind")

query, args, err := qs.Select(kind.Expr(), qs.CountAll()).
    From(qs.Table("events").As("e")).
    Where(
        doc.Path("transforms", "0", "inputs").IsNotNull(),
        doc.Key("customer").TextKey("tier").Eq("gold"),
    ).
    GroupBy(kind.Expr()).
    ToSQL()
```

```sql
SELECT ("e"."payload" ->> E'kind'), count(*) FROM "events" AS "e"
WHERE (("e"."payload" #> (ARRAY[E'transforms', E'0', E'inputs'])::text[]) IS NOT NULL)
  AND ((("e"."payload" -> E'customer') ->> E'tier') = $1)
GROUP BY ("e"."payload" ->> E'kind')
```

Arguments: `[gold]`.

Keys, path steps, indexes, and JSONPath text render as SQL literals and bind
nothing. The same selector is therefore the same expression at every use, which
GROUP BY, DISTINCT with ORDER BY, and an expression index all require. Compared
values still bind. A selector chosen at run time changes the statement text, so
bind one instead with the operator functions:
`qs.JSONGetText(qs.Col("payload"), qs.Param(key))`. Cast a bound array index,
as in `qs.Param(int32(i)).Cast(qs.TypeInt4)`, because PostgreSQL reads an
untyped parameter there as a text key.

## Read values from a document

```go
doc := qs.Col("doc")
path := qs.LiteralString

query, args, err := qs.Select(
    qs.JSONValue(doc, path("$.customer.name")).As("customer"),
    qs.JSONValue(doc, path("$.total")).
        Returning(qs.TypeNumeric).
        DefaultOnEmpty(qs.LiteralInt(0)).
        As("total"),
    qs.JSONQuery(doc, path("$.items[*].sku")).WithWrapper().As("skus"),
).
    From("orders").
    Where(
        qs.JSONExists(doc, path("$.items[*] ? (@.qty >= $min)")).
            Passing("min", qs.Param(10).Cast(qs.TypeInt4)).
            Condition(),
    ).
    ToSQL()
```

```sql
SELECT JSON_VALUE("doc", E'$.customer.name') AS "customer", JSON_VALUE("doc", E'$.total' RETURNING numeric DEFAULT 0 ON EMPTY) AS "total", JSON_QUERY("doc", E'$.items[*].sku' WITH WRAPPER) AS "skus" FROM "orders" WHERE JSON_EXISTS("doc", E'$.items[*] ? (@.qty >= $min)' PASSING ($1)::integer AS "min")
```

Arguments: `[10]`.

`JSONValue` returns one SQL scalar, `JSONQuery` returns JSON, and `JSONExists`
tests whether the path matches anything. `Passing` supplies the value of a
`$name` variable in the path. Cast a parameter you pass this way, because
PostgreSQL otherwise reads it as text.

`DefaultOnEmpty` and `DefaultOnError` take a constant, a function call, or an
operator expression. PostgreSQL rejects a column reference there, and a value
that is a column fails rendering. Under the extended query protocol PostgreSQL
also rejects a value that is only a bound parameter; qs renders it and leaves
that error to the server.

`OmitQuotes` with `WithWrapper` or `WithConditionalWrapper` fails rendering,
because PostgreSQL rejects the pair.

A `JSONBDocument` or `JSONDocument` stays a document through `Key`, `Path`,
and `Index`. `Cast` and `TypeOf` leave it: `doc.Key("quantity").Cast(qs.TypeInt4)`
renders `(("payload" -> E'quantity'))::integer`, and `doc.TypeOf()` is a
`Field[string]` over `jsonb_typeof`, so `doc.TypeOf().Eq("object")` filters on
the kind of value. For any other position, such as a CASE result, pass
`doc.Expr()`; `qs.JSONBExpr(expr)` wraps an expression as a document again.

`IsJSON` tests whether text is well-formed JSON. Narrow the test with `Object`,
`Array`, or `Scalar`, and negate it with `Condition().Not()`.

## Turn a document into rows

```go
items := qs.JSONTable(qs.Col("o", "doc"), "$.items[*]",
    qs.JSONOrdinality("position"),
    qs.JSONColumn("sku", qs.TypeText),
    qs.JSONColumn("qty", qs.TypeInt4).Path("$.quantity").DefaultOnEmpty(qs.LiteralInt(1)),
    qs.JSONExistsColumn("gift", qs.TypeBool),
    qs.JSONNested("$.parts[*]", qs.JSONColumn("part", qs.TypeText).Path("$.name")),
)

query, args, err := qs.Select(qs.Col("o", "id"), qs.Star("item")).
    From(qs.Table("orders").As("o")).
    CrossJoin(qs.Lateral(items.As("item"))).
    ToSQL()
```

```sql
SELECT "o"."id", "item".* FROM ("orders" AS "o" CROSS JOIN LATERAL JSON_TABLE("o"."doc", E'$.items[*]' COLUMNS ("position" FOR ORDINALITY, "sku" text, "qty" integer PATH E'$.quantity' DEFAULT 1 ON EMPTY, "gift" boolean EXISTS, NESTED PATH E'$.parts[*]' COLUMNS ("part" text PATH E'$.name'))) AS "item")
```

Arguments: `[]`.

`JSONTable` is a row source, and `As` or `Ref` makes it a relation. PostgreSQL
lets it read a column of an earlier FROM item with or without `Lateral`, which
only writes the keyword. The row path and every column path are Go strings,
because PostgreSQL accepts only a string constant there. A column without `Path`
reads the member that has the column's name.

Column names and path names must be distinct within one `JSONTable`. A repeated
name, an empty path, or more column aliases in `As` than the table has columns
fails rendering. PostgreSQL names a row path without `PathName`
`json_table_path_0`, so that name fails rendering too until you name the row
path.
