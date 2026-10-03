package qs

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRenderErrorStructuralPaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		query Statement
		want  []string
	}{
		{"projection_siblings", Select(Param(1), Param(2), Expr{}, Expr{}).Where(Condition{}), []string{"SELECT", "projection", "expression[3]"}},
		{"cte_before_projection", Select(Expr{}).With(CTE("private_name", Select(Param("private_value"), Expr{}))).Where(Condition{}), []string{"SELECT", "CTE[1]", "body", "SELECT", "projection", "expression[2]"}},
		{"cte_search_before_sibling", Select(Expr{}).WithRecursive(CTE("c", Select(LiteralInt(1))).Search(SearchDepthFirst("").Set("seq")), CTE("d", Select(Expr{}))), []string{"SELECT", "CTE[1]"}},
		{"scalar_subquery", Select(Scalar(Select(Expr{}))), []string{"SELECT", "projection", "expression[1]", "scalar subquery", "SELECT", "projection", "expression[1]"}},
		{"function_arguments", Select(Call("private_function", Param(1), Expr{}, Expr{})), []string{"SELECT", "projection", "expression[1]", "arguments", "expression[2]"}},
		{"where", Select(LiteralInt(1)).Where(True(), Condition{}, Condition{}), []string{"SELECT", "WHERE", "condition[2]"}},
		{"case", Select(Case().When(True(), LiteralInt(1)).When(True(), Expr{}).Else(Expr{}).End()), []string{"SELECT", "projection", "expression[1]", "CASE THEN[2]"}},
		{"set_right", Union(Select(LiteralInt(1)), Select(Expr{})), []string{"set operation", "right", "SELECT", "projection", "expression[1]"}},
		{"values", ValuesExpr(LiteralInt(1), LiteralInt(2)).RowExpr(LiteralInt(3), Expr{}), []string{"VALUES", "row[2]", "expression[2]"}},
		{"assignment", Update("private_table").Set(Set("a", Value(1)), Set("b", Write(Expr{}))), []string{"UPDATE", "SET", "assignment[2]", "value"}},
		{"insert_values", InsertInto("private_table").Values(Value(1), Write(Expr{})), []string{"INSERT", "VALUES", "row[1]", "value[2]"}},
		{"order", Select(LiteralInt(1)).OrderBy(LiteralInt(1).Asc(), Expr{}.Asc()), []string{"SELECT", "ORDER BY", "order[2]"}},
		{"join_right", Select(LiteralInt(1)).FromExpr(InnerJoin(Table("a"), Relation{}).On(Condition{})), []string{"SELECT", "FROM", "relation[1]", "right"}},
		{"join_on", Select(LiteralInt(1)).FromExpr(InnerJoin(Table("a"), Table("b")).On(True(), Condition{})), []string{"SELECT", "FROM", "relation[1]", "ON", "condition[2]"}},
		{"conflict_target", InsertInto("t").Values(Value(1)).OnConflict(ConflictColumns("").TargetWhere(Condition{}).DoUpdate(Set("a", Write(Expr{})))), []string{"INSERT", "ON CONFLICT", "target"}},
		{"merge_condition", MergeInto("t").Using(Table("s")).On(True()).When(Matched().And(Condition{}).ThenUpdate(Set("a", Write(Expr{})))), []string{"MERGE", "WHEN[1]", "condition", "condition[1]"}},
		{"merge_action", MergeInto("t").Using(Table("s")).On(True()).When(Matched().And(True()).ThenUpdate(Set("a", Write(Expr{})))), []string{"MERGE", "WHEN[1]", "SET", "assignment[1]", "value"}},
		{"filter_before_over", Select(CountAll().Filter(True(), Condition{}).Over(Window().PartitionBy(Expr{}))), []string{"SELECT", "projection", "expression[1]", "FILTER", "condition[2]"}},
		{"window_partition", Select(CountAll().Filter(True()).Over(Window().PartitionBy(LiteralInt(1), Expr{}).OrderBy(Expr{}.Asc()))), []string{"SELECT", "projection", "expression[1]", "OVER", "PARTITION BY", "expression[2]"}},
		{"window_order", Select(CountAll().Over(Window().OrderBy(LiteralInt(1).Asc(), Expr{}.Asc()).Rows(PrecedingExpr(Expr{})))), []string{"SELECT", "projection", "expression[1]", "OVER", "ORDER BY", "order[2]"}},
		{"window_frame", Select(CountAll().Over(Window().RowsBetween(PrecedingExpr(Expr{}), FollowingExpr(Expr{})))), []string{"SELECT", "projection", "expression[1]", "OVER", "frame start"}},
		{"returning", DeleteFrom("t").Where(True()).Returning(LiteralInt(1), Expr{}), []string{"DELETE", "RETURNING", "expression[2]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := tc.query.ToSQL()
			var detail *RenderError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &detail) || sql != "" || args != nil {
				t.Fatalf("SQL=%q args=%#v err=%v", sql, args, err)
			}
			if !reflect.DeepEqual(detail.Path, tc.want) {
				t.Fatalf("path=%q; want %q", detail.Path, tc.want)
			}
			if strings.Contains(err.Error(), "private_") {
				t.Fatalf("path leaked application content: %v", err)
			}
		})
	}
}

func TestRenderErrorPathsOwnTheirStorage(t *testing.T) {
	t.Parallel()
	sql := make([]byte, 0, 512)
	args := make([]any, 0, 16)
	query := Select(Param("secret"), Expr{})
	_, _, err := query.AppendSQL(sql, args)
	var first *RenderError
	if !errors.As(err, &first) {
		t.Fatalf("missing RenderError: %v", err)
	}
	held := first.Path
	want := slices.Clone(held)
	if len(want) == 0 {
		t.Fatal("missing path")
	}
	for _, next := range []Statement{Select(LiteralInt(1)), Select(Scalar(Select(Expr{}))), query} {
		_, _, _ = next.AppendSQL(sql, args)
		if !slices.Equal(held, want) || !slices.Equal(first.Path, want) {
			t.Fatalf("retained path changed: %q, want %q", first.Path, want)
		}
	}
}

func TestRenderErrorDepthPath(t *testing.T) {
	t.Parallel()
	query := Select(Scalar(Select(Scalar(Select(LiteralInt(1))))))
	_, _, err := query.ToSQLWith(Options{MaxDepth: 3})
	var detail *RenderError
	if !errors.Is(err, ErrDepth) || !errors.As(err, &detail) {
		t.Fatalf("depth error=%v", err)
	}
	if len(detail.Path) == 0 || detail.Path[0] != "SELECT" {
		t.Fatalf("missing enclosing statement: %q", detail.Path)
	}
}

func TestRenderErrorPathPreservesSentinels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		query   Statement
		options Options
		cause   error
		want    []string
	}{
		{"unsupported", Select(Normalize(Param("secret"))), Options{PostgreSQL: PostgreSQL12}, ErrUnsupported, []string{"SELECT", "projection", "expression[1]"}},
		{"parameter_limit", Select(Param("secret"), Param("other")), Options{MaxParameters: 1}, ErrParameterLimit, []string{"SELECT", "projection", "expression[2]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := tc.query.ToSQLWith(tc.options)
			detail, ok := errors.AsType[*RenderError](err)
			if !errors.Is(err, tc.cause) || !ok || !slices.Equal(detail.Path, tc.want) {
				t.Fatalf("got %v; want cause %v and path %q", err, tc.cause, tc.want)
			}
		})
	}
}
