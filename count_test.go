package qs

import (
	"errors"
	"testing"
)

func TestCountRowsOmitsOrderingPaginationAndLocks(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).
		From("t").
		Where(Col("a").Eq(Param(1))).
		OrderBy(Col("t", "id").Asc()).
		SeekAfter(5).
		Limit(10).
		Offset(20).
		Lock(ForUpdate())
	checkSQL(t, CountRows(q), `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1)) AS "matching"`, 1)
	checkSQL(t, q, `SELECT "id" FROM "t" WHERE ("a" = $1) AND ("t"."id" > $2) ORDER BY "t"."id" ASC LIMIT $3 OFFSET $4 FOR UPDATE`, 1, 5, 10, 20)

	fetch := Select(Col("id")).From("t").OrderBy(Col("id").Asc()).LimitWithTies(3)
	checkSQL(t, CountRows(fetch), `SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`)
}

func TestCountRowsKeepsRowShapingClauses(t *testing.T) {
	t.Parallel()
	inner := Select(Col("id")).From("u").OrderBy(Col("id").Asc()).Limit(3)
	runCases(t, []renderCase{
		{
			"distinct",
			CountRows(Select(Col("a")).Distinct().From("t").OrderBy(Col("a").Asc()).Limit(5)),
			`SELECT count(*) FROM (SELECT DISTINCT "a" FROM "t") AS "matching"`,
			nil,
		},
		{
			"group_having",
			CountRows(
				Select(Col("a")).
					From("t").
					GroupBy("a").
					Having(CountAll().Gt(Param(1))).
					OrderBy(Col("a").Asc()),
			),
			`SELECT count(*) FROM (SELECT "a" FROM "t" GROUP BY "a" HAVING (count(*) > $1)) AS "matching"`,
			[]any{1},
		},
		{
			"nested_clauses_survive",
			CountRows(
				Select(Col("id")).
					With(CTE("c", inner)).
					From(inner.As("s")).
					OrderBy(Col("id").Asc()).
					Limit(5),
			),
			`SELECT count(*) FROM (WITH "c" AS (SELECT "id" FROM "u" ORDER BY "id" ASC LIMIT $1) SELECT "id" FROM (SELECT "id" FROM "u" ORDER BY "id" ASC LIMIT $2) AS "s") AS "matching"`,
			[]any{3, 3},
		},
		{
			"prefix_suffix",
			CountRows(
				Select(Col("id")).
					From("t").
					Prefix(UnsafeSQL("/* p */")).
					Suffix(UnsafeSQL("/* s */")).
					Limit(1),
			),
			`SELECT count(*) FROM (/* p */ SELECT "id" FROM "t" /* s */) AS "matching"`,
			nil,
		},
		{
			"window_with_lock",
			CountRows(Select(Col("id"), CountAll().Over(Window())).From("t").Lock(ForUpdate())),
			`SELECT count(*) FROM (SELECT "id", count(*) OVER () FROM "t") AS "matching"`,
			nil,
		},
		{"invalid_cursor_is_omitted", CountRows(Select(Col("id")).From("t").SeekAfter(1)),
			`SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`, nil},
		{
			"bare_key_cursor_is_omitted",
			CountRows(Select(Col("id")).From("t").OrderBy(Col("id").Asc()).SeekAfter(1)),
			`SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`,
			nil,
		},
	})
}

// PostgreSQL evaluates an ORDER BY expression that is not an output column as
// a hidden output column, so a set-returning function there multiplies rows
// and an aggregate makes the query grouped. Names, ordinals and bound values
// cannot, even in parentheses, which PostgreSQL discards.
func TestCountRowsKeepsOrderExpressionsThatChangeRows(t *testing.T) {
	t.Parallel()
	q := func() SelectBuilder { return Select(Col("id")).From("t") }
	const unchanged = `SELECT count(*) FROM (SELECT "id" FROM "t") AS "matching"`
	inner := Select(Col("id")).From("u").OrderBy(Lower(Col("y")).Asc()).Limit(3)
	runCases(t, []renderCase{
		{"column", CountRows(q().OrderBy(Col("id").Asc())), unchanged, nil},
		{"qualified_column", CountRows(q().OrderBy(Col("t", "id").Asc())), unchanged, nil},
		{"identifier_parts", CountRows(q().OrderBy(Col("t", "id").Asc())), unchanged, nil},
		{"ordinal", CountRows(q().OrderBy(Ordinal(1).Asc())), unchanged, nil},
		{"bound_value", CountRows(q().OrderBy(Param(1).Asc())), unchanged, nil},
		{"parenthesized_column", CountRows(q().OrderBy(Col("id").Parenthesized().Asc())), unchanged, nil},
		{"parenthesized_ordinal", CountRows(q().OrderBy(Ordinal(1).Parenthesized().Asc())), unchanged, nil},
		{"base_prefixed_ordinal", CountRows(q().OrderBy(LiteralNumeric("0x1").Asc())), unchanged, nil},
		{
			"doubly_parenthesized_bound_value",
			CountRows(q().OrderBy(Param(1).Parenthesized().Parenthesized().Asc())),
			unchanged,
			nil,
		},

		{"set_returning_function", CountRows(q().OrderBy(Call("unnest", Col("tags")).Asc())),
			`SELECT count(*) FROM (SELECT "id", "unnest"("tags") AS "qs_order_1" FROM "t") AS "matching"`, nil},
		{"aggregate", CountRows(Select(LiteralInt(1)).From("t").OrderBy(CountAll().Desc())),
			`SELECT count(*) FROM (SELECT 1, count(*) AS "qs_order_1" FROM "t") AS "matching"`, nil},
		{
			"window_over_aggregate",
			CountRows(Select(LiteralInt(1)).From("t").OrderBy(Sum(CountAll()).Over(Window()).Asc())),
			`SELECT count(*) FROM (SELECT 1, sum(count(*)) OVER () AS "qs_order_1" FROM "t") AS "matching"`,
			nil,
		},
		{"outer_level_aggregate", CountRows(q().OrderBy(Scalar(Select(Max(Col("t", "id")))).Asc())),
			`SELECT count(*) FROM (SELECT "id", (SELECT max("t"."id")) AS "qs_order_1" FROM "t") AS "matching"`, nil},
		{"operator", CountRows(q().OrderBy(Col("a").Add(Col("b")).Asc())),
			`SELECT count(*) FROM (SELECT "id", ("a" + "b") AS "qs_order_1" FROM "t") AS "matching"`, nil},
		{"trusted_sql", CountRows(q().OrderBy(UnsafeSQL("random()").Asc())),
			`SELECT count(*) FROM (SELECT "id", random() AS "qs_order_1" FROM "t") AS "matching"`, nil},
		{"parenthesized_call", CountRows(q().OrderBy(Lower(Col("name")).Parenthesized().Asc())),
			`SELECT count(*) FROM (SELECT "id", (lower("name")) AS "qs_order_1" FROM "t") AS "matching"`, nil},

		{
			"after_projection_in_order_without_direction",
			CountRows(
				Select(Col("a"), Col("b")).
					From("t").
					OrderBy(
						Lower(Col("c")).Desc().NullsFirst(), Col("id").Asc(), Lower(Col("d")).Using(
							"<",
						), Call("upper", Col("e")).Asc()),
			),
			`SELECT count(*) FROM (SELECT "a", "b", lower("c") AS "qs_order_1", lower("d") AS "qs_order_3", "upper"("e") AS "qs_order_4" FROM "t") AS "matching"`,
			nil,
		},
		{
			"aliases_keep_two_digit_positions",
			CountRows(
				Select(Col("a")).
					From("t").
					OrderBy(
						Lower(Col("c1")).Asc(), Col("k2").Asc(), Col("k3").Asc(), Col("k4").Asc(), Lower(
							Col("c5"),
						).Asc(),
						Col("k6").Asc(), Col("k7").Asc(), Col("k8").Asc(), Col("k9").Asc(), Lower(
							Col("c10"),
						).Asc()),
			),
			`SELECT count(*) FROM (SELECT "a", lower("c1") AS "qs_order_1", lower("c5") AS "qs_order_5", lower("c10") AS "qs_order_10" FROM "t") AS "matching"`,
			nil,
		},
		{
			"grouped_aggregate",
			CountRows(Select(Col("a")).From("t").GroupBy("a").OrderBy(Sum(Col("b")).Desc())),
			`SELECT count(*) FROM (SELECT "a", sum("b") AS "qs_order_1" FROM "t" GROUP BY "a") AS "matching"`,
			nil,
		},
		{
			"group_by_ordinal_keeps_position",
			CountRows(
				Select(Col("a")).From("t").GroupBy(Ordinal(1)).OrderBy(Sum(Col("b")).Desc()),
			),
			`SELECT count(*) FROM (SELECT "a", sum("b") AS "qs_order_1" FROM "t" GROUP BY 1) AS "matching"`,
			nil,
		},
		{
			"no_columns",
			CountRows(SelectNoColumns().From("t").OrderBy(Col("id").Asc(), Lower(Col("x")).Asc())),
			`SELECT count(*) FROM (SELECT lower("x") AS "qs_order_2" FROM "t") AS "matching"`,
			nil,
		},
		{"no_columns_only_names", CountRows(SelectNoColumns().From("t").OrderBy(Col("id").Asc())),
			`SELECT count(*) FROM (SELECT  FROM "t") AS "matching"`, nil},
		// SELECT DISTINCT requires ORDER BY expressions in the projection already.
		{
			"distinct",
			CountRows(Select(Lower(Col("a"))).Distinct().From("t").OrderBy(Lower(Col("a")).Asc())),
			`SELECT count(*) FROM (SELECT DISTINCT lower("a") FROM "t") AS "matching"`,
			nil,
		},
		{
			"distinct_on",
			CountRows(
				Select(Col("id")).
					DistinctOn(Col("a")).
					From("t").
					OrderBy(Col("a").Asc(), Lower(Col("c")).Asc()),
			),
			`SELECT count(*) FROM (SELECT DISTINCT ON ("a") "id", lower("c") AS "qs_order_2" FROM "t") AS "matching"`,
			nil,
		},
		{
			"window_with_lock",
			CountRows(q().OrderBy(CountAll().Over(Window()).Asc()).Lock(ForUpdate())),
			`SELECT count(*) FROM (SELECT "id", count(*) OVER () AS "qs_order_1" FROM "t") AS "matching"`,
			nil,
		},
		{
			"window_stays_in_count_scope",
			Select(Scalar(CountRows(q().OrderBy(CountAll().Over(Window()).Asc())))).
				From("u").
				Lock(ForUpdate()),
			`SELECT (SELECT count(*) FROM (SELECT "id", count(*) OVER () AS "qs_order_1" FROM "t") AS "matching") FROM "u" FOR UPDATE`,
			nil,
		},
		{
			"nested_order_stays",
			CountRows(Select(Col("id")).From(inner.As("s")).OrderBy(Lower(Col("x")).Asc())),
			`SELECT count(*) FROM (SELECT "id", lower("x") AS "qs_order_1" FROM (SELECT "id" FROM "u" ORDER BY lower("y") ASC LIMIT $1) AS "s") AS "matching"`,
			[]any{3},
		},
	})
}

func TestCountRowsBindsOrderExpressionsBeforeWhere(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).
		From("t").
		Where(Col("tenant").Eq(Param(7))).
		OrderBy(Call("abs", Col("id").Sub(Param(9))).Asc(), Col("t", "id").Asc()).
		SeekAfter(1, 2).
		Limit(5)
	checkSQL(t, CountRows(q), `SELECT count(*) FROM (SELECT "id", "abs"(("id" - $1)) AS "qs_order_1" FROM "t" WHERE ("tenant" = $2)) AS "matching"`, 9, 7)
	checkSQL(t, q, `SELECT "id" FROM "t" WHERE ("tenant" = $1) AND (("abs"(("id" - $2)), "t"."id") > ($3, $4)) ORDER BY "abs"(("id" - $5)) ASC, "t"."id" ASC LIMIT $6`, 7, 9, 1, 2, 9, 5)
}

func TestCountRowsCountsTheQueryItWasGiven(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).From("t").Where(Col("a").Eq(Param(1)))
	count := CountRows(q)
	page := q.Where(Col("b").Eq(Param(2))).OrderBy(Col("t", "id").Asc()).SeekAfter(3).Limit(4)

	checkSQL(t, count, `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1)) AS "matching"`, 1)
	checkSQL(t, CountRows(page), `SELECT count(*) FROM (SELECT "id" FROM "t" WHERE ("a" = $1) AND ("b" = $2)) AS "matching"`, 1, 2)
}

func TestCountRowsFailures(t *testing.T) {
	t.Parallel()
	checkError(t, CountRows(SelectBuilder{}), ErrInvalid)

	modifying := Select(Col("id")).
		With(CTE("d", DeleteFrom("t").AllRows().Returning(Col("id")))).
		From("d")
	checkError(t, CountRows(modifying), ErrInvalid)
	_, _, countErr := CountRows(modifying).ToSQL()
	_, _, scalarErr := Select(Scalar(modifying)).ToSQL()
	count, _ := errors.AsType[*RenderError](countErr)
	scalar, _ := errors.AsType[*RenderError](scalarErr)
	if scalar == nil || count.Clause != scalar.Clause || count.Detail != scalar.Detail {
		t.Fatalf("CountRows error %v differs from subquery error %v", countErr, scalarErr)
	}
}
