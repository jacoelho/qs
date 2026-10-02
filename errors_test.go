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
		{"select_zero", Select()}, {"nil", nil}, {"typed_nil", (*SelectBuilder)(nil)},
		{"where_zero", Select(LiteralInt(1)).Where(Condition{})},
		{"identifier_empty", Select(Col(""))}, {"identifier_component", Select(Col("a..b"))},
		{"identifier_nul", Select(Ident("a\x00b"))}, {"identifier_utf8", Select(Ident("\xff"))},
		{"raw_empty", SelectSQL("")}, {"raw_nul", SelectSQL("a\x00")},
		{"cast_zero", Select(Param(1).Cast(DataType{}))}, {"cast_varchar", Select(Param(1).Cast(Varchar(0)))},
		{"cast_numeric", Select(Param(1).Cast(Decimal(1001, 0)))},
		{"nan", Select(LiteralFloat(math.NaN()))}, {"infinity", Select(LiteralFloat(math.Inf(1)))},
		{"literal_nul", Select(LiteralString("a\x00"))},
		{"bind_expression", Select(Param(Col("x")))}, {"bind_condition", Select(Param(True()))},
		{"bind_statement", Select(Param(Select(Param(1))))},
		{"bind_optional", Select(Param(Some(1)))}, {"bind_null", Select(Param(NullOf[int]()))},
		{"bind_null_pointer", Select(Param((*Null[int])(nil)))},
		{"limit_negative", Select(LiteralInt(1)).Limit(-1)}, {"offset_negative", Select(LiteralInt(1)).Offset(-1)},
		{"fetch_limit", Select(LiteralInt(1)).Limit(1).Fetch(2, OnlyRows)},
		{"fetch_mode", Select(LiteralInt(1)).Fetch(1, FetchMode(99))},
		{"fetch_ties_unordered", Select(LiteralInt(1)).Fetch(1, WithTies)},
		{"fetch_ties_locked", Select(LiteralInt(1)).OrderBy(Ordinal(1).Asc()).Fetch(1, WithTies).Lock(ForUpdate())},
		{"order_zero", Select(LiteralInt(1)).OrderBy(Order{})}, {"ordinal_zero", Select(LiteralInt(1)).OrderBy(Ordinal(0).Asc())},
		{"lock_distinct", Select(Star()).From("t").Distinct().Lock(ForUpdate())},
		{"lock_group", Select(Col("id")).From("t").GroupBy("id").Lock(ForUpdate())},
		{"lock_zero", Select(Star()).From("t").Lock(LockClause{})},
		{"join_no_from", Select(Star()).Join("t", True())},
		{"subquery_nil", Select(Star()).FromExpr(Subquery(nil, "s"))},
		{"subquery_no_alias", Select(Star()).FromExpr(Subquery(Select(Param(1)), ""))},
		{"subquery_alias_width", Select(Star()).FromExpr(Select(Param(1)).As("s", "a", "b"))},
		{"scalar_width", Select(Scalar(Select(Param(1), Param(2))))},
		{"in_width", Select(Col("a").InQuery(SelectCols("a", "b")).Expr())},
		{"in_nil", Select(Col("a").InQuery(nil).Expr())},
		{"row_width", Select(Row(Col("a"), Col("b")).EqValues(1).Expr())},
		{"row_empty", Select(Row().Expr())},
		{"row_membership_width", Select(Row(Col("a"), Col("b")).In(Row(Param(1))).Expr())},
		{"set_width", Union(SelectCols("a"), SelectCols("b", "c"))},
		{"set_nil", Union(nil, Select(Param(1)))},
		{"values_empty", Values()}, {"values_row_width", Values(1).Row(2, 3)},
		{"values_default", ValuesExpr(Default())},
		{"cte_duplicate", Select(Param(1)).With(CTE("c", Select(Param(1))), CTE("c", Select(Param(2))))},
		{"cte_nil", Select(Param(1)).With(CTE("c", nil))},
		{"cte_invalid_body", Select(Param(1)).With(CTE("c", Truncate("t")))},
		{"cte_width", Select(Param(1)).With(CTE("c", Select(Param(1))).Columns("a", "b"))},
		{"cte_search_nonrecursive", Select(Param(1)).With(CTE("c", Select(Param(1))).Search(SearchDepthFirst("id").Set("ord")))},
		{"cte_search_empty", Select(Param(1)).WithRecursive(CTE("c", Select(Param(1))).Search(SearchBreadthFirst().Set("ord")))},
		{"cte_cycle_empty", Select(Param(1)).WithRecursive(CTE("c", Select(Param(1))).Cycle(Cycle("id")))},
		{"cte_nested_dml", Select(Scalar(Select(Param(1)).With(CTE("d", DeleteFrom("t").ReturningCols("id")))))},
		{"insert_target", InsertInto("").Values(1)}, {"insert_source", InsertInto("t")},
		{"insert_two_sources", InsertInto("t").Values(1).DefaultValues()},
		{"insert_nil_select", InsertInto("t").From(nil)},
		{"insert_columns_width", InsertInto("t").Columns("a", "b").Values(1)},
		{"insert_select_width", InsertInto("t").Columns("a").From(SelectCols("a", "b"))},
		{"insert_columns_duplicate", InsertInto("t").Columns("a", "a").Values(1, 2)},
		{"insert_set_columns", InsertInto("t").Columns("a").Set(Set("a", 1))},
		{"insert_only", InsertIntoTable(Table("t").Only()).Values(1)},
		{"insert_default_override", InsertInto("t").DefaultValues().OverridingUserValue()},
		{"update_no_assignments", Update("t")}, {"update_target", Update("").Set(Set("a", 1))},
		{"update_duplicate", Update("t").Set(Set("a", 1), Set("a", 2))},
		{"update_tuple_empty", Update("t").Set(SetRow(nil, Param(1)))},
		{"update_tuple_width", Update("t").Set(SetRow([]string{"a", "b"}, Param(1)))},
		{"update_tuple_duplicate", Update("t").Set(SetRow([]string{"a", "a"}, Param(1), Param(2)))},
		{"update_tuple_query_width", Update("t").Set(SetRowFrom([]string{"a"}, SelectCols("b", "c")))},
		{"update_bad_target", Update("t").Set(Assign(Param("a"), Param(1)))},
		{"update_missing_guard", Update("t").Set(Set("a", 1)).RequireWhere()},
		{"cursor_and_where", DeleteFrom("t").Where(True()).WhereCurrentOf("c")},
		{"delete_target", DeleteFrom("")}, {"delete_missing_guard", DeleteFrom("t").RequireWhere()},
		{"returning_no_projection", DeleteFrom("t").ReturningRows(ReturningAliases{Old: "o"})},
		{"returning_same_alias", DeleteFrom("t").ReturningCols("id").ReturningRows(ReturningAliases{Old: "r", New: "r"})},
		{"conflict_missing_action", InsertInto("t").Values(1).OnConflict(ConflictColumns("a"))},
		{"conflict_any_update", InsertInto("t").Values(1).OnConflict(AnyConflict().DoUpdate(Set("a", 1)))},
		{"conflict_empty_target", InsertInto("t").Values(1).OnConflict(ConflictColumns().DoNothing())},
		{"conflict_empty_index", InsertInto("t").Values(1).OnConflict(ConflictIndex().DoNothing())},
		{"conflict_nothing_where", InsertInto("t").Values(1).OnConflict(AnyConflict().DoNothing().Where(True()))},
		{"conflict_constraint_predicate", InsertInto("t").Values(1).OnConflict(ConflictConstraint("pk").TargetWhere(True()).DoNothing())},
		{"case_nil", Select((*SearchedCaseBuilder)(nil).End())},
		{"function_modifier_wrong", Select(Col("x").Distinct())},
		{"ordered_set_distinct", Select(PercentileCont(Param(0.5)).Distinct().WithinGroup(Asc("x")))},
		{"ordered_set_window", Select(PercentileCont(Param(0.5)).WithinGroup(Asc("x")).Over(Window()))},
		{"window_duplicate", Select(RowNumber().OverNamed("w")).Window("w", Window()).Window("w", Window())},
		{"window_empty_name", Select(RowNumber().OverNamed(""))},
		{"frame_bad_start", Select(CountAll().Over(Window().RowsBetween(UnboundedFollowing(), CurrentRow())))},
		{"frame_bad_end", Select(CountAll().Over(Window().RowsBetween(CurrentRow(), UnboundedPreceding())))},
		{"frame_backwards", Select(CountAll().Over(Window().RowsBetween(Following(1), Preceding(1))))},
		{"frame_negative", Select(CountAll().Over(Window().RowsBetween(Preceding(-1), CurrentRow())))},
		{"range_unordered", Select(CountAll().Over(Window().RangeBetween(Preceding(1), CurrentRow())))},
		{"range_many_orders", Select(CountAll().Over(Window().OrderBy(Asc("a"), Asc("b")).RangeBetween(Preceding(1), CurrentRow())))},
		{"groups_unordered", Select(CountAll().Over(Window().GroupsBetween(UnboundedPreceding(), CurrentRow())))},
		{"exclude_no_frame", Select(CountAll().Over(Window().Exclude(ExcludeTies)))},
		{"inherit_partition", Select(CountAll().Over(Window().Base("w").PartitionBy(Col("a"))))},
		{"operator_comment", Select(Operator(Col("a"), "--", Param(1)))},
		{"operator_keyword", Select(Operator(Col("a"), "UNION SELECT", Param(1)))},
		{"escape_too_long", Select(Like("a", "b").Escape("xy").Expr())},
		{"escape_wrong_operator", Select(Eq("a", 1).Escape("\\").Expr())},
		{"table_lateral", Select(Star()).FromExpr(Lateral(Table("t")))},
		{"subquery_only", Select(Star()).FromExpr(Select(Param(1)).As("s").Only())},
		{"sample_subquery", Select(Star()).FromExpr(Select(Param(1)).As("s").TableSample("system", Param(10)))},
		{"sample_no_method", Select(Star()).FromExpr(Table("t").Repeatable(Param(1)))},
		{"ordinality_table", Select(Star()).FromExpr(Table("t").WithOrdinality())},
		{"rows_from_empty", Select(Star()).FromExpr(RowsFrom())},
		{"record_ordinality", Select(Star()).FromExpr(TableFunc(Call("f")).DefineColumns(Def("x", Int4)).WithOrdinality())},
		{"json_object_odd", Select(JSONBBuildObject(Param("x")))},
		{"json_exists_returning", Select(JSONExists(Col("doc"), Param("$")).Returning(Bool).Expr())},
		{"json_exists_empty", Select(JSONExists(Col("doc"), Param("$")).OnEmpty(JSONFalse()).Expr())},
		{"json_exists_wrong_error", Select(JSONExists(Col("doc"), Param("$")).OnError(JSONNull()).Expr())},
		{"json_value_wrapper", Select(JSONValue(Col("doc"), Param("$")).WithWrapper().Expr())},
		{"json_value_empty_array", Select(JSONValue(Col("doc"), Param("$")).OnEmpty(JSONEmptyArray()).Expr())},
		{"json_query_quotes_wrapper", Select(JSONQuery(Col("doc"), Param("$")).WithWrapper().OmitQuotes().Expr())},
		{"json_format_without_returning", Select(JSONQuery(Col("doc"), Param("$")).FormatJSON().Expr())},
		{"json_passing_duplicate", Select(JSONQuery(Col("doc"), Param("$")).Passing("x", Param(1)).Passing("x", Param(2)).Expr())},
		{"json_table_empty", Select(Star()).FromExpr(JSONTable(Col("doc"), "$").As("j"))},
		{"json_table_bad_root_error", Select(Star()).FromExpr(JSONTable(Col("doc"), "$", JSONColumn("a", Int4)).OnError(JSONNull()).As("j"))},
		{"json_table_duplicate_column", Select(Star()).FromExpr(JSONTable(Col("doc"), "$", JSONColumn("a", Int4), JSONColumn("a", Text)).As("j"))},
		{"json_ordinality_path", Select(Star()).FromExpr(JSONTable(Col("doc"), "$", JSONOrdinality("n").Path("$")).As("j"))},
		{"json_nested_options", Select(Star()).FromExpr(JSONTable(Col("doc"), "$", JSONNested("$", JSONColumn("a", Int4)).OnError(JSONError())).As("j"))},
		{"json_table_column_zero", Select(Star()).FromExpr(JSONTable(Col("doc"), "$", JSONTableColumn{}).As("j"))},
		{"merge_no_on", MergeInto("t").Using(Table("s")).When(Matched().ThenDelete())},
		{"merge_no_when", MergeInto("t").Using(Table("s")).On(True())},
		{"merge_zero_branch", MergeInto("t").Using(Table("s")).On(True()).When(MergeWhen{})},
		{"merge_unreachable", MergeInto("t").Using(Table("s")).On(True()).When(Matched().ThenDelete(), Matched().And(True()).ThenDoNothing())},
		{"merge_empty_insert", MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsert())},
		{"merge_insert_width", MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsertValues([]string{"a", "b"}, Param(1)))},
		{"merge_duplicate_columns", MergeInto("t").Using(Table("s")).On(True()).When(NotMatched().ThenInsertValues([]string{"a", "a"}, Param(1), Param(2)))},
		{"explain_analyze_generic", Explain(Select(Param(1))).Analyze(true).GenericPlan(true)},
		{"explain_format", Explain(Select(Param(1))).Format(ExplainFormat(99))},
		{"explain_nested", Explain(Explain(Select(Param(1))))},
		{"truncate_empty", Truncate()}, {"truncate_alias", Truncate().TablesExpr(Table("t").As("a"))},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tt.q, ErrInvalid)
		})
	}
}

func TestFeatureVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version PostgreSQLVersion
		q       Statement
	}{
		{"ties", PostgreSQL13, SelectCols("id").OrderBy(Asc("id")).Fetch(1, WithTies)},
		{"date_bin", PostgreSQL14, Select(DateBin(Param("1 hour"), Param("2020-01-01"), Param("2000-01-01")))},
		{"search", PostgreSQL14, Select(Star()).WithRecursive(CTE("t", Select(Param(1))).Search(SearchDepthFirst("id").Set("ord"))).From("t")},
		{"merge", PostgreSQL15, MergeInto("t").Using(Table("s")).On(True()).When(Matched().ThenDoNothing())},
		{"negative_scale", PostgreSQL15, Select(Param(123).Cast(Decimal(2, -3)))},
		{"generic_plan", PostgreSQL16, Explain(Select(Param(1))).GenericPlan(true)},
		{"merge_returning", PostgreSQL17, MergeInto("t").Using(Table("s")).On(True()).When(Matched().ThenDoNothing()).Returning(MergeAction())},
		{"merge_by_source", PostgreSQL17, MergeInto("t").Using(Table("s")).On(True()).When(NotMatchedBySource().ThenDelete())},
		{"json_value", PostgreSQL17, Select(JSONValue(Param(`{"a":1}`), Param("$.a")).Expr())},
		{"json_table", PostgreSQL17, Select(Star()).FromExpr(JSONTable(Param(`{"a":1}`), "$", JSONColumn("a", Int4)).As("j"))},
		{"returning_old", PostgreSQL18, Update("t").Set(Set("a", 1)).Returning(Old("a"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sql, args, err := ToSQLWith(tt.q, Options{PostgreSQL: tt.version - 1})
			if !errors.Is(err, ErrUnsupported) || sql != "" || args != nil {
				t.Fatalf("lower version accepted: %s %#v %v", sql, args, err)
			}
			if _, _, err := ToSQLWith(tt.q, Options{PostgreSQL: tt.version}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
