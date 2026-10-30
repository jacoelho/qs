package qs

import "strconv"

// CountRows returns SELECT count(*) over the rows query selects, ignoring its
// SeekAfter cursor, LIMIT, OFFSET, FETCH and row locks. query is rendered live,
// as the derived table "matching", without those clauses or its ORDER BY clause.
// Its WITH, DISTINCT, GROUP BY, HAVING, set-returning projections and trusted
// prefix and suffix fragments still apply, and nested subqueries keep all of
// their clauses. Later changes to query are rendered; pass query.Clone() to
// count a fixed query. A nil query, and a data-modifying WITH in query, fail
// rendering with ErrInvalid.
//
// The count does not sort, but an ORDER BY expression can change the rows: a
// set-returning function there multiplies them and an aggregate groups them.
// Unless query is SELECT DISTINCT, which already projects them, ORDER BY
// expressions other than names, ordinals and bound values are appended to the
// counted projection as qs_order_N, N being the term's ORDER BY position, to
// keep that effect; DISTINCT ON and GROUP BY in query must not name columns
// qs_order_N. Volatile ones are evaluated for every counted row. A trusted SQL
// term naming an output column can fail in PostgreSQL, because the projection
// cannot see output columns; order by the aliased expression instead.
//
// The count is a separate statement. It agrees with the pages only when both
// run in one REPEATABLE READ or SERIALIZABLE transaction.
func CountRows(query *SelectBuilder) *SelectBuilder {
	return Select(CountAll()).FromExpr(Relation{kind: relationMatching, value: query, alias: "matching"})
}

// appendMatching renders the rows CountRows counts. The statement suffix still
// follows, because qs cannot tell whether a trusted fragment belongs to the
// omitted clauses.
func (b *SelectBuilder) appendMatching(w *renderer) {
	b.appendRows(w, nil, nil, b.tail.order)
	if w.err != nil {
		return
	}
	w.foot(b.base)
}

// appendOrderExprs appends ORDER BY expressions to the projection, so a
// set-returning function there still multiplies rows and an aggregate still
// groups them without the sort. PostgreSQL evaluates such a term as a hidden
// output column, so appending preserves its effect. A hidden column has no
// name, but an appended one would default to its function, column or field
// name, which DISTINCT ON and GROUP BY can resolve to instead of the column
// the query means; the reserved alias keeps it out of their way. Names,
// ordinals and bound values cannot change the rows and are skipped: a name or
// ordinal may refer to an output column, which a projection cannot. SELECT
// DISTINCT requires every ORDER BY expression to appear in the projection
// already.
func (b *SelectBuilder) appendOrderExprs(w *renderer, order []Order) {
	if b.distinct == selectDistinct {
		return
	}
	separate := len(b.columns) > 0
	for i, term := range order {
		switch unparenthesized(term.expr).kind {
		case exprIdentifier, exprIdentifierParts, exprLiteral, exprParameter:
			continue
		default:
		}
		if separate {
			w.text(", ")
		}
		separate = true
		w.expr(term.expr)
		if w.stopped("order", i+1) {
			w.errorPath("ORDER BY", 0)
			return
		}
		w.text(` AS "qs_order_`)
		w.sql = strconv.AppendInt(w.sql, int64(i+1), 10)
		w.byte('"')
	}
}

// matchingRelation renders query as a parenthesized subquery in its own
// statement scope, as statement does for any other subquery.
func (w *renderer) matchingRelation(query *SelectBuilder) bool {
	w.byte('(')
	if outerHasWindow, ok := w.openStatement(); ok {
		if query == nil {
			w.fail(ErrInvalid, "statement", "nil SELECT")
		} else {
			query.appendMatching(w)
		}
		w.closeStatement(outerHasWindow)
	}
	if w.err != nil {
		w.errorPath("SELECT", 0)
		w.errorPath("subquery", 0)
		return false
	}
	w.byte(')')
	return true
}
