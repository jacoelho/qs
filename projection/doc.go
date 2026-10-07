// Package projection associates SQL result expressions with caller-defined metadata.
// Applications can reuse and extend declarations, then pass Expressions to qs.Select
// or a builder's Returning method. Metadata can describe field keys, destinations or
// other mapping information; this package never interprets or invokes it.
//
// Named adds an SQL alias and records it as string metadata. Output leaves both the
// expression and metadata unchanged, so aliases are optional and metadata need not
// describe a label. All columns in a projection share one metadata type. Result
// scanning, codecs, NULL handling and actual returned column labels belong to callers.
//
// Each supplied expression must produce one result column. The package cannot prove
// this for wildcards, trusted SQL or composite expansion. Named requires an unaliased
// expression: it adds an alias using qs.Expr.As rather than replacing one. Duplicate
// labels are permitted; callers own label uniqueness and mapper compatibility. qs
// performs its usual render checks, which do not establish complete SQL validity.
//
// A projection describes its declarations, not the final result of an arbitrary
// query. Expressions keep their SQL qualifiers. To describe an entire result, attach
// them to an otherwise empty output list and avoid independent output changes.
// Returning appends to existing expressions, and later column reordering can break
// the correspondence even when the result width stays the same.
//
// The zero Projection is empty. Empty declarations are permitted: qs.Select with
// no expressions fails rendering, while empty input to Returning adds no clause.
// Applications expecting returned rows must establish nonempty output before DML.
// qs.SelectNoColumns separately expresses an intentional zero-column SELECT.
//
// Projections own column positions and return owned slice positions. Copies are
// shallow: application objects, metadata pointers and nested query builders remain
// shared. Reads can run concurrently while those reachable values and graphs are
// stable. This package performs no I/O and owns no resources or background work.
package projection
