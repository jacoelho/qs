package qs

import (
	"errors"
	"testing"
)

func TestCountRowsOmitsOrderingPaginationAndLocks(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).From("t").Where(Eq("a", 1)).OrderBy(Asc("id")).SeekAfter(5).Limit(10).Offset(20).Lock(ForUpdate())
	checkSQL(t, CountRows(q), `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1)) AS "matching"`, 1)
	checkSQL(t, q, `SELECT "id" FROM "t" WHERE ("a" = $1) AND ("id" > $2) ORDER BY "id" ASC LIMIT $3 OFFSET $4 FOR UPDATE`, 1, 5, 10, 20)

	fetch := Select(Col("id")).From("t").OrderBy(Asc("id")).Fetch(3, OnlyRows)
	checkSQL(t, CountRows(fetch), `SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`)
}

func TestCountRowsKeepsRowShapingClauses(t *testing.T) {
	t.Parallel()
	inner := Select(Col("id")).From("u").OrderBy(Asc("id")).Limit(3)
	runCases(t, []renderCase{
		{"distinct", CountRows(Select(Col("a")).Distinct().From("t").OrderBy(Asc("a")).Limit(5)),
			`SELECT count(*) FROM (SELECT DISTINCT "a" FROM "t") AS "matching"`, nil},
		{"group_having", CountRows(Select(Col("a")).From("t").GroupBy("a").Having(CountAll().Gt(1)).OrderBy(Asc("a"))),
			`SELECT count(*) FROM (SELECT "a" FROM "t" GROUP BY "a" HAVING (count(*) > $1)) AS "matching"`, []any{1}},
		{"nested_clauses_survive", CountRows(Select(Col("id")).With(CTE("c", inner)).FromExpr(inner.As("s")).OrderBy(Asc("id")).Limit(5)),
			`SELECT count(*) FROM (WITH "c" AS (SELECT "id" FROM "u" ORDER BY "id" ASC LIMIT $1) SELECT "id" FROM (SELECT "id" FROM "u" ORDER BY "id" ASC LIMIT $2) AS "s") AS "matching"`, []any{3, 3}},
		{"prefix_suffix", CountRows(Select(Col("id")).From("t").Prefix(UnsafeSQL("/* p */")).Suffix(UnsafeSQL("/* s */")).Limit(1)),
			`SELECT count(*) FROM (/* p */ SELECT "id" FROM "t" /* s */) AS "matching"`, nil},
		{"window_with_lock", CountRows(Select(Col("id"), CountAll().Over(Window())).From("t").Lock(ForUpdate())),
			`SELECT count(*) FROM (SELECT "id", count(*) OVER () FROM "t") AS "matching"`, nil},
		{"invalid_cursor_is_omitted", CountRows(Select(Col("id")).From("t").SeekAfter(1)),
			`SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`, nil},
	})
}

func TestCountRowsRendersQueryLive(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).From("t").Where(Eq("a", 1))
	live := CountRows(q)
	frozen := CountRows(q.Clone())
	cloned := live.Clone()
	q.Where(Eq("b", 2)).OrderBy(Asc("id")).SeekAfter(3).Limit(4)

	checkSQL(t, live, `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1) AND ("b" = $2)) AS "matching"`, 1, 2)
	checkSQL(t, frozen, `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1)) AS "matching"`, 1)
	checkSQL(t, cloned, `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1)) AS "matching"`, 1)
}

func TestCountRowsCloneSharesRepeatedQuery(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).From("t")
	statement := Select(Scalar(CountRows(q))).FromExpr(q.As("page"))
	clone := statement.Clone()
	subquery, ok := clone.columns[0].value.(*subqueryExpression)
	if !ok {
		t.Fatalf("cloned expression has type %T, want *subqueryExpression", clone.columns[0].value)
	}
	scalar, ok := subquery.query.(*SelectBuilder)
	if !ok {
		t.Fatalf("scalar query has type %T, want *SelectBuilder", subquery.query)
	}
	counted := ownedPayload[*SelectBuilder](scalar.from[0].value)
	if counted == q || clone.from[0].value != counted {
		t.Fatal("clone did not map both references to one copied query")
	}
}

func TestCountRowsFailures(t *testing.T) {
	t.Parallel()
	checkError(t, CountRows(nil), ErrInvalid)
	checkError(t, Clone(CountRows(nil)), ErrInvalid)

	modifying := Select(Col("id")).With(CTE("d", DeleteFrom("t").Returning(Col("id")))).From("d")
	checkError(t, CountRows(modifying), ErrInvalid)
	_, _, countErr := CountRows(modifying).ToSQL()
	_, _, scalarErr := Select(Scalar(modifying)).ToSQL()
	count, _ := errors.AsType[*RenderError](countErr)
	scalar, _ := errors.AsType[*RenderError](scalarErr)
	if scalar == nil || count.Clause != scalar.Clause || count.Detail != scalar.Detail {
		t.Fatalf("CountRows error %v differs from subquery error %v", countErr, scalarErr)
	}
}
