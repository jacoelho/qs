package qs

import (
	"strings"
	"unicode/utf8"
)

// Col constructs a dotted identifier path. Dots delimit identifier components;
// only a final * is a wildcard. Nothing in path is interpreted as SQL syntax.
func Col(path string) Expr { return Expr{kind: exprIdentifier, text: path} }

// Ident quotes literal identifier components, including dots inside a component.
// Ident("a.b") is "a.b"; Col("a.b") is "a"."b". A literal * is quoted.
func Ident(parts ...string) Expr {
	return Expr{kind: exprIdentifierParts, value: identifierParts(cloneSlice(parts))}
}

// Star constructs a SELECT wildcard, optionally qualified by a dotted path.
func Star(table ...string) Expr {
	if len(table) == 0 {
		return Col("*")
	}
	if len(table) != 1 {
		return invalidExpr("star", "expected zero or one qualifier")
	}
	return Col(table[0] + ".*")
}

func (w *renderer) identifierPart(s string) {
	if !w.require(s != "" && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0, "identifier", "empty, NUL-containing or invalid UTF-8 identifier") {
		return
	}
	w.byte('"')
	for i := range len(s) {
		if s[i] == '"' {
			w.byte('"')
		}
		w.byte(s[i])
	}
	w.byte('"')
}

func (w *renderer) identifierPath(path string, wildcard bool) {
	if path == "" {
		w.fail(ErrInvalid, "identifier", "empty identifier")
		return
	}
	for {
		part, rest, found := strings.Cut(path, ".")
		if part == "*" && wildcard && !found {
			w.byte('*')
		} else {
			if part == "*" {
				w.fail(ErrInvalid, "identifier", "wildcard is only valid at the end of a projection path")
				return
			}
			w.identifierPart(part)
		}
		if !found {
			return
		}
		w.byte('.')
		path = rest
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
