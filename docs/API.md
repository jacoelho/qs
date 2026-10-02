# API guide

The complete exported API, generated with `go doc -all .`, is in
[API.txt](API.txt). The examples and tests are executable usage references.

## Construction choices

| Need | Entry point |
|---|---|
| Simple named projections | `SelectCols("id", "name")` |
| Rows with no output columns | `SelectNoColumns()` |
| Expressions, casts or aliases | `Select(Col("id"), Col("value").Cast(Text).As("value"))` |
| Existing trusted projection list | `Select(UnsafeSQL(projectionSQL))`; shorthand `SelectSQL(projectionSQL)` |
| Simple physical table | `.From("schema.table")` |
| Aliased/derived/joined/lateral table | `.FromExpr(relation)` |
| Ordered assignments | `Set("name", value)`, `SetExpr("total", expression)` |
| Composite/array INSERT destinations | `.Targets(Col("address").Field("city"), Col("tags").Index(Param(1)))` |
| Statically related operands | `Typed[T]("column")` / `TypedExpr[T](expression)` |
| Typed JSON extraction | `JSONCol("doc").Key("items").Index(0).TextKey("name")` |
| Typed JSONB operators | `JSONBCol("doc").Contains(JSONBParam(encoded))` |
| Complete output | `.ToSQL()` or `.ToSQLWith(Options{...})` |
| Caller-owned storage | `.AppendSQL(buf, args)` or `.AppendWith(buf, args, Options{...})` |

Values and expressions are intentionally separate. `Values(...)` binds values;
`ValuesExpr(...)` accepts expressions. `Eq(value)` binds a value; `EqExpr(expr)`
compares expressions. Passing an expression to a value-binding API is a rendering
error, not magical interpolation. Typed fields use `EqField`/`SetField` when both
operands have the same Go payload type.

## Joins

Ordinary joins accept structured ON conditions:

```go
qs.SelectCols("users.id", "orders.id").
    From("users").
    Join("orders", qs.EqColumns("users.id", "orders.user_id"))
```

For aliases, build the relation explicitly:

```go
users := qs.Table("users").As("u")
orders := qs.Table("orders").As("o")
qs.SelectCols("u.id", "o.id").FromExpr(
    qs.InnerJoin(users, orders).On(qs.EqColumns("u.id", "o.user_id")),
)
```

Relations also accept `.Using("id")` when both tables share that join column.
`qs.LeftJoin`, `qs.RightJoin` and `qs.FullJoin` use the same ON/USING methods.
`CrossJoin` needs no condition; natural joins derive their condition from shared
column names. An ordinary join missing ON or USING is a rendering error.

## Select versus SelectSQL

`Select(...Expr)` is the main constructor. `SelectCols` and `SelectSQL` both
return the same `*SelectBuilder`; they are convenience wrappers, not different
statement types or execution paths.

```go
qs.Select(qs.Col("id"), qs.Col("name"))
qs.SelectCols("id", "name") // Same structured projection.

qs.Select(qs.UnsafeSQL("id, name"))
qs.SelectSQL("id, name") // Same explicitly trusted SQL projection.
```

A plain string cannot say whether it is an identifier, SQL syntax or a value.
The expression constructors make that decision explicit:

```go
qs.Col("id").Cast(qs.Text) // SQL expression: ("id")::text
qs.Col("id::text")         // One identifier: "id::text"
qs.UnsafeSQL("id::text")   // Trusted syntax, rendered verbatim.
qs.Param("id::text")       // A bound value: $n, not SQL syntax.
```

For a reusable projection, prefer `[]qs.Expr` and `Select(projections...).Columns(...)`.
Retain `UnsafeSQL(projectionSQL)` only when migrating an existing trusted string.
Neither SelectSQL nor UnsafeSQL parses that string or knows how many columns
it contains. Both intentionally bypass structural guarantees for the raw part.
This does not affect automatic numbering of parameters in structural parts.

## Main signatures

```go
func Select(...Expr) *SelectBuilder
func SelectNoColumns() *SelectBuilder
func SelectCols(...string) *SelectBuilder
func SelectSQL(...string) *SelectBuilder
func InsertInto(string) *InsertBuilder
func Update(string) *UpdateBuilder
func DeleteFrom(string) *DeleteBuilder
func MergeInto(string) *MergeBuilder
func Values(...any) *ValuesBuilder
func ValuesExpr(...Expr) *ValuesBuilder
func TableRows(string) *TableBuilder
func Execute(string, ...Expr) *ExecuteBuilder
func CreateTableAs(string, Rowset) *CreateTableAsBuilder
func CreateTableAsExecute(string, *ExecuteBuilder) *CreateTableAsBuilder
func MaterializedViewAs(string, Rowset) *MaterializedViewBuilder
func DeclareCursor(string, Rowset) *DeclareCursorBuilder

func Union(Rowset, Rowset, ...Rowset) *SetBuilder
func UnionAll(Rowset, Rowset, ...Rowset) *SetBuilder
func Intersect(Rowset, Rowset, ...Rowset) *SetBuilder
func IntersectAll(Rowset, Rowset, ...Rowset) *SetBuilder
func Except(Rowset, Rowset, ...Rowset) *SetBuilder
func ExceptAll(Rowset, Rowset, ...Rowset) *SetBuilder

func CTE(string, Statement) WithQuery
func CTERef(string) Relation
func Scalar(Rowset) Expr
func Derived(Rowset) Relation
func Exists(Rowset) Condition
func NotExists(Rowset) Condition

func ToSQL(Statement) (string, []any, error)
func ToSQLWith(Statement, Options) (string, []any, error)
func AppendSQL([]byte, []any, Statement) ([]byte, []any, error)
func AppendWith([]byte, []any, Statement, Options) ([]byte, []any, error)
func MustToSQL(Statement) (string, []any)
func ExprToSQL(Expr) (string, []any, error)
func Clone[T Statement](T) T
```

## Rendering options

Every statement has `ToSQL()`, `ToSQLWith(Options)`, `AppendSQL(buf, args)` and
`AppendWith(buf, args, Options)` methods. The free functions accept the same
sealed `Statement` interface. Rendering traverses the stored query graph lazily;
it does not execute SQL or cache output.

```go
q := qs.SelectCols("id").From("users").Where(qs.Eq("name", "O'Reilly; ? $1"))
query, args, err := q.ToSQLWith(qs.Options{PlaceholderStyle: qs.Question})
// query: SELECT "id" FROM "users" WHERE ("name" = ?)
// args: []any{"O'Reilly; ? $1"}
```

`PlaceholderStyle` is a typed enum: `Dollar` emits `$1`, `$2`, etc.; `Question`
emits `?` for each parameter. Zero options select `Dollar`, PostgreSQL 18,
65,535 parameters and 256 nesting levels. Invalid styles return `ErrInvalid`
wrapped in `RenderError` before rendering. `Options.PostgreSQL` uses the typed
`PostgreSQLVersion` enum with `PostgreSQL12` through `PostgreSQL18`; zero selects
PostgreSQL18 and unknown versions return `ErrInvalid`. Convert a dynamic integer
explicitly with `qs.PostgreSQLVersion(value)`. Other options select parameter and
nesting limits.

Options belong to each rendering call. The same query and its clone can render
with either style without changing defaults or argument order. Nested statements
share the outer render's options. `q.AppendWith(buf, args, options)` uses the
same policy with caller-owned storage; dollar numbering starts at `len(args)+1`
and both styles count prefix arguments toward the limit. The free form is
`qs.AppendWith(buf, args, q, options)`.

```go
buf := make([]byte, 0, 1024)
args := make([]any, 0, 16)
buf, args, err := q.AppendWith(buf, args, qs.Options{PlaceholderStyle: qs.Question})
```

Values containing quotes, semicolons, `?` or `$1` remain argument data.
Identifiers and explicit string literals use their respective quoting rules.
JSONB question-mark operators, comments and trusted raw SQL are never rewritten.
`Question` does not convert SQL to another dialect or adapt a driver's query
parser. Use `Dollar` for native PostgreSQL/pgx execution; a question-mark consumer
must distinguish PostgreSQL operators from parameters. Raw fragments do not
bind their own `?` or `$1`: compose them with `Param` nodes instead.

Owned rendering returns empty SQL and nil arguments on failure. Appending
returns the original slices and visible prefixes; unused capacity is not
preserved. Appended argument slots are cleared on failure.

## Rendering API migration

These are breaking renames; the previous exports have been removed.

| Previous | Current |
|---|---|
| `Build` / `.Build()` | `ToSQL` / `.ToSQL()` |
| `BuildWith` | `ToSQLWith` / `.ToSQLWith()` |
| `MustBuild` | `MustToSQL` |
| `BuildExpr` | `ExprToSQL` |
| `BuildError` | `RenderError` |

`AppendSQL` and `AppendWith` retain their names. Builders gain `ToSQLWith` and
`AppendWith` methods alongside the renamed `ToSQL` method.

## Deliberate naming distinctions

`Col("a.b")` splits a qualified path; `Ident("a.b")` quotes a literal identifier.
`Table("a.b")` and `TableIdent("a.b")` make the same distinction for tables.
`As` always quotes the alias as one identifier.

Assignment strings are assignment targets, not automatically table-qualified
columns: `Set("address.city", value)` denotes a composite-field assignment.
For an aliased typed field, `Typed[T]("u.name").Set(value)` strips the qualifier
because ordinary PostgreSQL SET targets are unqualified. Use `Assign` for an
explicit composite/subscript target. `Excluded("name")`, `Old("name")` and
`New("name")` accept the individual column name, not an already-qualified path.

`ConflictClause.TargetWhere` applies to conflict-index inference;
`ConflictClause.Where` applies to the DO UPDATE action. `Where`, `Having`, and an
aggregate's `Filter` occupy different SQL clauses and are never interchanged.

`WithinGroup` describes ordered-set aggregation; an aggregate's `OrderBy`
describes ordering inside its argument list. `Over` and `OverNamed` are window
operations. Unsupported modifier combinations are structurally rejected where
known; the database remains responsible for actual function/operator typing.

`WithRecursive` belongs to statements whose PostgreSQL grammar supports it.
MERGE intentionally has only `With`, not `WithRecursive`.

## Result and NULL typing

`Expr` has no Go result-type parameter, so aggregate result types are not
incorrectly assumed to equal their input type. `Field[T]` is an optional
application assertion about the non-NULL payload. The builder does not scan
results, infer outer-join nullability or generate records from projection lists.

`Null[T]` and `Optional[T]` are value containers. They do not implement
`driver.Valuer` or `sql.Scanner`. Use explicit nullable helpers for binding.

## Composite fields, rows and types

`expr.Field("city")` selects one composite field; the field name is a literal
identifier, including dots and quotes. `expr.Fields()` expands composite `.*`.
`Index`, `Slice`, `SliceFrom`, `SliceTo` and `SliceAll` compose with fields.
Expression reads use the required grouping; assignment destinations render
their distinct PostgreSQL syntax.

Chained subscripts form one multidimensional operation. Use
`a.Index(i).Parenthesized().Index(j)` to index the result of `a[i]`, preserving
`(a[i])[j]`. This matters for domains containing arrays and geometric types.
Parentheses preserve composite expansion's unknown width. Apply aggregate
modifiers before grouping and aliases afterward.

```go
city := qs.TypedExpr[string](qs.Ident("u", "address").Field("city"))
qs.Update("users").Set(city.Set("Porto"))
// UPDATE "users" SET "address"."city" = $1

qs.InsertInto("users").Targets(qs.Col("address").Field("city")).Values("Porto")
```

`Row` emits explicit `ROW(...)`; `Tuple` emits a parenthesized row and requires
at least two expressions. `AssignRow` and `AssignRowFrom` accept structured
field/subscript destinations. Composite expansion has unknown width; known
width mismatches still fail. `SelectNoColumns()` deliberately has width zero,
rejects DISTINCT and remains zero-column after removing its projections.
`Select()` requires projections. `Derived(query)` permits an unaliased FROM
subquery on PostgreSQL 16+; `.As(...)` names it for qualification.

For a single INSERT row, typed assignments prevent separate column/value counts:

```go
id, name := qs.Typed[int]("id"), qs.Typed[string]("name")
qs.InsertInto("users").Set(id.Set(42), name.Set("Ana"))
```

Each `Field[T].Set(T)` accepts exactly one correctly typed value at compile time;
INSERT derives its column and value lists together. The variadic
`Columns(...string).Values(...any)` form cannot encode lengths in Go types and
checks known width mismatches when rendering. Typed fields declare the
application's payload types; they do not validate a live database schema.

Use `Varchar` and `Decimal` for their validated built-in forms.
`NamedType("schema", "type").Modifiers(...int)` preserves qualified type identity
and copies integer modifiers. Qualified `pg_catalog` numeric/length/time types
retain known bounds; PostgreSQL resolves custom type modifiers. `ArrayType`
keeps modifiers on the element type.

## SQL grammar expressions

`SubstringFrom`, `SubstringFor` and `SubstringSimilar` represent SQL's keyword
forms; `Substring` remains an ordinary function call. Other native forms include
`Position`, `Overlay`, `TrimSyntax`, `Normalize`, `IsNormalized`, `Overlaps`,
`AtLocal` and `CollationFor`. Directions and normalization forms are typed enums.
`ExtractNamed` accepts a constrained set of PostgreSQL field spellings.
SQL value functions accept optional precision where PostgreSQL permits it.

`Window().Rows(start)`, `.Range(start)` and `.Groups(start)` preserve shorthand
frames with implicit `CURRENT ROW` ends. The corresponding `*Between` methods
retain explicit start/end syntax. `EscapeExpr` supports expression-valued
pattern escapes; `Escape(string)` retains its character validation.

## JSON documents

`JSONCol` and `JSONBCol` assert column types; `JSONExpr` and `JSONBExpr` assert
existing expression types without casts. `JSONParam[T JSONInput]` and
`JSONBParam[T JSONInput]` accept encoded string/byte-slice types and cast bound
values. They do not encode arbitrary Go structs or validate document contents.

Both document types expose `Key(string)`, `Index(int)`, `TextKey(string)`,
`TextIndex(int)`, `Path(...string)` and `PathText(...string)`. Text results are
`Field[string]`; document results retain json/jsonb. `Key("0")` and `Index(0)`
have distinct semantics. Integer selectors cast to int4 and reject values
outside its range; paths use typed text arrays, including empty paths.

Only JSONB exposes containment, key-existence, deletion, concatenation and
JSONPath operators. Their arguments distinguish documents, text keys and
integer indices. `Set` accepts the same document type and uses normal assignment
target validation. `IsNull` and `IsNotNull` test SQL NULL, which is distinct
from JSON null. Missing keys and invalid array positions yield SQL NULL.
`PathExists` tests returned items; `PathMatches` tests a predicate and can be
unknown. See the [typed JSON examples](../README.md#typed-json-and-jsonb).

For a numeric text extraction, explicitly write
`TypedExpr[int32](doc.TextKey("count").Expr().Cast(Int4))`.
The builder does not infer SQL types from Go result types.

## SQL/JSON construction

Native grammar constructors include `JSONObject`, `JSONArray`, `JSONArrayQuery`,
`JSONObjectAggregate`, `JSONArrayAggregate`, `JSONParse`, `JSONScalar`,
`JSONSerialize` and `IsJSON`. Object entries use `JSONPair`; ordinary expressions
are accepted directly as values. `JSONInputExpr(expr).FormatJSON()` marks an
already formatted input, separately from output `Returning`/format options.

```go
qs.JSONObject(qs.JSONPair(
    qs.Param("name").Cast(qs.Text),
    qs.Param("Ada").Cast(qs.Text),
)).AbsentOnNull().Returning(qs.JSONB).Expr()
```

Object and array builders expose their applicable null/uniqueness policies.
Both aggregate builders expose FILTER and window clauses; only array aggregation
exposes in-call ordering. These grammar constructors require PostgreSQL 16+.
`JSONObjectAgg` remains the ordinary `json_object_agg` function; the spelling
`JSONObjectAggregate` denotes SQL `JSON_OBJECTAGG`.

## SQL/XML construction

`XMLElement`, `XMLForest`, `XMLConcat`, `XMLParse`, `XMLPI`, `XMLRoot`,
`XMLSerialize` and `XMLExists` represent native SQL/XML syntax. Element and
attribute names are quoted identifiers. `XMLIsDocument` tests the document role.
Modes and passing policies are typed enums; text content stays bound data.

```go
items := qs.XMLTable(
    qs.LiteralString("/root/item"),
    qs.Param(document).Cast(qs.XML),
    qs.XMLColumn("name", qs.Text).Path(qs.LiteralString("name")),
    qs.XMLOrdinality("position"),
).As("items")
q := qs.Select(items.Col("name")).FromExpr(items)
```

Namespaces, paths and defaults accept expressions. XMLTABLE descriptors own
their column lists and validate names, duplicate columns and ordinality options.
PostgreSQL evaluates XML, XPath and conversions. In polymorphic JSON/XML calls,
cast bound inputs explicitly when PostgreSQL has no context to infer their SQL
types; `Field[T]` does not add a cast automatically.

## Extension points

Query destination and cursor commands are Statements. `selectQuery.Into(name)`
returns a separate `SelectIntoBuilder`; the source SELECT stays usable as a
rowset. Destination options belong to the wrapper. CTAS accepts either a Rowset
or an explicit `CreateTableAsExecute` source. These wrappers retain live source
references; `Clone` freezes them.

`DeclareCursor` exposes a typed scroll mode and WITH/WITHOUT HOLD. It constructs
the command; the caller owns transactions, fetching and cursor cleanup.
Materialized-view definitions reject external bind parameters and modifying
CTEs. EXECUTE also rejects external binds; its arguments must be parameter-free
expressions. Prepared-statement and cursor names are single quoted identifiers.

CTAS and SELECT INTO permit top-level modifying CTEs; cursor and materialized-view
sources have their stricter PostgreSQL contexts. Utility commands cannot be used
as ordinary Rowsets or CTE bodies. EXPLAIN can wrap these command forms.

For planner diagnostics, `Explain(query).Analyze(true).Serialize()`
measures result conversion in PostgreSQL 17+. `SerializeBinary` measures binary
conversion and `SerializeNone` disables it. Bare serialization defaults to text;
an explicit enum argument preserves NONE/TEXT/BINARY. Text/binary requires
ANALYZE; invalid modes and lower configured server versions are rendering errors.

`LiteralNumeric` validates and preserves exact decimal/scientific tokens without
float64 conversion, plus PostgreSQL 16+ binary/octal/hex integers and numeric
underscore separators. `LiteralBit` and `LiteralHex` validate bit-string literals.
These are useful for SQL grammar and reproducible static queries; normal
application values should use bound parameters. `PrefixOperator` validates
symbolic unary operators with the same policy as `Operator`.

`Call` quotes an ordinary function identifier and binds/embeds explicit Expr
arguments. `NamedArg` and `Variadic` represent PostgreSQL function-call syntax.
`Operator` permits validated symbolic operators; `QualifiedOperator` handles an
extension operator's schema. SQL grammar not represented by a dedicated node
can use `Fragment`/`UnsafeSQL`, `RelationSQL` or `StatementSQL` with explicit
identifier/value parts. These APIs are not parsers or sanitizers for user SQL.
