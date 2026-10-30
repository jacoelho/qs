package qs

import "testing"

// trustedDisjunction is a trusted condition whose OR binds looser than the AND
// that joins conditions, so splicing it unparenthesized changes the filter.
func trustedDisjunction() Condition { return AsCondition(UnsafeSQL("a = 1 OR b = 2")) }

func TestTrustedConditionsAreUnitsWhenCombined(t *testing.T) {
	t.Parallel()
	raw := trustedDisjunction
	fragment := AsCondition(Fragment(Col("m"), UnsafeSQL(" ? "), Param("k")))
	x := Eq("x", 1)
	sel := func() *SelectBuilder { return Select(Col("id")).From("t") }
	runCases(t, []renderCase{
		{"where_first", sel().Where(raw(), x),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("x" = $1)`, []any{1}},
		{"where_last", sel().Where(x, raw()),
			`SELECT "id" FROM "t" WHERE ("x" = $1) AND (a = 1 OR b = 2)`, []any{1}},
		{"where_fragment", sel().Where(raw(), fragment),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("m" ? $1)`, []any{"k"}},
		{"where_separate_calls", sel().Where(raw()).Where(x),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("x" = $1)`, []any{1}},
		{"cursor_uniform", sel().Where(raw()).OrderBy(Asc("id")).SeekAfter(5),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("id" > $1) ORDER BY "id" ASC`, []any{5}},
		{"cursor_mixed", sel().Where(raw()).OrderBy(Asc("a"), Desc("id")).SeekAfter(1, 5),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND (("a" >= $1) AND (("a" > $2) OR (("a" = $3) AND ("id" < $4)))) ORDER BY "a" ASC, "id" DESC`, []any{1, 1, 1, 5}},
		{"cursor_before_where", sel().OrderBy(Asc("id")).SeekAfter(5).Where(raw()),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("id" > $1) ORDER BY "id" ASC`, []any{5}},
		{"and_first", sel().Where(And(raw(), x)),
			`SELECT "id" FROM "t" WHERE ((a = 1 OR b = 2) AND ("x" = $1))`, []any{1}},
		{"and_last", sel().Where(And(x, raw())),
			`SELECT "id" FROM "t" WHERE (("x" = $1) AND (a = 1 OR b = 2))`, []any{1}},
		{"and_method", sel().Where(raw().And(x)),
			`SELECT "id" FROM "t" WHERE ((a = 1 OR b = 2) AND ("x" = $1))`, []any{1}},
		{"not", sel().Where(Not(raw())),
			`SELECT "id" FROM "t" WHERE (NOT (a = 1 OR b = 2))`, nil},
		{"not_not", sel().Where(Not(Not(raw()))),
			`SELECT "id" FROM "t" WHERE (NOT (NOT (a = 1 OR b = 2)))`, nil},
		{"not_single_member_and", sel().Where(Not(And(raw()))),
			`SELECT "id" FROM "t" WHERE (NOT (a = 1 OR b = 2))`, nil},
	})
}

func TestTrustedConditionsAreUnitsAtEveryConditionList(t *testing.T) {
	t.Parallel()
	raw := trustedDisjunction
	x := Eq("x", 1)
	insert := func() *InsertRows { return InsertInto("t").Columns("id").Values(Value(1)) }
	runCases(t, []renderCase{
		{"having", Select(Col("a")).From("t").GroupBy("a").Having(raw(), x),
			`SELECT "a" FROM "t" GROUP BY "a" HAVING (a = 1 OR b = 2) AND ("x" = $1)`, []any{1}},
		{"join_on", Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).On(raw(), x)),
			`SELECT * FROM ("a" JOIN "b" ON (a = 1 OR b = 2) AND ("x" = $1))`, []any{1}},
		{"aggregate_filter", Select(CountAll().Filter(raw(), x)).From("t"),
			`SELECT count(*) FILTER (WHERE (a = 1 OR b = 2) AND ("x" = $1)) FROM "t"`, []any{1}},
		{"update_where", Update("t").Set(Set("y", Value(2))).Where(raw(), x),
			`UPDATE "t" SET "y" = $1 WHERE (a = 1 OR b = 2) AND ("x" = $2)`, []any{2, 1}},
		{"delete_where", DeleteFrom("t").Where(raw(), x),
			`DELETE FROM "t" WHERE (a = 1 OR b = 2) AND ("x" = $1)`, []any{1}},
		{"conflict_target_where", insert().OnConflict(ConflictColumns("id").TargetWhere(raw(), x).DoNothing()),
			`INSERT INTO "t" ("id") VALUES ($1) ON CONFLICT ("id") WHERE (a = 1 OR b = 2) AND ("x" = $2) DO NOTHING`, []any{1, 1}},
		{"conflict_update_where", insert().OnConflict(ConflictColumns("id").DoUpdate(Set("y", Value(2))).Where(raw(), x)),
			`INSERT INTO "t" ("id") VALUES ($1) ON CONFLICT ("id") DO UPDATE SET "y" = $2 WHERE (a = 1 OR b = 2) AND ("x" = $3)`, []any{1, 2, 1}},
		{"merge_on", MergeInto("t").Using(Table("s")).On(raw(), x).When(Matched().ThenDelete()),
			`MERGE INTO "t" USING "s" ON (a = 1 OR b = 2) AND ("x" = $1) WHEN MATCHED THEN DELETE`, []any{1}},
		{"merge_when", MergeInto("t").Using(Table("s")).On(True()).When(Matched().And(raw(), x).ThenDelete()),
			`MERGE INTO "t" USING "s" ON TRUE WHEN MATCHED AND (a = 1 OR b = 2) AND ("x" = $1) THEN DELETE`, []any{1}},
	})
}

// A lone trusted condition renders verbatim so that SQL valid only as a whole
// clause body keeps working.
func TestSingleTrustedConditionStaysVerbatim(t *testing.T) {
	t.Parallel()
	raw := trustedDisjunction
	sel := func() *SelectBuilder { return Select(Col("id")).From("t") }
	runCases(t, []renderCase{
		{"where", sel().Where(raw()),
			`SELECT "id" FROM "t" WHERE a = 1 OR b = 2`, nil},
		{"and_single_member", sel().Where(And(raw())),
			`SELECT "id" FROM "t" WHERE a = 1 OR b = 2`, nil},
		{"or_single_member", sel().Where(Or(raw())),
			`SELECT "id" FROM "t" WHERE a = 1 OR b = 2`, nil},
		{"count_omits_cursor", CountRows(sel().Where(raw()).OrderBy(Asc("id")).SeekAfter(5)),
			`SELECT count(*) FROM (SELECT "id" FROM "t" WHERE a = 1 OR b = 2) AS "matching"`, nil},
		{"case_when", Select(Case().When(raw(), Param(1)).End()).From("t"),
			`SELECT CASE WHEN a = 1 OR b = 2 THEN $1 END FROM "t"`, []any{1}},
		{"current_of", Update("t").Set(Set("y", Value(2))).Where(AsCondition(UnsafeSQL("CURRENT OF c"))),
			`UPDATE "t" SET "y" = $1 WHERE CURRENT OF c`, []any{2}},
	})
}

func TestParenthesizedTrustedConditionIsNotRewrapped(t *testing.T) {
	t.Parallel()
	parenthesized := AsCondition(UnsafeSQL("a = 1 OR b = 2").Parenthesized())
	runCases(t, []renderCase{
		{"lone", Select(Col("id")).From("t").Where(parenthesized),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2)`, nil},
		{"combined", Select(Col("id")).From("t").Where(parenthesized, Eq("x", 1)),
			`SELECT "id" FROM "t" WHERE (a = 1 OR b = 2) AND ("x" = $1)`, []any{1}},
	})
}

// Other operators render trusted operands verbatim: wrapping them would turn
// valid SQL such as = ANY(...) into a parenthesized operand that PostgreSQL
// rejects.
func TestTrustedOperandsOfOtherOperatorsStayVerbatim(t *testing.T) {
	t.Parallel()
	runCases(t, []renderCase{
		{"raw", Select(Col("id")).From("t").Where(Col("x").EqExpr(UnsafeSQL("ANY(ARRAY[1,2])"))),
			`SELECT "id" FROM "t" WHERE ("x" = ANY(ARRAY[1,2]))`, nil},
		{"fragment", Select(Col("id")).From("t").Where(Col("x").EqExpr(Fragment(UnsafeSQL("ANY("), Param([]int{1}), UnsafeSQL(")")))),
			`SELECT "id" FROM "t" WHERE ("x" = ANY($1))`, []any{[]int{1}}},
	})
}

// A trusted ORDER BY term becomes a comparison operand in the cursor, where
// NOT archived > $1 would parse as NOT (archived > $1).
func TestTrustedOrderKeysAreUnitsInCursor(t *testing.T) {
	t.Parallel()
	raw := UnsafeSQL("NOT archived")
	sel := func() *SelectBuilder { return Select(Col("id")).From("t") }
	runCases(t, []renderCase{
		{"single_asc", sel().OrderBy(raw.Asc()).SeekAfter(true),
			`SELECT "id" FROM "t" WHERE ((NOT archived) > $1) ORDER BY NOT archived ASC`, []any{true}},
		{"single_desc", sel().OrderBy(raw.Desc()).SeekAfter(true),
			`SELECT "id" FROM "t" WHERE ((NOT archived) < $1) ORDER BY NOT archived DESC`, []any{true}},
		{"mixed_leading", sel().OrderBy(raw.Asc(), Desc("id")).SeekAfter(true, 2),
			`SELECT "id" FROM "t" WHERE (((NOT archived) >= $1) AND (((NOT archived) > $2) OR (((NOT archived) = $3) AND ("id" < $4)))) ORDER BY NOT archived ASC, "id" DESC`,
			[]any{true, true, true, 2}},
		{"mixed_trailing", sel().OrderBy(Asc("a"), raw.Desc()).SeekAfter(1, true),
			`SELECT "id" FROM "t" WHERE (("a" >= $1) AND (("a" > $2) OR (("a" = $3) AND ((NOT archived) < $4)))) ORDER BY "a" ASC, NOT archived DESC`,
			[]any{1, 1, 1, true}},
		{"mixed_middle", sel().OrderBy(Asc("a"), raw.Desc(), Asc("id")).SeekAfter(1, true, 3),
			`SELECT "id" FROM "t" WHERE (("a" >= $1) AND (("a" > $2) OR (("a" = $3) AND ((NOT archived) < $4)) OR (("a" = $5) AND ((NOT archived) = $6) AND ("id" > $7)))) ORDER BY "a" ASC, NOT archived DESC, "id" ASC`,
			[]any{1, 1, 1, true, 1, true, 3}},
		{"fragment", sel().OrderBy(Fragment(UnsafeSQL("NOT "), Col("archived")).Asc()).SeekAfter(true),
			`SELECT "id" FROM "t" WHERE ((NOT "archived") > $1) ORDER BY NOT "archived" ASC`, []any{true}},
		{"disjunction", sel().OrderBy(UnsafeSQL("a OR b").Asc()).SeekAfter(true),
			`SELECT "id" FROM "t" WHERE ((a OR b) > $1) ORDER BY a OR b ASC`, []any{true}},
		{"uniform_row_unchanged", sel().OrderBy(raw.Asc(), Asc("id")).SeekAfter(true, 2),
			`SELECT "id" FROM "t" WHERE ((NOT archived, "id") > ($1, $2)) ORDER BY NOT archived ASC, "id" ASC`, []any{true, 2}},
		{"parenthesized_not_rewrapped", sel().OrderBy(raw.Parenthesized().Asc()).SeekAfter(true),
			`SELECT "id" FROM "t" WHERE ((NOT archived) > $1) ORDER BY (NOT archived) ASC`, []any{true}},
		{"structural_unchanged", sel().OrderBy(Lower(Col("name")).Asc(), Desc("id")).SeekAfter("ann", 4),
			`SELECT "id" FROM "t" WHERE ((lower("name") >= $1) AND ((lower("name") > $2) OR ((lower("name") = $3) AND ("id" < $4)))) ORDER BY lower("name") ASC, "id" DESC`,
			[]any{"ann", "ann", "ann", 4}},
	})
}
