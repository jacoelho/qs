package qs

import "testing"

func TestSetOperationSliceForms(t *testing.T) {
	t.Parallel()
	a := Select(Col("id")).From("a").Where(Col("x").Eq(Param(1)))
	b := Select(Col("id")).From("b")
	c := Select(Col("id")).From("c").Where(Col("y").Eq(Param(2)))
	runCases(t, []renderCase{
		{"several_match_the_variadic_form", UnionSlice([]Rowset{a, b, c}),
			`((SELECT "id" FROM "a" WHERE ("x" = $1)) UNION (SELECT "id" FROM "b")) UNION (SELECT "id" FROM "c" WHERE ("y" = $2))`, []any{1, 2}},
		{"two", ExceptAllSlice([]Rowset{a, b}),
			`(SELECT "id" FROM "a" WHERE ("x" = $1)) EXCEPT ALL (SELECT "id" FROM "b")`, []any{1}},
		{"one_is_the_rowset_itself", UnionSlice([]Rowset{a}),
			`(SELECT "id" FROM "a" WHERE ("x" = $1))`, []any{1}},
		{
			"one_keeps_the_builder_clauses",
			IntersectSlice([]Rowset{a}).OrderBy(Col("id").Desc()).Limit(3),
			`(SELECT "id" FROM "a" WHERE ("x" = $1)) ORDER BY "id" DESC LIMIT $2`,
			[]any{1, 3},
		},
		{"one_as_a_branch", UnionAll(UnionSlice([]Rowset{a}), b),
			`((SELECT "id" FROM "a" WHERE ("x" = $1))) UNION ALL (SELECT "id" FROM "b")`, []any{1}},
		{"one_as_a_subquery", Select(Star()).FromExpr(Subquery(UnionSlice([]Rowset{b}), "s")),
			`SELECT * FROM ((SELECT "id" FROM "b")) AS "s"`, nil},
	})
	for name, operator := range map[string]func([]Rowset) SetBuilder{
		"UNION": UnionSlice, "UNION ALL": UnionAllSlice, "INTERSECT": IntersectSlice,
		"INTERSECT ALL": IntersectAllSlice, "EXCEPT": ExceptSlice, "EXCEPT ALL": ExceptAllSlice,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, operator([]Rowset{b, b}), `(SELECT "id" FROM "b") `+name+` (SELECT "id" FROM "b")`)
			checkError(t, operator(nil), ErrInvalid)
			checkError(t, operator([]Rowset{}), ErrInvalid)
			checkError(t, operator([]Rowset{nil}), ErrInvalid)
		})
	}
}

func TestSetOperationSliceDoesNotKeepTheList(t *testing.T) {
	t.Parallel()
	sets := []Rowset{Select(Col("id")).From("a"), Select(Col("id")).From("b")}
	q := UnionSlice(sets)
	sets[1] = Select(Col("id")).From("changed")
	checkSQL(t, q, `(SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")`)
}

func TestMutationWhereSlice(t *testing.T) {
	t.Parallel()
	conditions := []Condition{Col("tenant").Eq(Param("acme")), Col("id").Eq(Param(7))}
	update := Update("t").Set(Set("n", Value(1))).WhereSlice(conditions)
	remove := DeleteFrom("t").WhereSlice(conditions)
	conditions[1] = Col("id").Eq(Param(99))
	checkSQL(t, update, `UPDATE "t" SET "n" = $1 WHERE ("tenant" = $2) AND ("id" = $3)`, 1, "acme", 7)
	checkSQL(t, remove, `DELETE FROM "t" WHERE ("tenant" = $1) AND ("id" = $2)`, "acme", 7)

	// A filter with no entries must not reach every row.
	for name, statement := range map[string]Statement{
		"update nil":         Update("t").Set(Set("n", Value(1))).WhereSlice(nil),
		"update empty":       Update("t").Set(Set("n", Value(1))).WhereSlice([]Condition{}),
		"update always true": Update("t").Set(Set("n", Value(1))).WhereSlice([]Condition{And()}),
		"delete nil":         DeleteFrom("t").WhereSlice(nil),
		"delete empty":       DeleteFrom("t").WhereSlice([]Condition{}),
		"delete always true": DeleteFrom("t").WhereSlice([]Condition{True()}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}

// A condition is usable where a value is, without converting it by hand.
func TestConditionAsValue(t *testing.T) {
	t.Parallel()
	adult := Col("age").Gte(Param(18))
	checkSQL(t,
		Select(Col("id"), adult.As("adult")).
			From("t").
			Where(Col("active").Condition(), Param(true).Condition()).
			OrderBy(adult.Desc(), Col("deleted_at").IsNull().Asc()),
		`SELECT "id", ("age" >= $1) AS "adult" FROM "t" WHERE "active" AND $2 ORDER BY ("age" >= $3) DESC, ("deleted_at" IS NULL) ASC`,
		18, true, 18)
}

func TestFunctionHelpers(t *testing.T) {
	t.Parallel()
	checkSQL(t,
		Select(
			ConcatWS(Param(", "), Col("city"), Col("country")),
			GenRandomUUID(),
			ClockTimestamp(),
			ConcatWS(Param("-")),
		).
			From("t").
			Where(StartsWith(Col("name"), Param("a%"))),
		`SELECT concat_ws($1, "city", "country"), gen_random_uuid(), clock_timestamp(), concat_ws($2) FROM "t" WHERE starts_with("name", $3)`,
		", ", "-", "a%")
}
