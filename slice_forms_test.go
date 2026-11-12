package qs

import "testing"

func TestSetOperationsTakeARowsetList(t *testing.T) {
	t.Parallel()
	a := Select(Col("id")).From("a").Where(Col("x").Eq(Param(1)))
	b := Select(Col("id")).From("b")
	c := Select(Col("id")).From("c").Where(Col("y").Eq(Param(2)))
	runCases(t, []renderCase{
		{"several_match_the_variadic_form", Union([]Rowset{a, b, c}...),
			`((SELECT "id" FROM "a" WHERE ("x" = $1)) UNION (SELECT "id" FROM "b")) UNION (SELECT "id" FROM "c" WHERE ("y" = $2))`, []any{1, 2}},
		{"two", ExceptAll([]Rowset{a, b}...),
			`(SELECT "id" FROM "a" WHERE ("x" = $1)) EXCEPT ALL (SELECT "id" FROM "b")`, []any{1}},
		{"one_is_the_rowset_itself", Union([]Rowset{a}...),
			`(SELECT "id" FROM "a" WHERE ("x" = $1))`, []any{1}},
		{
			"one_keeps_the_builder_clauses",
			Intersect([]Rowset{a}...).OrderBy(Col("id").Desc()).Limit(3),
			`(SELECT "id" FROM "a" WHERE ("x" = $1)) ORDER BY "id" DESC LIMIT $2`,
			[]any{1, 3},
		},
		{"one_as_a_branch", UnionAll(Union([]Rowset{a}...), b),
			`((SELECT "id" FROM "a" WHERE ("x" = $1))) UNION ALL (SELECT "id" FROM "b")`, []any{1}},
		{"one_as_a_subquery", Select(Star()).From(Subquery(Union([]Rowset{b}...), "s")),
			`SELECT * FROM ((SELECT "id" FROM "b")) AS "s"`, nil},
	})
	for name, operator := range map[string]func(...Rowset) SetBuilder{
		"UNION": Union, "UNION ALL": UnionAll, "INTERSECT": Intersect,
		"INTERSECT ALL": IntersectAll, "EXCEPT": Except, "EXCEPT ALL": ExceptAll,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, operator(b, b), `(SELECT "id" FROM "b") `+name+` (SELECT "id" FROM "b")`)
			checkSQL(t, operator([]Rowset{b, b}...), `(SELECT "id" FROM "b") `+name+` (SELECT "id" FROM "b")`)
			checkError(t, operator(), ErrInvalid)
			checkError(t, operator([]Rowset{}...), ErrInvalid)
			checkError(t, operator(nil), ErrInvalid)
		})
	}
}

func TestSetOperationDoesNotKeepTheList(t *testing.T) {
	t.Parallel()
	sets := []Rowset{Select(Col("id")).From("a"), Select(Col("id")).From("b")}
	q := Union(sets...)
	sets[1] = Select(Col("id")).From("changed")
	checkSQL(t, q, `(SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")`)
}

func TestInsertSetTakesAnAssignmentList(t *testing.T) {
	t.Parallel()
	assignments := []Assignment{Set("name", Value("Ada")), SetDefault("created_at")}
	selected := InsertInto("people").Set(assignments)
	appended := InsertInto("people").Set(Set("id", Value(7))).Set(assignments)
	assignments[0] = Set("changed", Value("changed"))
	checkSQL(t, selected, `INSERT INTO "people" ("name", "created_at") VALUES ($1, DEFAULT)`, "Ada")
	checkSQL(t, appended, `INSERT INTO "people" ("id", "name", "created_at") VALUES ($1, $2, DEFAULT)`, 7, "Ada")
	checkSQL(t, selected.Set(Set("id", Value(7))), `INSERT INTO "people" ("name", "created_at", "id") VALUES ($1, DEFAULT, $2)`, "Ada", 7)

	// The method appends, so an empty list leaves earlier assignments in place and
	// only an INSERT with none at all is refused.
	for _, empty := range [][]Assignment{nil, {}} {
		checkError(t, InsertInto("people").Set(empty), ErrInvalid)
		checkSQL(t, InsertInto("people").Set(Set("id", Value(7))).Set(empty), `INSERT INTO "people" ("id") VALUES ($1)`, 7)
		checkSQL(t, InsertInto("people").Set(empty).Set(Set("id", Value(7))), `INSERT INTO "people" ("id") VALUES ($1)`, 7)
	}
}

// A builder copied before Set must not see what a sibling appended.
func TestInsertSetListBranchesShareNothing(t *testing.T) {
	t.Parallel()
	base := InsertInto("people").Set(Set("id", Value(1)), Set("a", Value(2)), Set("b", Value(3)))
	left := base.Set([]Assignment{Set("left", Value("l"))})
	right := base.Set([]Assignment{Set("right", Value("r"))})
	checkSQL(t, left, `INSERT INTO "people" ("id", "a", "b", "left") VALUES ($1, $2, $3, $4)`, 1, 2, 3, "l")
	checkSQL(t, right, `INSERT INTO "people" ("id", "a", "b", "right") VALUES ($1, $2, $3, $4)`, 1, 2, 3, "r")
}

func TestOrderColumnNames(t *testing.T) {
	t.Parallel()
	names := []string{"last_name", "u.first"}
	asc := AscCols(names...)
	names[0] = "changed"
	runCases(t, []renderCase{
		{"ascending_names_are_unqualified", Select(Col("id")).From("users").OrderBy(asc...),
			`SELECT "id" FROM "users" ORDER BY "last_name" ASC, "u.first" ASC`, nil},
		{"descending", Select(Col("id")).From("users").OrderBy(DescCols("score", "id")...),
			`SELECT "id" FROM "users" ORDER BY "score" DESC, "id" DESC`, nil},
		{"mixed_through_a_second_call", Select(Col("id")).From("users").OrderBy(AscCols("a", "b")...).OrderBy(Col("u", "c").Desc().NullsLast()),
			`SELECT "id" FROM "users" ORDER BY "a" ASC, "b" ASC, "u"."c" DESC NULLS LAST`, nil},
		{"terms_keep_order_modifiers", Select(Col("id")).From("users").OrderBy(AscCols("a")[0].NullsFirst().Reverse()),
			`SELECT "id" FROM "users" ORDER BY "a" DESC NULLS LAST`, nil},
		{"no_names_add_no_clause", Select(Col("id")).From("users").OrderBy(AscCols()...).OrderBy(DescCols()...),
			`SELECT "id" FROM "users"`, nil},
		{"quote_in_name", Select(Col("id")).From("users").OrderBy(AscCols(`a" DESC; --`)...),
			`SELECT "id" FROM "users" ORDER BY "a"" DESC; --" ASC`, nil},
		{"window_and_aggregate_order", Select(ArrayAgg(Col("v")).OrderBy(DescCols("n")...), RowNumber().Over(Window().OrderBy(AscCols("a", "b")...))).From("t"),
			`SELECT array_agg("v" ORDER BY "n" DESC), row_number() OVER (ORDER BY "a" ASC, "b" ASC) FROM "t"`, nil},
	})
	checkError(t, Select(Col("id")).From("users").OrderBy(AscCols("")...), ErrInvalid)
}

// SELECT takes a run-time list through its variadic Where, and none is no filter.
func TestSelectWhereTakesAConditionList(t *testing.T) {
	t.Parallel()
	conditions := []Condition{Col("tenant").Eq(Param("acme")), Col("id").Gt(Param(7))}
	q := Select(Col("id")).From("t").Where(conditions...)
	conditions[1] = Col("id").Eq(Param(99))
	checkSQL(t, q, `SELECT "id" FROM "t" WHERE ("tenant" = $1) AND ("id" > $2)`, "acme", 7)
	checkSQL(t, Select(Col("id")).From("t").Where([]Condition(nil)...).Where(), `SELECT "id" FROM "t"`)
}

func TestMutationWhereTakesAConditionList(t *testing.T) {
	t.Parallel()
	conditions := []Condition{Col("tenant").Eq(Param("acme")), Col("id").Eq(Param(7))}
	update := Update("t").Set(Set("n", Value(1))).Where(And(conditions...))
	remove := DeleteFrom("t").Where(And(conditions...))
	conditions[1] = Col("id").Eq(Param(99))
	checkSQL(t, update, `UPDATE "t" SET "n" = $1 WHERE (("tenant" = $2) AND ("id" = $3))`, 1, "acme", 7)
	checkSQL(t, remove, `DELETE FROM "t" WHERE (("tenant" = $1) AND ("id" = $2))`, "acme", 7)

	// A filter with no entries must not reach every row.
	for name, statement := range map[string]Statement{
		"update nil":         Update("t").Set(Set("n", Value(1))).Where(And(nil...)),
		"update empty":       Update("t").Set(Set("n", Value(1))).Where(And([]Condition{}...)),
		"update always true": Update("t").Set(Set("n", Value(1))).Where(And([]Condition{And()}...)),
		"delete nil":         DeleteFrom("t").Where(And(nil...)),
		"delete empty":       DeleteFrom("t").Where(And([]Condition{}...)),
		"delete always true": DeleteFrom("t").Where(And([]Condition{True()}...)),
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

// An element-or-list method takes a run-time list bare, optionally followed by
// more elements, and copies the list.
func TestListFormsTakeAListThenElements(t *testing.T) {
	t.Parallel()
	list := []Assignment{Set("a", Value(1)), Set("b", Value(2))}
	names := []string{"tenant", "id"}
	arrays := []ArrayColumn{NamedArray("id", ArrayParam([]int64{1}, TypeInt8))}

	update := Update("t").Set(list, Set("c", Value(3))).AllRows()
	conflict := InsertInto("t").
		Columns("tenant", "id", "a").
		Values(Value("x"), Value(7), Value(0)).
		OnConflict(ConflictColumns(names, "a").DoUpdate(list, SetDefault("c")))
	merge := MergeInto("t").
		Using(Table("s")).
		On(True()).
		When(Matched().ThenUpdate(list, SetDefault("c")))
	unnest := Select(Star()).From(UnnestAs("u", arrays, NamedArray("name", ArrayParam([]string{"n"}, TypeText))))

	list[0], names[0], arrays[0] = Set("changed", Value(9)), "changed", NamedArray("changed", ArrayParam([]int64{9}, TypeInt8))

	checkSQL(t, update, `UPDATE "t" SET "a" = $1, "b" = $2, "c" = $3`, 1, 2, 3)
	checkSQL(t, conflict,
		`INSERT INTO "t" ("tenant", "id", "a") VALUES ($1, $2, $3) ON CONFLICT ("tenant", "id", "a") DO UPDATE SET "a" = $4, "b" = $5, "c" = DEFAULT`,
		"x", 7, 0, 1, 2)
	checkSQL(t, merge,
		`MERGE INTO "t" USING "s" ON TRUE WHEN MATCHED THEN UPDATE SET "a" = $1, "b" = $2, "c" = DEFAULT`, 1, 2)
	checkSQL(t, unnest,
		`SELECT * FROM unnest(($1)::bigint[], ($2)::text[]) AS "u" ("id", "name")`, []int64{1}, []string{"n"})

	for name, statement := range map[string]Statement{
		"update":   Update("t").Set([]Assignment{}).AllRows(),
		"conflict": InsertInto("t").Set(Set("a", Value(1))).OnConflict(ConflictColumns("a").DoUpdate([]Assignment(nil))),
		"columns":  InsertInto("t").Set(Set("a", Value(1))).OnConflict(ConflictColumns([]string{}).DoNothing()),
		"merge":    MergeInto("t").Using(Table("s")).On(True()).When(Matched().ThenUpdate([]Assignment{})),
		"unnest":   Select(Star()).From(UnnestAs("u", []ArrayColumn{})),
		"arrays":   InsertInto("t").FromArrays([]ArrayColumn(nil)),
		"index":    InsertInto("t").Set(Set("a", Value(1))).OnConflict(ConflictIndex([]IndexElement{}).DoNothing()),
		"insert":   MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsert([]Assignment{})),
	} {
		t.Run("empty "+name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}

// Sibling builders that append to one UPDATE assignment list share nothing.
func TestUpdateSetListBranchesShareNothing(t *testing.T) {
	t.Parallel()
	base := Update("t").Set(Set("a", Value(1)), Set("b", Value(2)), Set("c", Value(3)))
	left := base.Set([]Assignment{Set("left", Value("l"))}).AllRows()
	right := base.Set([]Assignment{Set("right", Value("r"))}).AllRows()
	checkSQL(t, left, `UPDATE "t" SET "a" = $1, "b" = $2, "c" = $3, "left" = $4`, 1, 2, 3, "l")
	checkSQL(t, right, `UPDATE "t" SET "a" = $1, "b" = $2, "c" = $3, "right" = $4`, 1, 2, 3, "r")
}

// A clause that names a table takes the name or a relation, with one meaning.
func TestTablesByNameOrRelation(t *testing.T) {
	t.Parallel()
	users, teams := Table("app", "users").As("u"), Table("teams").As("t")
	on := Col("t", "id").Eq(Col("u", "team_id"))
	runCases(t, []renderCase{
		{"select_from_relations", Select("id").From(users, teams),
			`SELECT "id" FROM "app"."users" AS "u", "teams" AS "t"`, nil},
		{"select_from_names", Select("id").From("users", "teams"),
			`SELECT "id" FROM "users", "teams"`, nil},
		{"from_replaces", Select("id").From("a").From(users),
			`SELECT "id" FROM "app"."users" AS "u"`, nil},
		{"empty_list_removes_from", Select("id").From("a").From([]Relation{}...),
			`SELECT "id"`, nil},
		{"right_join_relation", Select("id").From(users).RightJoin(teams, on),
			`SELECT "id" FROM ("app"."users" AS "u" RIGHT JOIN "teams" AS "t" ON ("t"."id" = "u"."team_id"))`, nil},
		{"full_join_relation", Select("id").From(users).FullJoin(teams, on),
			`SELECT "id" FROM ("app"."users" AS "u" FULL JOIN "teams" AS "t" ON ("t"."id" = "u"."team_id"))`, nil},
		{"full_join_name", Select("id").From("users").FullJoin("teams", True()),
			`SELECT "id" FROM ("users" FULL JOIN "teams" ON TRUE)`, nil},
		{"cross_join_relation", Select("id").From("users").CrossJoin(teams),
			`SELECT "id" FROM ("users" CROSS JOIN "teams" AS "t")`, nil},
		{"update_relation", Update(users).Set(SetDefault("n")).From(teams).Where(on),
			`UPDATE "app"."users" AS "u" SET "n" = DEFAULT FROM "teams" AS "t" WHERE ("t"."id" = "u"."team_id")`, nil},
		{"delete_relation", DeleteFrom(users).Using(teams).Using("audit").Where(on),
			`DELETE FROM "app"."users" AS "u" USING "teams" AS "t", "audit" WHERE ("t"."id" = "u"."team_id")`, nil},
		{"insert_relation", InsertInto(Table("app", "users")).Set(SetDefault("n")).Returning(Col("n")),
			`INSERT INTO "app"."users" ("n") VALUES (DEFAULT) RETURNING "n"`, nil},
		{"group_by_names", Select("team_id").From("users").GroupBy("team_id", "kind"),
			`SELECT "team_id" FROM "users" GROUP BY "team_id", "kind"`, nil},
	})
}
