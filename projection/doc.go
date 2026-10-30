// Package projection declares SQL result columns together with where each one is
// read. One declaration owns output order, destinations and nesting, and drives
// both a query's output list and the scan of each row:
//
//	p := projection.New(
//		projection.Column(qs.Col("id"), func(u *User) *int64 { return &u.ID }),
//		projection.ColumnNull(qs.Col("email"), func(u *User, v *string) { u.Email = v }),
//	)
//	rows, err := conn.Query(ctx, sql, args...) // sql rendered from p.Expressions()
//	user, err := p.Scan(rows.Scan)
//
// Column reads a required column, and ColumnNull a nullable one. Embed nests a
// declaration in a field, and EmbedNull nests it as an optional group for the
// nullable side of a LEFT JOIN, so one declaration serves both sides of a join.
// Map transforms a declaration's value after the row is read. New concatenates.
// For aliases use qs.Expr.As; for self-joins use a function taking qs.Relation.
// Add a column outside D with a wrapper row: Embed the declaration beside a Column.
//
// Scan is positional and invokes the caller's function, so the package performs no
// I/O and imports no driver. Attach Expressions to an otherwise empty SELECT or
// RETURNING list: other output changes break the correspondence, and a reorder of
// equal width can go unnoticed. SQL types are not checked against T. Incompatible
// types fail when a row is scanned, while convertible ones, such as an integer
// into a string, are coerced by the driver. A width mismatch fails only when a row
// is scanned, so an empty result hides it.
//
// Each expression must produce one result column; the package cannot prove this
// for wildcards, trusted SQL or composite expansion. Empty declarations are
// permitted: qs.Select with no expressions fails rendering, while empty input to
// Returning adds no clause. Applications expecting returned rows must establish
// nonempty output before DML. qs.SelectNoColumns separately expresses an intentional
// zero-column SELECT.
//
// Column accepts NULL outside a group when T can hold it, such as *string, []byte
// or sql.NullString, and returns ErrNull inside a present EmbedNull group, which also
// rejects a pointer T. Declare nullable columns with ColumnNull so a declaration reads
// the same in both places. ColumnNull and group columns read as var v *T; rows.Scan(&v)
// does: see EmbedNull.
//
// Column and Embed dests run on a zero value at declaration, repeatedly under EmbedNull,
// and once per Scan, before the row is read. They must be deterministic and must not
// retain their argument beyond the field they return. A dest that panics at Scan would
// strand the query, leaving a pgx connection busy; a declaration panic holds nothing,
// so dests are checked there. set and convert run after the row is read.
// A Scanner that panics hangs database/sql, with or without this package.
//
// Projections are immutable and safe for concurrent reads and Scan. Declare one once
// per alias and reuse it: declaring runs the checks above and allocates per column,
// while reuse costs only Expressions. Copies are shallow: nested query builders stay
// shared. This package owns no resources or background work.
package projection
