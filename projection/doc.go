// Package projection declares SQL result columns together with where each one is
// read. One declaration owns output order, destinations and nesting, and drives
// both a query's output list and the scan of each row:
//
//	p := projection.New(
//		projection.Column(qs.Col("id"), func(u *User) *int64 { return &u.ID }),
//		projection.ColumnNull(qs.Col("email"), func(u *User, v *string) { u.Email = v }),
//	)
//	rows, err := conn.Query(ctx, sql, args...) // sql rendered from p.Expressions()
//	if err != nil {
//		return err
//	}
//	defer rows.Close()
//	users, err := p.Collect(rows)
//
// Column reads a required column, ColumnNull a nullable one, and ColumnOrZero a
// nullable one whose NULL reads as the zero value. Embed nests a declaration in a field, and EmbedNull nests it as an optional group for the
// nullable side of a LEFT JOIN, so one declaration serves both sides of a join.
// Map converts a declaration's value after the row is read and passes the result to a set. New concatenates.
// For aliases use qs.Expr.As; for self-joins use a function taking qs.Relation.
// Add a column outside D with a wrapper row: Embed the declaration beside a Column.
//
// Scan reads one row through the caller's scan function, normally rows.Scan or
// row.Scan, and ScanInto reads it into a value the caller already holds. All and
// Collect read a result set through the methods pgx.Rows and *sql.Rows share, reuse
// their working memory from row to row, and leave closing the result set to the caller. Reading is positional and goes through the caller's
// values, so the package performs no I/O and imports no driver. Put Expressions first
// in a SELECT or RETURNING list, unchanged: other output before or among them breaks
// the correspondence, and a reorder of equal width can go unnoticed. Output selected
// after them is read by destinations the scan function appends: see ScanInto. SQL
// types are not checked against T. Incompatible types fail when a row is scanned,
// while convertible ones, such as an integer into a string, are coerced by the driver.
// A width mismatch fails only when a row is scanned, so an empty result hides it.
//
// Each expression must produce one result column; the package cannot prove this
// for wildcards, trusted SQL or composite expansion. Empty declarations are
// permitted: qs.SelectSlice with no expressions fails rendering, while empty input to
// Returning adds no clause. Applications expecting returned rows must establish
// nonempty output before DML. qs.SelectNoColumns separately expresses an intentional
// zero-column SELECT.
//
// Column accepts NULL outside a group when T can hold it, such as *string, []byte
// or sql.NullString, and returns ErrNull inside a present EmbedNull group, which also
// rejects a pointer T. Declare nullable columns with ColumnNull or ColumnOrZero so a
// declaration reads the same in both places. Both, and group columns, read as
// var v *T; rows.Scan(&v) does: see EmbedNull.
//
// Column, ColumnOrZero and Embed dests run on a zero value at declaration, again at the
// declaration of each Embed and EmbedNull that encloses them, and once per Scan, before
// the row is read. They must be deterministic and must not retain their argument beyond
// the field they return. A dest that panics at Scan would strand the query, leaving a
// pgx connection busy; a declaration panic holds nothing, so dests are checked there.
// set and convert run after the row is read, in declaration
// order, and each finds every Column and ColumnOrZero of its row and of the rows
// enclosing it assigned. The package writes into a row only through the addresses
// Column and ColumnOrZero dests returned; every other write is a statement in a set.
// Declarations that reach the same value, such as two Embeds of one field, therefore
// keep what they read unless a set assigns over it: a set that assigns its whole value
// replaces what was read into it before the set ran.
// A set may keep the pointers it receives. The *T of a ColumnNull and the *N of an
// EmbedNull belong to the caller and are not written again; inside an EmbedNull group
// the row is that *N, so a pointer an inner set kept points into it. A kept pointer to
// any other row stays valid and no later row is written through it. It points into the
// caller's value when that value was passed to ScanInto; whether it points into a value
// Scan, All or Collect returned is unspecified.
// A Scanner that panics hangs database/sql, with or without this package.
//
// Projections are immutable and safe for concurrent reads and Scan. Declare one once
// per alias and reuse it: declaring runs the checks above and allocates per column,
// while reuse costs only Expressions. This package owns no resources or background
// work.
package projection
