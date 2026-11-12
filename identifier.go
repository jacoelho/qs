package qs

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Col names a column. Every argument is one identifier, quoted as written:
// Col("id") is "id", Col("u", "id") is "u"."id" and Col("a.b") is the single
// identifier "a.b". Nothing in a name is interpreted as SQL syntax, a path or a
// wildcard; use Star for a wildcard and Relation.Col to qualify by a relation.
//
// Col is small enough to inline, which lets a qualified name written as
// constants cost no allocation: the qualifier is then stored as static data.
func Col(first string, rest ...string) Expr {
	if len(rest) == 1 {
		return Expr{kind: exprQualified, value: first, text: rest[0]}
	}
	return unpairedColumn(first, rest)
}

// unpairedColumn is Col for one name, or for three or more parts such as
// schema.table.column.
func unpairedColumn(first string, rest []string) Expr {
	if len(rest) == 0 {
		return unqualifiedColumn(first)
	}
	return nodeExpr(newQualifiedIdentifier(first, rest))
}

// newQualifiedIdentifier owns the parts of first followed by rest, which must
// not be empty.
func newQualifiedIdentifier(first string, rest []string) *qualifiedIdentifier {
	if len(rest) != 1 {
		return &qualifiedIdentifier{parts: qualifiedName(first, rest)}
	}
	n := &qualifiedIdentifier{pair: [2]string{first, rest[0]}}
	n.parts = n.pair[:]
	return n
}

// unqualifiedColumn is Col for one unqualified name.
func unqualifiedColumn(name string) Expr { return Expr{kind: exprIdentifier, text: name} }

// isColumn reports whether e is a column name in any of its three
// representations: an unqualified leaf, the two-part leaf, or the node that
// holds three or more parts.
func (e Expr) isColumn() bool {
	if e.kind == exprIdentifier || e.kind == exprQualified {
		return true
	}
	_, qualified := e.node().(*qualifiedIdentifier)
	return qualified
}

// qualifiedName owns the parts of a name such as schema.table or table.column.
func qualifiedName(first string, rest []string) identifierParts { return ownedList(first, rest) }

// ownedList returns first followed by rest in storage the caller owns. It is
// how a constructor that requires at least one item takes its arguments.
func ownedList[T any](first T, rest []T) []T {
	list := make([]T, 1+len(rest))
	list[0] = first
	copy(list[1:], rest)
	return list
}

// gather returns the list given to a clause that needs at least one element:
// first is one element or a list of them, and rest follows it. The result is
// storage no caller holds. T is X or []X by the caller's constraint.
func gather[X, T any](first T, rest []X) []X {
	return gatherOnto[X](nil, first, rest)
}

// gatherOnto is gather after list, in one allocation: an appending clause
// would otherwise copy the gathered list a second time.
func gatherOnto[X, T any](list []X, first T, rest []X) []X {
	switch v := any(first).(type) {
	case []X:
		gathered := make([]X, 0, len(list)+len(v)+len(rest))
		return append(append(append(gathered, list...), v...), rest...)
	case X:
		gathered := make([]X, 0, len(list)+1+len(rest))
		return append(append(append(gathered, list...), v), rest...)
	}
	return list
}

// appendColumns returns list followed by one expression per item of a clause
// that takes column names or expressions. A name is one unqualified column,
// quoted as written. The result is storage no caller holds.
func appendColumns[T string | Expr](list []Expr, items []T) []Expr {
	switch v := any(items).(type) {
	case []string:
		return extendWith(list, v, unqualifiedColumn)
	case []Expr:
		return extend(list, v...)
	}
	return list
}

// appendRelations is appendColumns for a clause that takes table names or
// relations. A name is one unqualified table, quoted as written.
func appendRelations[T string | Relation](list []Relation, items []T) []Relation {
	switch v := any(items).(type) {
	case []string:
		return extendWith(list, v, table)
	case []Relation:
		return extend(list, v...)
	}
	return list
}

// relationOf returns the relation a table name or a relation stands for.
func relationOf[T string | Relation](item T) Relation {
	switch v := any(item).(type) {
	case string:
		return table(v)
	case Relation:
		return v
	}
	return Relation{}
}

// Star constructs a SELECT wildcard, optionally qualified by a relation name
// given as identifiers: Star("u") is "u".* and Star("public", "users") is
// "public"."users".*.
func Star(qualifier ...string) Expr {
	if len(qualifier) == 0 {
		return nodeExpr(unqualifiedStar)
	}
	return nodeExpr(&starExpression{cloneSlice(qualifier)})
}

// unqualifiedStar is shared so that Star() does not allocate. It is never
// written.
var unqualifiedStar = &starExpression{}

// identifierLimit is the number of bytes PostgreSQL keeps of an identifier:
// NAMEDATALEN-1 in a standard build.
const identifierLimit = 63

// sameName reports whether PostgreSQL reads a and b as one name. Its scanner
// truncates a longer identifier to identifierLimit bytes before any name is
// compared, so every uniqueness rule qs checks compares names through here.
// The limit is counted in UTF-8, which is exact for a UTF8 server encoding.
func sameName(a, b string) bool {
	if len(a) <= identifierLimit && len(b) <= identifierLimit {
		return a == b
	}
	return sameTruncatedName(a, b)
}

// sameTruncatedName is the slow path of sameName, kept out of line so that
// sameName inlines.
//
//go:noinline
func sameTruncatedName(a, b string) bool {
	return truncatedIdentifier(a) == truncatedIdentifier(b)
}

// truncatedIdentifier returns the part of name PostgreSQL keeps. It cuts at a
// character boundary, as the server does.
func truncatedIdentifier(name string) string {
	if len(name) <= identifierLimit {
		return name
	}
	end := identifierLimit
	for end > 0 && !utf8.RuneStart(name[end]) {
		end--
	}
	return name[:end]
}

func (w *renderer) identifierPart(s string) {
	if !w.require(s != "" && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0, "identifier", "empty, NUL-containing or invalid UTF-8 identifier") {
		return
	}
	w.byte('"')
	for {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			w.text(s)
			break
		}
		w.text(s[:i+1])
		w.byte('"')
		s = s[i+1:]
	}
	w.byte('"')
}

// star renders a wildcard and its optional qualifier.
func (w *renderer) star(n *starExpression) {
	if len(n.qualifier) != 0 {
		w.qualifiedName(n.qualifier, "identifier")
		w.byte('.')
	}
	w.byte('*')
}

// qualifiedName renders parts as quoted identifiers joined by dots.
func (w *renderer) qualifiedName(parts identifierParts, clause string) {
	if !w.require(len(parts) > 0, clause, "empty identifier") {
		return
	}
	for i, part := range parts {
		if i != 0 {
			w.byte('.')
		}
		w.identifierPart(part)
	}
}

func (w *renderer) names(names []string) {
	for i, n := range names {
		if i != 0 {
			w.text(", ")
		}
		w.identifierPart(n)
	}
}

func cloneSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	return append([]T(nil), values...)
}

// extend returns s followed by items in storage that no other value can
// write. Builders are values: a copy shares its slices with the original, so
// appending into spare capacity would change both. The capacity is clipped so
// that append always moves to a new array.
func extend[T any](s []T, items ...T) []T {
	return append(slices.Clip(s), items...)
}

// extendRows returns list followed by a copy of rows. All the added rows
// share one array and each is clipped to its own length, so adding n rows
// costs two allocations whatever n is.
func extendRows[T any](list, rows [][]T) [][]T {
	cells := 0
	for _, row := range rows {
		cells += len(row)
	}
	array := make([]T, 0, cells)
	extended := make([][]T, len(list), len(list)+len(rows))
	copy(extended, list)
	for _, row := range rows {
		start := len(array)
		array = append(array, row...)
		extended = append(extended, array[start:len(array):len(array)])
	}
	return extended
}

// extendRowsWith is extendRows for cells converted from values.
func extendRowsWith[T, V any](list [][]T, rows [][]V, convert func(V) T) [][]T {
	cells := 0
	for _, row := range rows {
		cells += len(row)
	}
	array := make([]T, 0, cells)
	extended := make([][]T, len(list), len(list)+len(rows))
	copy(extended, list)
	for _, row := range rows {
		start := len(array)
		for _, value := range row {
			array = append(array, convert(value))
		}
		extended = append(extended, array[start:len(array):len(array)])
	}
	return extended
}

// extendWith is extend for items converted from values.
func extendWith[T, V any](s []T, values []V, convert func(V) T) []T {
	if len(values) == 0 {
		return s
	}
	extended := make([]T, len(s), len(s)+len(values))
	copy(extended, s)
	for _, value := range values {
		extended = append(extended, convert(value))
	}
	return extended
}

// convertedList is ownedList for items converted from values.
func convertedList[T, V any](first V, rest []V, convert func(V) T) []T {
	list := make([]T, 1, 1+len(rest))
	list[0] = convert(first)
	for _, value := range rest {
		list = append(list, convert(value))
	}
	return list
}
