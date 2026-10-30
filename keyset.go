package qs

import (
	"slices"
	"strings"
)

// keysetCursor holds the bound ORDER BY key values that a page starts after.
// An empty list means no cursor. The pointer keeps SelectBuilder in a smaller
// allocation size class, and RemoveOrderBy and Reset keep its storage for reuse.
type keysetCursor struct{ values []Expr }

func (c *keysetCursor) active() bool { return c != nil && len(c.values) > 0 }

func (c *keysetCursor) clear() {
	if c != nil {
		clear(c.values)
		c.values = c.values[:0]
	}
}

// SeekAfter restricts the query to rows strictly after a cursor in ORDER BY
// order, for keyset pagination. Pass the ORDER BY key values of the last row of
// the previous page, one per term; each call replaces the cursor. Rendering
// fails with ErrInvalid unless the cursor width equals the ORDER BY width and
// every term is an ASC or DESC expression without NULLS FIRST/LAST, USING or an
// ordinal. It also fails for DISTINCT ON, and when a bare column key names an
// output alias of a different expression; page such queries by wrapping them
// with Subquery. Set operations, VALUES and TABLE are paged the same way.
//
// qs cannot check the schema. Pages are complete and disjoint only when every
// key is NOT NULL and the last term is unique, such as a primary key. Keys are
// evaluated in WHERE, where output aliases, aggregates and window functions are
// unavailable, and window functions in the projection see only rows after the
// cursor. Trusted SQL keys are compared in parentheses, and qs cannot check
// whether they name an output alias. Untyped nil fails rendering. A typed nil
// binds NULL: rows that tie with the cursor on every earlier key are skipped,
// and a NULL first key selects no rows.
//
// Uniform directions render one row comparison, ("a", "b") > ($1, $2), which a
// B-tree index on the keys in that order can serve. Mixed directions expand to
// an equivalent disjunction that binds earlier keys again: three keys bind
// seven arguments.
//
// RemoveOrderBy and Reset remove the cursor; Clone copies it. For the previous
// page, reverse each term with Order.Reverse, seek after the first row of the
// current page, and reverse the fetched rows.
func (b *SelectBuilder) SeekAfter(first any, rest ...any) *SelectBuilder {
	if b.cursor == nil {
		b.cursor = new(keysetCursor)
	}
	b.cursor.clear()
	values := slices.Grow(b.cursor.values, 1+len(rest))
	values = append(values, comparand(first))
	for _, value := range rest {
		values = append(values, comparand(value))
	}
	b.cursor.values = values
	return b
}

// keyset renders the WHERE conjunct admitting rows strictly after values in
// the ORDER BY of b. Terms are validated here rather than at SeekAfter because
// ORDER BY and the projection may change until rendering.
func (b *SelectBuilder) keyset(w *renderer, values []Expr) {
	order := b.tail.order
	if !w.require(len(order) == len(values), "keyset", "cursor width must equal ORDER BY width") {
		return
	}
	if !w.require(b.distinct != selectDistinctOn, "keyset", "DISTINCT ON keeps one row per group before the cursor filter applies; page a Subquery of it instead") {
		return
	}
	uniform := true
	for i, term := range order {
		b.keysetTerm(w, term)
		if w.stopped("order", i+1) {
			return
		}
		uniform = uniform && term.direction == order[0].direction
	}
	w.byte('(')
	if uniform {
		w.keysetRow(order, values)
	} else {
		w.keysetExpansion(order, values)
	}
	if w.err != nil {
		return
	}
	w.byte(')')
}

func (b *SelectBuilder) keysetTerm(w *renderer, term Order) {
	if !w.require(term.direction == ascending || term.direction == descending, "keyset", "order term must be ASC or DESC") {
		return
	}
	if !w.require(term.nulls == defaultNulls, "keyset", "order term cannot set NULLS FIRST or NULLS LAST; keys must be NOT NULL") {
		return
	}
	if !w.require(term.expr.kind != exprLiteral && term.expr.kind != exprParameter, "keyset", "order term must be an expression over rows, not an ordinal or bound value") {
		return
	}
	name, bare := bareIdentifier(term.expr)
	if !bare {
		return
	}
	for _, column := range b.columns {
		if column.kind != exprAlias || column.text != name {
			continue
		}
		if aliased, ok := bareIdentifier(column.unary()); !ok || aliased != name {
			// ORDER BY resolves a bare name to the output column, but WHERE
			// resolves it to an input column, so the pages would follow a
			// different ordering than the cursor.
			w.fail(ErrInvalid, "keyset", "order term names an output alias of a different expression; order by that expression")
			return
		}
	}
}

// bareIdentifier reports the name of a single-component identifier, which
// PostgreSQL may resolve to an output column in ORDER BY.
func bareIdentifier(e Expr) (string, bool) {
	switch e.kind {
	case exprIdentifier:
		if e.text != "*" && strings.IndexByte(e.text, '.') < 0 {
			return e.text, true
		}
	case exprIdentifierParts:
		if parts := ownedPayload[identifierParts](e.value); len(parts) == 1 {
			return parts[0], true
		}
	default:
	}
	return "", false
}

// keysetRow renders one row comparison; a B-tree index on the keys in ORDER BY
// order serves it for either uniform direction.
func (w *renderer) keysetRow(order []Order, values []Expr) {
	if len(order) == 1 {
		w.keysetComparison(order, values, 0, false)
		return
	}
	w.byte('(')
	for i, term := range order {
		if i != 0 {
			w.text(", ")
		}
		w.expr(term.expr)
		if w.stopped("order", i+1) {
			return
		}
	}
	w.byte(')')
	w.text(keysetOperator(order[0].direction))
	w.byte('(')
	w.keysetValues(values)
	if w.err != nil {
		return
	}
	w.byte(')')
}

func (w *renderer) keysetValues(values []Expr) {
	for i, value := range values {
		if i != 0 {
			w.text(", ")
		}
		w.expr(value)
		if w.stopped("cursor", i+1) {
			return
		}
	}
}

// keysetExpansion renders the lexicographic disjunction for mixed directions:
// k1 > v1 OR (k1 = v1 AND k2 < v2) OR ..., each operator following its term.
// The leading inclusive bound repeats the first disjunct's range so an index on
// the first key can bound the scan; the disjunction alone is only a filter.
func (w *renderer) keysetExpansion(order []Order, values []Expr) {
	w.byte('(')
	w.keysetComparison(order, values, 0, true)
	if w.err != nil {
		return
	}
	w.text(") AND (")
	for i := range order {
		if i != 0 {
			w.text(" OR (")
		}
		for j := range i {
			w.byte('(')
			w.bounded(order[j].expr)
			if w.stopped("order", j+1) {
				return
			}
			w.text(" = ")
			w.expr(values[j])
			if w.stopped("cursor", j+1) {
				return
			}
			w.text(") AND ")
		}
		w.byte('(')
		w.keysetComparison(order, values, i, false)
		if w.err != nil {
			return
		}
		w.byte(')')
		if i != 0 {
			w.byte(')')
		}
	}
	w.byte(')')
}

func (w *renderer) keysetComparison(order []Order, values []Expr, i int, inclusive bool) {
	w.bounded(order[i].expr)
	if w.stopped("order", i+1) {
		return
	}
	operator := keysetOperator(order[i].direction)
	if inclusive {
		operator = keysetInclusiveOperator(order[i].direction)
	}
	w.text(operator)
	w.expr(values[i])
	w.stopped("cursor", i+1)
}

func keysetOperator(d direction) string {
	if d == descending {
		return " < "
	}
	return " > "
}

func keysetInclusiveOperator(d direction) string {
	if d == descending {
		return " <= "
	}
	return " >= "
}
