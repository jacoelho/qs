package qs

import (
	"errors"
	"math"
	"testing"
)

func TestRenderErrorMethods(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		err   *RenderError
		want  string
		cause error
	}{
		{"nil", nil, "<nil>", nil},
		{"zero", &RenderError{}, "qs: render error", nil},
		{"context_without_cause", &RenderError{Clause: "SELECT", Detail: "requires a projection"}, "qs: render error (SELECT): requires a projection", nil},
		{"validation", &RenderError{Cause: ErrInvalid, Clause: "SELECT", Detail: "requires a projection"}, "qs: invalid query (SELECT): requires a projection", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
			if got := tc.err.Unwrap(); !errors.Is(got, tc.cause) {
				t.Fatalf("Unwrap() = %v, want %v", got, tc.cause)
			}
		})
	}
}

func TestInvalidConstruction(t *testing.T) {
	t.Parallel()

	bad := []struct {
		name string
		q    Statement
	}{
		{"select_zero", Select([]Expr(nil)...)}, {"nil", nil},
		{"where_zero", Select(LiteralInt(1)).Where(Condition{})},
		{"identifier_empty", Select(Col(""))}, {"identifier_component", Select(Col("a", "", "b"))},
		{"identifier_nul", Select(Col("a\x00b"))}, {"identifier_utf8", Select(Col("\xff"))},
		{"raw_empty", SelectSQL("")}, {"raw_nul", SelectSQL("a\x00")},
		{"cast_zero", Select(Param(1).Cast(DataType{}))}, {
			"cast_varchar",
			Select(Param(1).Cast(TypeVarchar(0))),
		},
		{"cast_numeric", Select(Param(1).Cast(TypeDecimal(1001, 0)))},
		{"nan", Select(LiteralFloat(math.NaN()))}, {"infinity", Select(LiteralFloat(math.Inf(1)))},
		{"literal_nul", Select(LiteralString("a\x00"))},
		{"bind_expression", Select(Param(Col("x")))}, {"bind_condition", Select(Param(True()))},
		{"bind_statement", Select(Param(Select(Param(1))))},
		{"bind_optional", Select(Param(Some(1)))}, {"bind_null", Select(Param(NullOf[int]()))},
		{"bind_null_pointer", Select(Param((*Null[int])(nil)))},
		{"limit_negative", Select(LiteralInt(1)).Limit(-1)}, {
			"offset_negative",
			Select(LiteralInt(1)).Offset(-1),
		},
		{"ties_negative", Select(LiteralInt(1)).OrderBy(Ordinal(1).Asc()).LimitWithTies(-1)},
		{"ties_unordered", Select(LiteralInt(1)).LimitWithTies(1)},
		{
			"ties_skip_locked",
			Select(Col("a")).
				From("t").
				OrderBy(Col("a").Asc()).
				LimitWithTies(1).
				Lock(ForUpdate().SkipLocked()),
		},
		{"order_zero", Select(LiteralInt(1)).OrderBy(Order{})}, {
			"ordinal_zero",
			Select(LiteralInt(1)).OrderBy(Ordinal(0).Asc()),
		},
		{"lock_distinct", Select(Star()).From("t").Distinct().Lock(ForUpdate())},
		{"lock_group", Select(Col("id")).From("t").GroupBy("id").Lock(ForUpdate())},
		{"lock_having", Select(Col("id")).From("t").Having(True()).Lock(ForUpdate())},
		{"lock_zero", Select(Star()).From("t").Lock(LockClause{})},
		{"join_no_from", Select(Star()).Join("t", True())},
		{"subquery_nil", Select(Star()).From(Subquery(nil, "s"))},
		{"subquery_no_alias", Select(Star()).From(Subquery(Select(Param(1)), ""))},
		{"subquery_alias_width", Select(Star()).From(Select(Param(1)).As("s", "a", "b"))},
		{"scalar_width", Select(Scalar(Select(Param(1), Param(2))))},
		{"in_width", Select(Col("a").InQuery(Select("a", "b")).Expr())},
		{"in_nil", Select(Col("a").InQuery(nil).Expr())},
		{"row_width", Select(Row(Col("a"), Col("b")).Eq(Row(Param(1))).Expr())},
		{"row_empty", Select(Row().Expr())},
		{"row_membership_width", Select(Row(Col("a"), Col("b")).In(Row(Param(1))).Expr())},
		{"set_width", Union(Select("a"), Select("b", "c"))},
		{"set_nil", Union(nil, Select(Param(1)))},
		{"values_empty", Values()}, {"values_row_width", Values(1).Row(2, 3)},
		{"bound_write_descriptor", Select(Param(Default()))},
		{
			"cte_duplicate",
			Select(Param(1)).With(CTE("c", Select(Param(1))), CTE("c", Select(Param(2)))),
		},
		{"cte_nil", Select(Param(1)).With(CTE("c", nil))},
		{"cte_width", Select(Param(1)).With(CTE("c", Select(Param(1))).Columns("a", "b"))},
		{
			"cte_search_nonrecursive",
			Select(Param(1)).With(
				CTE("c", Select(Param(1))).Search(SearchDepthFirst("id").Set("ord")),
			),
		},
		{
			"cte_search_empty",
			Select(Param(1)).WithRecursive(
				CTE("c", Select(Param(1))).Search(SearchBreadthFirst().Set("ord")),
			),
		},
		{
			"cte_cycle_empty",
			Select(Param(1)).WithRecursive(CTE("c", Select(Param(1))).Cycle(Cycle("id"))),
		},
		{
			"cte_nested_dml",
			Select(
				Scalar(
					Select(Param(1)).With(CTE("d", DeleteFrom("t").AllRows().Returning("id"))),
				),
			),
		},
		{"insert_target", InsertInto("").Values(Write(Param(1)))},
		{"insert_nil_select", InsertInto("t").From(nil)},
		{"insert_columns_width", InsertInto("t").Columns("a", "b").Values(Write(Param(1)))},
		{"insert_select_width", InsertInto("t").Columns("a").From(Select("a", "b"))},
		{
			"insert_columns_duplicate",
			InsertInto("t").Columns("a", "a").Values(Write(Param(1)), Write(Param(2))),
		},
		{"insert_only", InsertInto(Table("t").Only()).Values(Write(Param(1)))},
		{"update_no_assignments", Update("t").AllRows()}, {
			"update_target",
			Update("").Set(Set("a", Write(Param(1)))).AllRows(),
		},
		{
			"update_duplicate",
			Update("t").Set(Set("a", Write(Param(1))), Set("a", Write(Param(2)))).AllRows(),
		},
		{
			"update_qualified_duplicate",
			Update("t").
				Set(Assign(Col("t", "a"), Write(Param(1))), Assign(Col("t", "a"), Write(Param(2)))).
				AllRows(),
		},
		{
			"update_schema_qualified_duplicate",
			Update("t").
				Set(
					Assign(Col("s", "t", "a"), Write(Param(1))),
					Assign(Col("s", "t", "a"), Write(Param(2))),
				).
				AllRows(),
		},
		{
			"insert_qualified_targets_duplicate",
			InsertInto("t").
				Columns(Col("t", "a"), Col("t", "a")).
				Values(Write(Param(1)), Write(Param(2))),
		},
		{"update_tuple_empty", Update("t").Set(SetRow(nil, Write(Param(1)))).AllRows()},
		{
			"update_tuple_width",
			Update("t").Set(SetRow([]string{"a", "b"}, Write(Param(1)))).AllRows(),
		},
		{
			"update_tuple_duplicate",
			Update("t").Set(SetRow([]string{"a", "a"}, Write(Param(1)), Write(Param(2)))).AllRows(),
		},
		{
			"update_tuple_query_width",
			Update("t").Set(SetRowFrom([]string{"a"}, Select("b", "c"))).AllRows(),
		},
		{"update_bad_target", Update("t").Set(Assign(Param("a"), Write(Param(1)))).AllRows()},
		{
			"update_unselected",
			UpdateBuilder{table: table("t"), set: []Assignment{Set("a", Write(Param(1)))}},
		},
		{"update_where_true", Update("t").Set(Set("a", Write(Param(1)))).Where(True())},
		{"update_where_empty_and", Update("t").Set(Set("a", Write(Param(1)))).Where(And())},
		{"delete_target", DeleteFrom("").AllRows()}, {
			"delete_unselected",
			DeleteBuilder{table: table("t")},
		},
		{"delete_where_true", DeleteFrom("t").Where(True(), True())},
		{"delete_where_empty_and", DeleteFrom("t").Where(And())},
		{
			"returning_no_projection",
			DeleteFrom("t").AllRows().ReturningRows(ReturningAliases{Old: "o"}),
		},
		{
			"returning_same_alias",
			DeleteFrom("t").
				AllRows().
				Returning("id").
				ReturningRows(ReturningAliases{Old: "r", New: "r"}),
		},
		{
			"conflict_empty_target",
			InsertInto("t").Values(Write(Param(1))).OnConflict(ConflictColumnsTarget{}.DoNothing()),
		},
		{
			"conflict_empty_index",
			InsertInto("t").Values(Write(Param(1))).OnConflict(ConflictIndexTarget{}.DoNothing()),
		},
		{"function_modifier_wrong", Select(Col("x").Distinct())},
		{
			"ordered_set_distinct",
			Select(PercentileCont(Param(0.5)).Distinct().WithinGroup(Col("x").Asc())),
		},
		{
			"ordered_set_window",
			Select(PercentileCont(Param(0.5)).WithinGroup(Col("x").Asc()).Over(Window())),
		},
		{
			"ordered_set_named_window",
			Select(PercentileCont(Param(0.5)).WithinGroup(Col("x").Asc()).OverNamed("w")).Window("w", Window()),
		},
		{"set_row_nil_subquery", Update("t").Set(SetRowFrom([]string{"a"}, nil)).AllRows()},
		{"assign_row_nil_subquery", Update("t").Set(AssignRowFrom([]Expr{Col("a")}, nil)).AllRows()},
		{"merge_insert_zero_assignment", MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsert(Assignment{}))},
		{
			"window_duplicate",
			Select(RowNumber().OverNamed("w")).Window("w", Window()).Window("w", Window()),
		},
		{"window_empty_name", Select(RowNumber().OverNamed(""))},
		{
			"frame_bad_start",
			Select(CountAll().Over(Window().RowsBetween(UnboundedFollowing(), CurrentRow()))),
		},
		{
			"frame_bad_end",
			Select(CountAll().Over(Window().RowsBetween(CurrentRow(), UnboundedPreceding()))),
		},
		{"frame_backwards", Select(CountAll().Over(Window().RowsBetween(Following(1), Preceding(1))))},
		{"frame_negative", Select(CountAll().Over(Window().RowsBetween(Preceding(-1), CurrentRow())))},
		{
			"range_unordered",
			Select(CountAll().Over(Window().RangeBetween(Preceding(1), CurrentRow()))),
		},
		{
			"range_many_orders",
			Select(
				CountAll().Over(
					Window().OrderBy(Col("a").Asc(), Col("b").Asc()).RangeBetween(
						Preceding(1),
						CurrentRow(),
					),
				),
			),
		},
		{
			"groups_unordered",
			Select(CountAll().Over(Window().GroupsBetween(UnboundedPreceding(), CurrentRow()))),
		},
		{"exclude_no_frame", Select(CountAll().Over(Window().Exclude(ExcludeTies)))},
		{"inherit_partition", Select(CountAll().Over(Window().Base("w").PartitionBy(Col("a"))))},
		{"operator_comment", Select(Operator(Col("a"), "--", Param(1)))},
		{"operator_keyword", Select(Operator(Col("a"), "UNION SELECT", Param(1)))},
		{"escape_too_long", Select(Col("a").Like("b").Escape("xy").Expr())},
		{"escape_wrong_operator", Select(Col("a").Eq(Param(1)).Escape("\\").Expr())},
		{"table_lateral", Select(Star()).From(Lateral(Table("t")))},
		{"subquery_only", Select(Star()).From(Select(Param(1)).As("s").Only())},
		{
			"sample_subquery",
			Select(Star()).From(Select(Param(1)).As("s").TableSample("system", Param(10))),
		},
		{"sample_no_method", Select(Star()).From(Table("t").Repeatable(Param(1)))},
		{"ordinality_table", Select(Star()).From(Table("t").WithOrdinality())},
		{"rows_from_empty", Select(Star()).From(RowsFrom())},
		{
			"record_ordinality",
			Select(Star()).From(
				TableFunc(Call("f")).DefineColumns(Def("x", TypeInt4)).WithOrdinality(),
			),
		},
		{"json_object_odd", Select(JSONBBuildObject(Param("x")))},
		{"merge_no_on", MergeInto("t").Using(Table("s")).When(Matched().ThenDelete())},
		{"merge_no_when", MergeInto("t").Using(Table("s")).On(True())},
		{"merge_zero_branch", MergeInto("t").Using(Table("s")).On(True()).When(MergeWhen{})},
		{
			"merge_unreachable",
			MergeInto("t").
				Using(Table("s")).
				On(True()).
				When(Matched().ThenDelete(), Matched().And(True()).ThenDoNothing()),
		},
		{
			"merge_empty_insert",
			MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsert([]Assignment(nil))),
		},
		{
			"merge_insert_width",
			MergeInto("t").
				Using(Table("s")).
				On(True()).
				When(NotMatched().ThenInsertValues([]string{"a", "b"}, Write(Param(1)))),
		},
		{
			"merge_duplicate_columns",
			MergeInto("t").
				Using(Table("s")).
				On(True()).
				When(
					NotMatched().ThenInsertValues(
						[]string{"a", "a"},
						Write(Param(1)),
						Write(Param(2)),
					),
				),
		},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tt.q, ErrInvalid)
		})
	}
}

// A dynamic filter whose entries all vanish must not reach every row, however
// the constant was assembled: And and NotIn of empty lists are TRUE.
func TestMutationWhereRejectsPredicatesTrueForEveryRow(t *testing.T) {
	t.Parallel()
	var none []Condition
	var ids []Expr
	for name, where := range map[string][]Condition{
		"true":                    {True()},
		"empty_and":               {And(none...)},
		"empty_not_in":            {Col("a").NotIn(ids...)},
		"and_of_empty_filters":    {And(Col("a").NotIn(ids...), Col("b").NotIn(ids...))},
		"listed_empty_filters":    {Col("a").NotIn(ids...), And(none...)},
		"not_false":               {Not(False())},
		"not_empty_in":            {Not(Col("a").In(ids...))},
		"not_empty_or":            {Or(none...).Not()},
		"or_with_true":            {Or(Col("a").IsNull(), True())},
		"parenthesized_true":      {LiteralBool(true).Parenthesized().Condition()},
		"nested_and_of_or_of_not": {And(Or(False(), Not(False())), And(True(), True()))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, Update("t").Set(Set("a", Value(1))).Where(where[0], where[1:]...), ErrInvalid)
			checkError(t, DeleteFrom("t").Where(where[0], where[1:]...), ErrInvalid)
		})
	}
}

// Only a predicate that is TRUE for every row is refused: one that is FALSE
// for every row changes nothing, and one that reads the row is the caller's.
func TestMutationWhereAcceptsOtherPredicates(t *testing.T) {
	t.Parallel()
	var ids []Expr
	b := Col("b").IsNull()
	runCases(t, []renderCase{
		{"false", DeleteFrom("t").Where(False()), `DELETE FROM "t" WHERE FALSE`, nil},
		{"empty_in", DeleteFrom("t").Where(Col("a").In(ids...)), `DELETE FROM "t" WHERE FALSE`, nil},
		{"and_with_false", DeleteFrom("t").Where(And(True(), False())), `DELETE FROM "t" WHERE (TRUE AND FALSE)`, nil},
		{"and_with_row_condition", DeleteFrom("t").Where(And(True(), b)), `DELETE FROM "t" WHERE (TRUE AND ("b" IS NULL))`, nil},
		{"or_of_row_conditions", DeleteFrom("t").Where(Or(False(), b)), `DELETE FROM "t" WHERE (FALSE OR ("b" IS NULL))`, nil},
		{"not_row_condition", DeleteFrom("t").Where(Not(b)), `DELETE FROM "t" WHERE (NOT ("b" IS NULL))`, nil},
	})
}

// The literal TRUE does not select rows by itself, and beside a condition that
// does it is rendered as written.
func TestMutationWhereKeepsTrueBesideACondition(t *testing.T) {
	t.Parallel()
	checkSQL(t, Update("t").Set(Set("a", Write(LiteralInt(1)))).Where(True(), Col("b").IsNull()),
		`UPDATE "t" SET "a" = 1 WHERE TRUE AND ("b" IS NULL)`)
	var filters []Condition
	checkSQL(t, DeleteFrom("t").Where(And(filters...), Col("b").IsNull()),
		`DELETE FROM "t" WHERE TRUE AND ("b" IS NULL)`)
}
