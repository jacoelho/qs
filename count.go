package qs

// CountRows returns SELECT count(*) over the rows query selects before ordering
// and pagination. query is rendered live, as the derived table "matching",
// without its ORDER BY, SeekAfter cursor, LIMIT, OFFSET, FETCH and row locks.
// Its WITH, DISTINCT, GROUP BY, HAVING, set-returning projections and trusted
// prefix and suffix fragments still apply, and nested subqueries keep all of
// their clauses. Later changes to query are rendered; pass query.Clone() to
// count a fixed query. A nil query, and a data-modifying WITH in query, fail
// rendering with ErrInvalid.
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
	b.appendRows(w, nil, nil)
	if w.err != nil {
		return
	}
	w.foot(b.base)
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
