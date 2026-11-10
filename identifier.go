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

// Star constructs a SELECT wildcard, optionally qualified by a relation name
// given as identifiers: Star("u") is "u".* and Star("public", "users") is
// "public"."users".*.
func Star(qualifier ...string) Expr {
	if len(qualifier) == 0 {
		return nodeExpr(unqualifiedStar)
	}
	return nodeExpr(&starExpression{cloneSlice(qualifier)})
}

// unqualifiedStar is shared so that Star() and CountAll() do not allocate. It
// is never written.
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
