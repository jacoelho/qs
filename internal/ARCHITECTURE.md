# Architecture

This is the architecture entry point. [README.md](../README.md) owns public usage
and caller contracts; the [corpus README](postgrescorpus/README.md) owns
construction-support measurement and fixture maintenance.

The [projection package documentation](../projection/doc.go) owns the result
declaration contract.

## Boundaries and representation

The core constructs PostgreSQL SQL and arguments. It has no execution, scanning,
transaction, schema-discovery or dialect layer. Its module has no third-party
dependencies. The separate `integration/` module owns pgx and replaces its qs
dependency with the local checkout. Tagged database tests run and lint through
that module; root module tests need no database driver. Callers own connections,
transactions, argument encoding and database authorization.

The `projection` companion imports qs; the core never imports it. A projection
owns the ordered pairing of expressions and destinations, and `Scan` drives the
caller's scan function: it performs no I/O and imports no driver or
`database/sql`. It uses `reflect` only when a projection is declared. Callers still own execution, connections and SQL scope. Reading is
positional, so it proves neither result width nor type agreement; the driver reports
those when a row is scanned.

`ScanInto` is the one row read; `Scan` is `ScanInto` on a new value, zeroed on error.
The caller owns the value passed to `ScanInto`, including what reusing it across rows
shares. `All` and `Collect` own the row loop and its completion check, `rows.Err`, and
read through `Rows`, the three methods `pgx.Rows` and `*sql.Rows` have in common. They
never close the rows: the caller opened the result set, and its deferred `Close` also
covers an abandoned loop.

Between rows of one result set they keep a scratch: the destination list, the cell
lists and the cells of the previous row, which each part overwrites in creation order,
the order the declaration fixes. Parts still run for every row, because a dest may
allocate inside the row it is given. Nothing a caller can keep is reused: a present
group's row is a new allocation, taken again only when the group was absent and no set
received the row, though its dests may have allocated inside it. The cell is marked
when the group is found present, before its cells finish, so this does not depend on
the loop stopping at the first error. A `Map` input is a new allocation for every row,
because a set inside it may keep a pointer to it. The `*T` a `ColumnNull` set receives
is the driver's allocation: only the cell holding that pointer is pooled, so pooling
the value would break what a set may keep. `Collect` reads each row into its slot
of the result slice and `All` into a new value. A scan function must therefore not
keep its destination list past its return, which neither driver's `Scan` does. A
single `Scan` or `ScanInto` has no scratch and allocates what it needs.

Flat columns read straight into their destination. Nullable columns, `EmbedNull`
groups and `Map` add per-row cells that run after the row is read, and only those
pay for them. This gives `Column` two read modes: flat, the driver owns NULL; in a
group, the group owns it, because presence is unknown until the whole row is read.
Cells that assign a column finish before the others: a group's `Column` cells, and
every `ColumnOrZero` cell, flat or grouped. This reproduces the order of a flat
`Column`, which the driver has written before any cell runs. `set` and `convert`
therefore see the same row in both modes: every column of their row and of the
enclosing rows assigned, and earlier callbacks applied in declaration order.

`ColumnOrZero` is the `ColumnNull` read with the assignment owned by the package: it
copies the driver's value, or the zero value on NULL, to the address `dest` returned
before the row was read. Assigning zero rather than skipping keeps a destination reused
through `ScanInto` free of the previous row. Its target passes the checks of a group
`Column`, for every `T`, since no Scanner path exists for it: once on a zero `D`, and
again behind the dests of enclosing `Embed`s, which run inner's parts in a declaring
pass of their own. A value the caller prepares for `ScanInto` is
beyond any declaration, so the rule itself, that `T` decodes from zero, stays the
caller's.

Positional reading covers a prefix: `scan` receives one destination per expression
and may append its own for output selected after `Expressions`. Such output has no
owner in the declaration, so it suits values that belong to no row, such as a window
count; a per-row value belongs in a wrapper row.
A group decides presence from what the driver wrote and never inspects Go types.
For non-Scanner destinations that signal is a nil `**T`, so `ColumnNull` and a group
`Column` are by construction the hand-written read `var v *T; rows.Scan(&v)`, with
that read's limits on each driver, and integration tests compare against it. A
non-NULL value the driver decodes to nil, as pgx does for JSON `null`, counts as NULL.
Driver guards against borrowed memory see only `**T`. Declaration catches what it can
with `reflect`: pointer `T`, which would reach pgx as `***T` and change json and jsonb
values; `sql.RawBytes` and `pgtype.DriverBytes`; `pgtype.CompositeFields` and
`pgtype.Multirange`, which panic in the codec; and non-zero or unstable targets. The
driver types are identified by import path and name, because the package imports
neither `database/sql` nor pgx: a type defined from one of them, or another driver's
equivalent, passes and fails as the hand-written read does. Reading `*T` instead needs
a `Scan(any)` read, which loses native codecs and needs reflection on every row to
assign `T`. Inside a group a Scanner destination receives only non-NULL values
through a probing cell, which keeps a write-through target reachable, at the cost of
pgx's native codec: such a read can differ from a flat one, in value as well as error.

All destination checks run at declaration: `Column` and `Embed` call their dest on a
zero value, and `Embed` and `EmbedNull` run inner's dests in a declaring pass, where a dest under
`Embed` receives what that `Embed` returned. A panic at Scan would come before `Row.Scan`, the only closer of a pgx
`QueryRow` and the only release of a pooled one, and a recover around a failed driver
read leaves the connection unusable. Expressions render before any query, so a
declaration panic holds no connection. Scan itself checks nothing, so a
non-deterministic accessor is outside the contract rather than detected. The exception
is `ScanInto`'s nil destination, which exists only at the call and panics there. That
panic also precedes `Row.Scan` and strands a pgx `QueryRow` connection; a nil
destination would panic in the first dest regardless, so the check adds a message,
not a hazard.

The package writes into a row only through dest addresses; `ColumnNull`, `EmbedNull` and `Map` deliver through a `set` the caller writes.

Rejected alternatives:

- `Map` yielding its whole value (`Map(inner, convert) Projection[T]`, the previous shape) with a guard in `New`: the package assigned the value when `convert` returned and discarded what else was read into it. The package checks destinations only at declaration, each through its own dest, so it cannot detect that two dests reach the same value, as two `Embed`s of one field or a `Column` into a field of an embedded `Map` do, and `New` saw only its direct arguments. The cost of the replacement is an identity `set` for a whole-value `Map`, which still replaces what else was read into the value, now as a statement at the call.
- One fallible callback for `Map` (`set func(*D, B) error`): a converter shaped `func(B) (T, error)`, such as `netip.ParseAddr`, would need a wrapper at every call, and `set` would be fallible here and infallible in `ColumnNull` and `EmbedNull`.
- Go-type NULL inference in `EmbedNull`: needs reflection on every row and guesses at
  driver and Scanner ownership of NULL.
- Identifying driver types by printed name and comparing targets by printed value (the
  previous checks, written to avoid importing `reflect`): any package named `pgtype`
  or `sql` matched, and a type with its own `GoString` hid an initialised target.
  Declaration-time reflection is exact and costs nothing per row.
- Recovering an accessor panic before the row is read, draining the row with no-op
  Scanners and re-panicking: a second path for a programmer error, still failing at
  the first row rather than at declaration.
- Double-pointer `EmbedNull` and redeclared pointer buffers: callers restate each
  LEFT JOIN side, and `**T` names a field shape rather than optionality.
- All-setter columns: every column allocates.
- Fixed-arity combinators and dry-run mappers: arity caps and swappable arguments, or
  expressions rebuilt per row with branches on the row undetected.
- Recovering driver panics at a group boundary: `database/sql` holds its lock without
  a deferred unlock, so `Row.Scan` deadlocks before recover runs.
- Opaque metadata (the previous design): the package never used the pairing, so
  every caller wrote the binding and scan code.
- Finishing a group's cells in declaration order: a `set` declared before a `Column`
  read its zero value in a group and its row value when flat.
- Closing rows in `All` and `Collect`: `pgx.Rows.Close()` and `sql.Rows.Close() error`
  share no signature, and both drivers already close on exhaustion.
- A method usable as `pgx.RowToFunc`: function types must be identical, so no signature
  that omits pgx is assignable; pgx callers wrap `p.Scan(row.Scan)` or use `Collect`.
- A default-value column (`ColumnOr(expr, value, dest)`): `COALESCE` in the expression
  already says it, in the query where the default is visible.
- A named discard column: `ColumnNull` with a `set` that does nothing reads a column
  no field holds, with the Go type the driver should decode stated at the call.
- A helper that appends destinations to `Rows`: three lines at the caller, and the
  appended output is outside the declaration either way.
- Binding destinations once for a result set: a dest may allocate inside the row it
  is given, so destinations bound to one row would point outside the next.
- One row value shared by `All` and copied out for each row: a setter that keeps a
  pointer into its row would then see the next row's data.
- Two cell lists and the scratch pointer inside the reader: the reader passes by value
  through every part, and past eight words a flat three-column `Scan` measured about a
  tenth slower on arm64. One list kept in finish order, a bit set for the mode and the
  scratch as a second argument keep it at eight.
- Reserving the assigning cells' slots from a declared count and filling them by
  position: a count that disagreed with the parts would skip a setter, finish a stale
  cell or panic at `Scan`. Inserting each assigning cell ahead of the others needs no
  count, measures the same, and leaves the count a capacity hint.
- A row read of its own for `Scan`, to save the call into the shared one: about 2 ns a
  row, against two bodies to keep alike.
- An exported scratch for `ScanInto` loops: no caller needs it while `All` and
  `Collect` cover result sets, and it would make reuse rules part of the API.

Driver-independent [examples](../projection/example_test.go) live alongside the
package and run with its tests; driver behaviour is tested in `integration/`.
Public contracts belong to the projection package documentation linked above.

`Statement` and `Rowset` are sealed interfaces. SELECT, VALUES, TABLE and set
operations are rowsets; DML is Statement-only even with RETURNING.

Builders implement both interfaces with value methods, so Go also admits a
pointer to a builder. A pointer renders as the builder it points to and a nil
pointer is a nil statement. The switches over statements match values only;
`statementValue`, generated beside them, resolves a pointer in their default
branch, so a value pays nothing for it. `JSONTableColumn` and `JSONOperand`
admit pointers the same way and normalise them once, where a constructor
stores them.

A set operation has the width of its leftmost leaf. The renderer finds that
width once at the top of a chain and passes it down the left spine, so each
level measures only its own right branch and validating a chain is linear in
its length.

### Scope

qs models the statements an application builds at run time: SELECT, VALUES,
TABLE, set operations, INSERT, UPDATE, DELETE and MERGE, with CTEs, joins,
windows, JSON/JSONB operators, the SQL/JSON constructors, query functions and
`JSON_TABLE`, and keyset pagination. It does not model XML, EXPLAIN, TRUNCATE,
CREATE TABLE AS, materialized views, cursors, EXECUTE or SELECT INTO. `Call` and `QualifiedCall`
reach any function, `UnsafeSQL` and `Fragment` any expression syntax, and
`StatementSQL` any statement. Each modelled form costs a constructor, grammar
rules, tests and corpus coverage for as long as the library exists, so a form
earns its place by needing structure the escape hatches cannot give: bound
parameters in the right order, quoting, or a rule qs can check.

One PostgreSQL grammar is rendered, with `$n` parameters. There are no version
gates: syntax newer than the server fails at the server with its own message,
which a render-time check only duplicated for the versions qs knew. The
integration suite runs on PostgreSQL 15 through 18.

### Values

Statement builders, expressions, relations and the structural descriptors are
immutable values. Every builder method takes its receiver by value and returns
the changed copy, so deriving two queries from one base cannot make them
interfere, a builder cannot contain itself, and concurrent derivation and
rendering need no synchronisation. A copy shares its slices with its source;
`extend` appends through a slice clipped to its length, so an append always
writes storage no other value can see. Constructors copy the slices callers
pass. Bound parameter values are never copied: callers keep them unchanged
until execution completes.

An append therefore copies the list it extends. That is constant in practice
for lists that follow the shape of a query, such as conditions, CTEs and
windows. Rows follow the caller's data, so they also enter in one call:
`Rows` on an INSERT and `Rows` or `RowsExpr` on VALUES copy any number of rows
into one array. Adding rows one call at a time stays quadratic and the method
documentation says so.

A method result that is discarded changes nothing, and neither the compiler nor
`go vet` reports it. This is the cost of value semantics in Go.

`internal/buildergen` writes the methods every builder repeats, since a method
returning its receiver's own type must be declared once per type. It also
writes the SQL/JSON clause methods; see SQL/JSON below.
`make generate-check` fails when its output is stale.

### Expressions

`Expr` is a small value: a kind, a text and one `any`. Leaf kinds (one
identifier, a two-part qualified name, a bound parameter, trusted SQL, a
literal, a keyword, a string literal) need only the text or the bound value and
allocate nothing beyond it. A two-part name keeps its qualifier in the `any`;
`Col` is small enough to inline, so a name written as constants allocates
nothing and one built from run-time strings allocates the qualifier's string
header. Names of three or more parts are a node. Every composite form is a private node struct holding exactly the fields
it renders, built only through `nodeExpr`. The renderer and the structural
checks switch on the node's concrete type, so no tag can disagree with its
payload and an unknown form fails rendering instead of panicking. The zero
`Expr` is invalid. `Relation` follows the same rule: a single-name table is a
leaf, and every other form is a typed source.

`Expr.isColumn` is the one test for a column name across its three
representations; the counted projection and the SQL/JSON DEFAULT check both use
it.

Every type that is or converts to an expression carries the `qxExpression`
marker, including the builders that still need `Expr`, `End` or `Condition`.
`Param` and the other value-binding entry points refuse a marked value, so a
builder passed without its conversion fails rendering instead of reaching the
driver as an argument. A test scans the package source and fails when a
converting type lacks the marker.

Typed fields, tuples and JSON documents wrap `Expr` and use the same renderer.
Go types on a `Field` do not prove SQL types or schema validity. Finite grammar
states use named enums; unknown states fail validation.

### Identifiers and comparisons

Every string that names something is exactly one identifier and is quoted as
written. `Col`, `Table`, `Star`, `QualifiedCall` and `Collate` take a qualified
name as separate arguments; `Relation.Col` qualifies by a relation. Nothing is
split on dots, so the meaning of a name cannot depend on its content.

PostgreSQL keeps the first 63 bytes of an identifier, cut at a character
boundary, and compares names after that. `sameName` in `identifier.go` owns
that rule, and every uniqueness check compares names through it: CTE names,
window names, INSERT and MERGE target columns, SET targets, the OLD and NEW
aliases of RETURNING, and `JSON_TABLE` column and path names. A base window is
resolved through it too, so the name a duplicate check calls taken is the name
a later window can inherit from. Two names equal
in their kept part are one name to the server, which reports the duplicate
after a truncation notice. qs still renders a long name as written. The limit
is `NAMEDATALEN - 1` of a standard build, counted in UTF-8: a server built with
a longer limit or running another encoding can accept two names that qs
refuses.

Comparisons have one owner: methods of `Expr` that take an `Expr`. `Param`
binds a value. `Field[T]`, from `TypedCol` or `Typed`, is the value-binding
form: its methods take a `T` and bind it. The comparison, range, membership
and pattern constructors reject a bound untyped nil in any operand, directly,
as a field of a row constructor or as the array of ANY or ALL, because a
comparison with SQL NULL is never true; the DISTINCT forms accept it.

### Statement roles

CASE uses distinct searched and simple builders; `End` copies the branches into
a node. `Case` and `CaseOf` return pending values whose only method adds the
first branch, so the builders that own `Else` and `End` always hold one. MERGE category selectors produce terminal `MergeWhen` values; identity
overriding precedes non-default insertion. Qualified joins remain `PendingJoin`
until completion owns a nonempty predicate or column list. Distinct private
field names prevent explicit cross-role conversions. Zero values and unchecked
names still require rendering validation. A universal modifier bag was rejected
because it admits illegal method combinations; interface-backed completion
would add boxing.

Write destinations own `WriteValue` and `WriteRowValue`. Their distinct private
fields prevent conversion into general expressions and tuple wrappers. `Value`
binds application data, including nil, through the parameter boundary; a
generic constructor would prove no additional invariant and prevent inference
for `Value(nil)`. `Write` captures an ordinary expression; `Default` constructs
a token usable only at a direct destination position. The parameter boundary
rejects write descriptors. Explicit scalar targets preserve their complete
path; typed Field and JSON helpers remove the relation qualifier while
retaining field and subscript steps. Row writes preserve tuple versus ROW
syntax.

`InsertTarget` owns only the table descriptor. Optional `Columns` or `Targets`
selection creates `InsertColumnsTarget`, which captures one target list and
exposes only rows, SELECT and default sources. Required-first methods prevent
empty target lists; `ColumnsSlice` instead records explicit selection in
`insertBase.targetsSelected` and rejects empty lists when rendered. Assignment
INSERTs start directly from `InsertTarget`. Neither target selector is a
statement or rowset. Each source selection creates an `InsertRows`,
`InsertSelect`, `InsertAssignments` or `InsertDefaults`. These are statements,
never rowsets, and a completed one cannot change its column list or switch
source roles. `ValuesSlice` copies each row slice and retains empty rows as
invalid input; later rows cannot erase that failure. Defaults omit identity
overriding.

Conflict selectors distinguish unrestricted, inference and constraint targets.
Completion produces `ConflictNothing` or `ConflictUpdate`; only updates have an
action predicate. Generic OnConflict methods accept the exact completed value
types and normalize into one private optional value. Targeted selectors keep
`DoUpdate(first, rest...)` for statically nonempty calls; `DoUpdateSlice`
accepts dynamic lists and copies them. Nil and empty lists fail rendering with
`ErrInvalid`, never become `DO NOTHING`. `SetAllExcluded` treats column names
literally on both assignment sides; explicit `Assign` owns field and subscript
targets.

UPDATE and DELETE have two stages. `Update` and `DeleteFrom` return
`PendingUpdate` and `PendingDelete`, which hold the clauses PostgreSQL writes
before WHERE and are not statements. `Where(first, rest...)`, `WhereCurrentOf`
or `AllRows` completes one into `UpdateBuilder` or `DeleteBuilder`, which add
only RETURNING and the generated statement methods. `rowSelection` is the one
owner of which rows change; the pending types are its only writers and set
exactly one kind, so a predicate cannot coexist with a cursor or with all
rows, and a statement that reaches every row says so where it is built. Two
checks stay at render. A predicate that is TRUE for every row is not a
selection, because `And` and `NotIn` return TRUE for an empty list and a
dynamic filter with no entries would otherwise reach every row unannounced.
`rowIndependent` owns that decision: it folds TRUE and FALSE literals through
AND, OR, NOT and parentheses, since one-call `Where` sends every dynamic
filter through `And`. Bound values and trusted SQL are not read.
The zero `UpdateBuilder` and `DeleteBuilder` select nothing and fail. Trusted
SQL is not inspected. `AllRows` is a structural guard, not authorization.

SELECT's projection is chosen by its constructor: `Select(first, rest...)`,
`SelectCols` and `SelectSQL` are nonempty by signature, `SelectSlice` takes a
run-time list and fails rendering when it is empty, and `SelectNoColumns` is
the only zero-column form. Queries hold one row limit, a private variant of
none, a count, ALL or a count WITH TIES. The `Limit` family replaces it, as
`Offset` replaces the offset, so LIMIT and FETCH FIRST cannot both be present.
WITH TIES renders as FETCH FIRST after OFFSET and still needs ORDER BY and no
SKIP LOCKED lock, which are checked at render as PostgreSQL's grammar does.

SQL grammar owners enforce local policy: JSON integer selectors are
int4-bounded and cast to avoid PostgreSQL's unknown-parameter text overload.
Assignment destinations have their own grammar. Parenthesized indirection
preserves successive indexing and projection width. Window definitions validate
local inheritance and effective frame ordering. Unknown widths remain unknown;
all width consumers share a bounded inspector. Row-list membership widths are
checked at construction and subquery widths at rendering. Row-width errors
identify one-based row positions and expected and actual counts, never bound
data.

`LikePrefix`, `LikeSuffix`, `LikeContains` and their case-insensitive variants
own literal-match escaping: one pass escapes `!`, `%`, and `_`, then adds the
leading or trailing `%` required by the operation. The pattern is bound and the
fixed escape is emitted with `LiteralString("!")`. Empty suffix and contains
inputs intentionally match every non-NULL text value; callers decide whether an
empty filter should be omitted. `Like` and `ILike` keep raw-pattern semantics.

### SQL/JSON

`sqljson.go`, `sqljson_query.go` and `sqljson_table.go` own the SQL/JSON forms.
The grammar accepts any clause on any of these functions and the analyzer then
rejects most combinations, so a single options struct would defer nearly every
rule to the server. Each form is instead its own builder type, and the builder
value is the expression node the renderer walks: there is no second
representation to keep in step with it.

`jsonForms` in `internal/buildergen` is the only statement of which form
accepts which clause, and `sqljson_methods.go` is generated from it. A form has
a method only for a clause PostgreSQL accepts there: `JSONValueBuilder` has no
wrapper or `RETURNING ... FORMAT JSON`, `JSONExistsBuilder` has no `RETURNING`,
and each ON EMPTY and ON ERROR behavior exists only on the forms that take it.
The sets were read from PostgreSQL 18's analyzer by probing a server, and the
corpus adapter repeats them as the gaps it reports.

| Rule | Where it is enforced |
|---|---|
| Clause and behavior sets per form | Method sets, generated from `jsonForms` |
| FORMAT JSON only on an operand position or with RETURNING | `JSONOperand`, sealed; `ReturningFormatJSON` states type and format together |
| ENCODING other than UTF8 | No constructor; PostgreSQL rejects UTF16 and UTF32 |
| `RETURNING ... ENCODING UTF8` only with `bytea` | `ReturningFormatJSONUTF8` takes no type |
| `JSON_TABLE` needs a column; a NESTED PATH needs one | Required first argument |
| `JSON_TABLE` paths are string constants | `string` parameters |
| A column kind's clauses | Four column types behind the sealed `JSONTableColumn` |
| Empty `JSON_OBJECT()` or `JSON_ARRAY()` with a NULL or uniqueness clause | Render time: the constructors take run-time lists |
| `JSON_ARRAY(query)` width | Render time, when the width is known |
| Distinct `JSON_TABLE` column and path names | Render time: names are run-time strings, compared as PostgreSQL truncates them |
| `json_table_path_0` as a column or path name while the row path is unnamed | Render time; see below |
| `JSON_TABLE` column aliases no more than its columns | Render time: both counts are run-time |
| A path is not empty | Render time: paths are run-time strings |
| A nil operand, including a nil pointer to an operand type | Render time |
| OMIT QUOTES with WITH WRAPPER or WITH CONDITIONAL WRAPPER | Render time: the two choices are independent methods |
| `JSON_ARRAYAGG` with ORDER BY as a window function | Render time, in `aggregateTail`, which applies the same rule to `Call` |
| DEFAULT is a column | Render time; see below |
| DEFAULT holds a column inside a larger expression, or is only a bound parameter | PostgreSQL; see below |
| EMPTY ARRAY or EMPTY OBJECT on a scalar `JSON_TABLE` column | PostgreSQL: the rule depends on the column's SQL type |

PostgreSQL accepts a constant, a function call or an operator expression as a
DEFAULT behavior and no column reference. The renderer rejects a value that is
a column, bare or behind casts and parentheses, and leaves a column inside a
larger expression to the server. It does not reject a bound parameter.
PostgreSQL refuses a value that is only a parameter when the statement is
prepared, which is the extended query protocol, and accepts it when the driver
interpolates arguments, as pgx does under `QueryExecModeSimpleProtocol`; qs
does not know which protocol will carry the statement. An argument type that
excluded parameters would also exclude the function calls and operators over
them that PostgreSQL accepts.

PostgreSQL names an unnamed row path `json_table_path_0` and registers that
name before it checks the explicit ones, so a column or NESTED PATH with that
name is a duplicate. The names it generates for unnamed NESTED PATH groups
(`json_table_path_1` and later) are added after the check and collide with
nothing, so qs reserves only the first, and only while the row path has no
`PathName`.

Setting a wrapper and setting quotes do not rewrite each other. An earlier
design made `OmitQuotes` clear a wrapper and the reverse, which changed a query
without telling the caller; the pair now fails rendering.

A `JSON_TABLE` value column is evaluated as `JSON_QUERY` when it has FORMAT
JSON, a wrapper or quotes clause, or a JSON, array or composite type, and as
`JSON_VALUE` otherwise. qs does not know SQL types, so `JSONTableValueColumn`
carries the union of both behavior sets.

`aggregateTail` owns FILTER and OVER for function calls and for the two SQL/JSON
aggregates. It holds one window, inline or named, so `Over` and `OverNamed`
replace each other. It fails rendering when a call has both an in-call ORDER BY
and a window, which PostgreSQL does not implement.

## Rendering and lifetime

One renderer owns buffers, arguments, options, limits and the first error. It
traverses in emitted SQL order, including nested statements. Builders are
values of several hundred bytes, so the renderer takes them by pointer: a
builder's own `ToSQL` and `AppendSQL` methods render the receiver in place,
and the functions that take a `Statement` copy it out of the interface once.
`internal/buildergen` holds the one list of statement types and writes those
methods, the dispatch over a `Statement` and the error-path labels from it. Parameter nodes bind
values and emit their placeholder. `Param(any)` retains dynamic value types;
a generic type parameter would establish no additional invariant. `Param`,
`Value` and `ParamNull` bind untyped nil as NULL. A nil `ArrayParam` slice binds
an empty array, matching empty `In`/`NotIn` lists. Identifiers, literals,
operators and trusted fragments are never rewritten, although a combined trusted
condition is parenthesized as described under validation and security limits.
Rendering never calls `driver.Valuer`, resolves a schema or independently
renders subquery strings.

`Options` holds the parameter and nesting limits of one render. Parameter limits
include prefix arguments. `RenderError` preserves context and wraps sentinels.
Its error-owned path records static structural scopes, outermost first, with
one-based indexes. Indexed child scopes close immediately; only the transition
to the first error adds a step. Labels are formatted on failure, accumulated
during unwinding, and reversed once at the append boundary. This avoids
successful-render path allocation, shared scratch state, and a second traversal.
The path is not a complete syntax tree; error text is presentation rather than a
machine-readable contract. `ToSQL` discards partial output on failure. Append
methods preserve original slice lengths and visible prefixes, clear appended
argument references, and may change unused backing-array capacity. Cleanup
covers written slots in both the original and final argument arrays. Its bound
depends on the renderer appending arguments without shortening their length.

ORDER BY is the only ordering owner. `SelectBuilder` owns its keyset cursor
values; rendering derives the predicate from the final ORDER BY terms and appends
it as the last WHERE conjunct, so later ORDER BY, DISTINCT ON or cursor changes
are validated where they take effect. `CountRows` holds the query it was given
in a private relation source. That relation renders the query in its own
statement scope without ORDER BY, cursor, row limit, OFFSET or locks; it
keeps the suffix, whose relation to the omitted clauses qs cannot know.

ORDER BY is not inert: PostgreSQL evaluates an expression missing from the
projection as a hidden output column, so a set-returning function there
multiplies rows and an aggregate makes the query grouped. qs cannot tell those
from other calls, so the count appends ORDER BY expressions to the counted
projection, after the existing columns so ordinal references keep their
positions. A hidden column has no name, but an appended one would take its
function, column or field name, which DISTINCT ON and GROUP BY resolve against:
DISTINCT ON could pick the moved term instead of the input column and miscount
silently, and either clause could fail as ambiguous. Each appended expression
is therefore aliased `qs_order_N`, N being its ORDER BY position. PostgreSQL 18
drops unused outputs that are neither volatile nor set-returning, so no sort or
extra evaluation remains for ordinary keys. Names, ordinals and bound values
are skipped, after discarding grouping parentheses as PostgreSQL does: they
cannot change the rows, and a name or ordinal may refer to an output column
that the projection cannot see. SELECT DISTINCT is skipped because its ORDER BY
expressions already appear in the projection. Keyset key validation classifies
terms the same way, so `(1)` and `0x1` are ordinals and `("x")` may name an
output column there too.

Callers own buffer reuse and bound-value lifetime through execution.

An application builds a statement and renders it once per request, so
`BenchmarkBuildAndRender` is the figure that describes qs in use and
`TestBuildAndRenderAllocations` holds its allocation budget for the reference
query. `BenchmarkRender` renders a prebuilt statement into reused storage; it
isolates the renderer, and the tests that require it not to allocate guard the
`AppendSQL` contract, not the request path. No CPU budget is enforced, because
shared-host noise makes one unreliable.

## Validation and security limits

Structural checks cover malformed nodes, required clauses, identifiers, known
widths, duplicate assignments/CTEs, modifier conflicts, branch reachability and
configured limits. They cannot prove column existence, SQL types, privileges,
aggregate legality, functional dependencies, scalar-query row cardinality or
custom driver encoding. Raw projections have unknown width. Zero write values,
nil rowsets, names, unknown widths and row assignments supplied to INSERT
assignment sources retain runtime checks. The write roles prevent DEFAULT from
entering general expressions through the public typed API; trusted SQL remains
an intentional escape.

Required-first and required-two signatures keep literal collections nonempty
and write tuples at least two elements wide, including `Coalesce`, `Greatest`,
`Least`, `SetRow` and MERGE insertion operands. Unrestricted
variadics were rejected because their shorter dynamic calls admit empty grammar
completions; compile-negative cases in `safety_test.go` enforce the policy.

SELECT locking rejects structural DISTINCT, GROUP BY and HAVING clauses. An
unused named WINDOW is compatible with locking. Rendering records structural
OVER use in the current statement and checks it after ORDER BY and pagination,
before emitting locks. Statement entry saves and clears this flag; exit restores
the parent scope, so scalar subqueries, CTEs and counted relations cannot change
outer eligibility. Opaque SQL, aggregate classification and locks propagated
into FROM subqueries remain server-validated.

Keyset rendering checks cursor width, ASC/DESC terms without NULLS or USING,
DISTINCT ON, and keys that are ordinals, bound values or bare names. PostgreSQL
resolves an ORDER BY term against output column names, explicit or derived,
only when it is a bare name or an integer; every other term resolves against
input columns, as the WHERE cursor does. Rejecting bare names therefore keeps
both clauses on the same values without reading the projection. Trusted SQL and
`Fragment` keys are not inspected. It cannot prove that keys are NOT NULL or
that the last key is unique; rows violating those assumptions are skipped or
repeated across pages.

Identifiers are quoted; values bind unless an explicit literal is selected.
Unsafe fragments are trusted code. `Condition` owns their boolean boundary: an
`UnsafeSQL` or `Fragment` condition is parenthesized where qs combines it with
another condition, which covers condition lists including the keyset conjunct,
`And`/`Or` members and `Not`. Keyset rendering likewise parenthesizes a trusted
ORDER BY term where it becomes a comparison operand. A lone trusted condition
and trusted operands of caller-written operators render verbatim, because
wrapping `= ANY(...)` produces invalid SQL. qs's own composite conditions, `LIKE ... ESCAPE` and `OPERATOR(...)`, are
structural groups rather than fragments, so this rule never wraps them twice.
Transactions, roles, RLS and tenant context belong to the execution boundary.

## Evaluated API boundaries

These evaluations record selected representations and constraints. Deferred
alternatives are not supported APIs or implementation commitments.

| # | Boundary | Decision or constraint |
|---|---|---|
| 1 | NULL/absence | Keep public containers; false validity binds NULL. Opaque accessors have lower payoff. |
| 2 | CASE | Separate concrete branch roles, entered through a pending value that only adds the first branch. Reject sticky misuse state and a render check for zero branches. |
| 3 | INSERT source | Column selection before source completion; independent concrete roles; reject shared stages and competing source flags. |
| 4 | Join qualification | Pending qualifiers, completed CROSS/NATURAL; reject general qualifier methods and interface boxing. |
| 5 | Conflict target/action | Concrete selectors and completed actions; exact generic union, normalized optional value. |
| 6 | MERGE | Category selectors and terminal branches; pre-action override avoids an extra completion wrapper. |
| 7 | Window frames | Keep dynamic pair/inheritance checks; elaborate bound typestate cannot establish effective ordering. |
| 8 | Pagination | One row-limit variant that the `Limit` family replaces. Reject separate LIMIT and FETCH setters with a render-time conflict error (the previous shape): the two clauses are one fact. `FETCH FIRST n ROWS ONLY` has no spelling because it equals LIMIT; only WITH TIES needs FETCH. |
| 9 | SEARCH/CYCLE | Defer complete-name constructors; recursion, names and unsigned constants remain render concerns. |
| 10 | Assignment target | Defer dedicated destinations; wrapping arbitrary Expr merely relocates validation. |
| 11 | Render options | One normalization boundary; reject another options framework. |
| 12 | Physical DML target | Defer a table-only role; one owner must enforce aliases and operation-specific ONLY rules. |
| 13 | Mutation row selection | Pending UPDATE and DELETE completed by `Where`, `WhereCurrentOf` or `AllRows`; only a predicate that folds to TRUE is checked at render. Reject a check of top-level literals alone: `And` over two empty filters is as unselective as one. Reject a render-only check (the previous shape): the forgotten predicate is the common mistake and a type can refuse it. Reject `Where`/`WhereIf` on the completed statement: appending to a cursor or all-rows selection needs the conflict state the stages remove. Reject an optional guard (`RequireWhere`): the unguarded default is the dangerous one. The pending types carry only pre-WHERE clauses, so no fluent method is duplicated beyond WITH. |
| 14 | Function/aggregate role | Custom catalog classification belongs to PostgreSQL; avoid universal call typestate. |
| 15 | Window reference/copy | Keep distinct paths; a global registry introduces scope and ownership errors. |
| 16 | CTE body | Defer sealed rowset-or-DML capability; nil and top-level placement still need validation. |
| 17 | DEFAULT context | Opaque concrete write values; direct destination rendering, no general-expression conversion. |
| 18 | Query degree | Bounded width inspection; stars, composite expansion and trusted SQL prevent universal static arity. |
| 19 | CYCLE constants | Keep bounded inspection; capability bits require fragile propagation through wrappers. |
| 20 | Identifiers | Every string is one identifier; qualified names are separate arguments. Reject dot-splitting constructors beside literal ones (the previous shape): which one a helper used decided what `"a.b"` meant. Reject rejecting dots at render: a dot is legal in a PostgreSQL identifier, and a mistaken qualified name already fails at the server naming the column. |
| 21 | SELECT quantifier | Favor a local variant over whole-builder typestate. |
| 22 | RETURNING | Defer one projection/alias descriptor; preserve OLD/NEW roles. |
| 23 | Ordered-set calls | Narrow known helpers; custom function classification stays server-owned. |
| 24 | Builder lifetime | Immutable value builders with clipped appends. Reject mutable builders with a generated deep `Clone` (the previous shape): branching without `Clone` corrupted the sibling, nested builders stayed live so a parent changed after it was built, cycles needed detection, and the copier needed a generator. The costs accepted are a struct copy per method and that a discarded result is silent. |
| 25 | Expression representation | Leaf kinds plus typed private nodes dispatched by a concrete type switch. Reject one tag with an untyped payload and a checked accessor (the previous shape): agreement was a convention that panicked when broken. Reject a render method on a node interface: the renderer would pass through an interface call, which escape analysis treats as escaping, so every render would allocate it; structural checks would still need type switches. `table.column` is the one composite kept as a leaf, because it is the most frequent expression in a join: as a node it cost two allocations. Reject a second string field for the qualifier: it removes the allocation for run-time names too, but widens every `Expr` from 40 to 56 bytes, which measured 10–16% more bytes on statements with no qualified name. |
| 26 | Comparison ownership | `Expr` methods take `Expr`; `Field[T]` binds values. Reject parallel package-level helpers taking a column name and a value (the previous shape): three spellings of one comparison with different quoting and binding rules. |
| 27 | Comparison NULL | Reject untyped nil in either operand of a comparison, range, membership or pattern match, including row fields at any depth and the array of ANY/ALL; binding it made comparisons silently false. |
| 28 | Array NULL | Nil `ArrayParam` slices bind `{}`; NULL arrays require `NullExpr(TypeArray(...))`. |
| 29 | Versions and placeholders | One grammar and `$n` only. Reject per-render version gates: they restated server errors for a fixed list of versions and every new syntax needed a threshold. Reject `?` placeholders: no PostgreSQL driver needs them and `?` is also an operator. |
| 30 | Scope | See Scope above. Reject modelling utility statements and syntax that adds no parameter, quoting or checkable rule over `Call`, `UnsafeSQL` and `StatementSQL`. SQL/JSON was removed under this rule and restored: its clause rules are checkable (row 37) and it is in regular application use. XML stays out. |
| 31 | Keyset ownership | Builder-owned `SeekAfter`, derived from ORDER BY at render. Reject a separate keyset value (a second owner drifts from ORDER BY), typed arity (runtime-selected keys defeat it), `SeekBefore` (under the same ORDER BY and LIMIT it returns the first page) and the expansion without a leading bound (an index cannot bound the scan). |
| 32 | Ordering direction | `Order.Reverse` involution, including NULLS; reject a `Direction` enum that cannot reverse whole terms. |
| 33 | Count derivation | `CountRows` relation that appends ORDER BY expressions other than names, ordinals and bound values to the counted projection as `qs_order_N`. Reject removing clauses from a copy at the call (cannot remove locks and relies on callers removing every clause), projection replacement (miscounts DISTINCT and GROUP BY), keeping ORDER BY (PostgreSQL keeps subquery sorts), rejecting terms qs cannot classify (breaks ordinary keys such as `lower(name)`) and detecting aggregates or set-returning functions by name (qs cannot classify `Call` or trusted SQL, so misses would miscount silently). Unaliased appended expressions are rejected because their default output names can capture DISTINCT ON and GROUP BY references. Trusted SQL naming an output column fails in PostgreSQL instead of counting wrongly. |
| 34 | Keyset key resolution | Reject bare-name keys while a cursor is active; callers qualify keys or page a `Subquery`. Reject deriving output labels as PostgreSQL does (version-specific, such as `?column?` for `true` in 18, and blind to trusted SQL and `(c).*`), accepting bare keys under a provably safe projection (a select-list change elsewhere would invalidate the cursor), qualifying bare keys automatically (rewrites identifiers and is ambiguous across joins), filtering in a wrapping query (keys must be output columns, and an unflattened subquery loses the index seek) and rewrapping bare keys in a no-op expression (`COALESCE(a)` sorts without the index). |
| 35 | Corpus oracle | Frozen PostgreSQL 18 parse trees built by a Go adapter (`internal/postgrescorpus/parsetree`) through exported constructors only; `go test` replays every record with the standard library. The pinned PostgreSQL parser grades the output offline, when fixtures are regenerated. Reject generated Go test shards written by a Python emitter (the previous shape): 108k lines no compiler checked at their source, regenerated on every API change. Reject parsing inside `go test`: a PostgreSQL parser means cgo and a dependency in a standard-library module. Reject the adapter grading itself: it would agree with its own mistakes, so a record is `verified` only when the reparsed SQL equals the source tree. Out-of-scope and gap classification is the adapter's own; pinned counts keep it from growing unnoticed. |
| 36 | SELECT projection | Required-first `Select`, `SelectCols` and `SelectSQL`; `SelectSlice` for run-time lists; `SelectNoColumns` for zero columns. Reject a variadic `Select` that fails rendering when empty: a literal call can be checked by its signature. Reject a nonempty-list type for `SelectSlice`: projections and callers assemble lists at run time, so the check would only move. |
| 37 | SQL/JSON | One builder type per form, itself the expression node; method sets generated from one table of the clauses PostgreSQL accepts per form. Reject one builder with option enums (the previous shape: `JSONBehavior`, `JSONWrapper`, `JSONQuotes`, `JSONNullPolicy` values legal on some forms only, each checked at render or by the server). Reject an input-role type per operand position: one sealed `JSONOperand` covers every position that takes FORMAT JSON. Reject `Not` on `IsJSON`: PostgreSQL parses IS NOT JSON as NOT over the predicate, which `Condition().Not()` already states. Reject `JSONCol` and `JSONBCol` shorthands: `JSONExpr(Col(...))` says the same. Reject typed column kinds for `JSON_VALUE` and `JSON_QUERY` evaluation of a `JSON_TABLE` column: PostgreSQL chooses by SQL type, which qs does not model. |
| 38 | Row lists | A bulk `Rows` entry that copies once; single-row methods keep the clipped append and document its cost. Reject amortised growth through an array shared by copies with an atomic high-water mark: sound, but it puts shared mutable state inside values documented as immutable, adds a second list representation, lets a short value retain its siblings' rows, and costs an allocation on the two-row statement. Reject backward-linked immutable chunks: no shared mutation, but more bytes than the shared array and the renderer and width checks would iterate chunks. Reject applying either to every list: the others are sized by the query a person writes, not by data. |
