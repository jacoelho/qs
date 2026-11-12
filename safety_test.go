package qs

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This checks the source imports rather than forbidding reflection in testing
// utilities or claiming that a selected driver's encoding uses no reflection.
// projection may import reflect, which it uses to check destinations when a
// projection is declared.
func TestCoreImportPolicy(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	for dir, allowReflect := range map[string]bool{".": false, "projection": true} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := filepath.Join(dir, entry.Name())
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range f.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path == "github.com/jacoelho/qs" {
					continue
				}
				first, _, _ := strings.Cut(path, "/")
				if path == "reflect" && allowReflect {
					continue
				}
				if path == "reflect" || path == "unsafe" || path == "database/sql" || path == "context" || strings.Contains(first, ".") {
					t.Errorf("%s imports forbidden core dependency %s", name, path)
				}
			}
		}
	}
}

type localImporter struct {
	library  *types.Package
	standard types.Importer
}

func (i localImporter) Import(path string) (*types.Package, error) {
	if path == "github.com/jacoelho/qs" {
		return i.library, nil
	}
	return i.standard.Import(path)
}

// Compile-negative tests are important: a runtime assertion alone cannot prove
// that an incorrectly typed expression is rejected by the public API.
//
//nolint:tparallel // Subtests share the importer cache and must remain serial.
func TestGenericTypeChecks(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	standard := importer.Default()
	cfg := types.Config{Importer: standard}
	pkg, err := cfg.Check("github.com/jacoelho/qs", fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkClient := func(t *testing.T, body string) error {
		t.Helper()
		src := `package client; import "github.com/jacoelho/qs"; func run(){` + body + `}`
		f, parseErr := parser.ParseFile(fset, "client.go", src, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		c := types.Config{Importer: localImporter{pkg, standard}}
		_, checkErr := c.Check("client", fset, []*ast.File{f}, nil)
		return checkErr
	}
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"field_value", `_ = qs.TypedCol[int64]("id").Eq(42)`, true},
		{"field_wrong_value", `_ = qs.TypedCol[int64]("id").Eq("42")`, false},
		{"field_expression", `_=qs.TypedCol[int64]("id").EqField(qs.TypedCol[int64]("other_id"))`, true},
		{"statement_valid", `_,_,_=qs.ToSQL(qs.Select(qs.Param(1)))`, true},
		{"statement_options", `_,_,_=qs.Select(qs.Param(1)).ToSQLWith(qs.Options{MaxDepth:8})`, true},
		{"statement_append_options", `_,_,_=qs.Select(qs.Param(1)).AppendWith(nil,nil,qs.Options{MaxDepth:8})`, true},
		{"statement_methods", `type output interface { ToSQL() (string, []any, error); ToSQLWith(qs.Options) (string, []any, error); AppendSQL([]byte, []any) ([]byte, []any, error); AppendWith([]byte, []any, qs.Options) ([]byte, []any, error) }; var _ output = qs.SelectBuilder{}; var _ output = qs.InsertRows{}; var _ output = qs.InsertSelect{}; var _ output = qs.InsertAssignments{}; var _ output = qs.InsertDefaults{}; var _ output = qs.UpdateBuilder{}; var _ output = qs.DeleteBuilder{}; var _ output = qs.MergeBuilder{}; var _ output = qs.SetBuilder{}; var _ output = qs.ValuesBuilder{}; var _ output = qs.TableBuilder{}; var _ output = qs.SQLStatement{}`, true},
		{"statements_are_values", `q := qs.Select(qs.Param(1)); var _ qs.SelectBuilder = q.Where(qs.True()); var _ qs.Statement = q; var _ qs.Rowset = q`, true},
		{"comparison_takes_expression", `_ = qs.Col("id").Eq(qs.Param(42)); _ = qs.Col("id").In(qs.Param(1), qs.Col("other"))`, true},
		{"comparison_rejects_bare_value", `_ = qs.Col("id").Eq(42)`, false},
		{"membership_rejects_bare_value", `_ = qs.Col("id").In(1, 2)`, false},
		{"typed_requires_expression", `_ = qs.Typed[int64]("id")`, false},
		{"typed_expression", `_ = qs.Typed[int64](qs.Table("users").Col("id")).Eq(42)`, true},
		{"placeholder_wrong_type", `_ = qs.Options{PlaceholderStyle:"?"}`, false},
		{"field_wrong_expression", `_ = qs.TypedCol[int64]("id").EqField(qs.TypedCol[string]("name"))`, false},
		{"nullable_assignment", `_ = qs.TypedCol[string]("email").SetNullable(qs.NullOf[string]())`, true},
		{"typed_insert", `_ = qs.InsertInto("users").Set(qs.TypedCol[int]("id").Set(42), qs.TypedCol[string]("name").Set("Ana"))`, true},
		{"typed_insert_wrong_value", `_ = qs.InsertInto("users").Set(qs.TypedCol[int]("id").Set("42"))`, false},
		{"typed_insert_missing_value", `_ = qs.InsertInto("users").Set(qs.TypedCol[int]("id").Set())`, false},
		{"typed_insert_extra_value", `_ = qs.TypedCol[int]("id").Set(42, 43)`, false},
		{"write_value_general_expression", `_ = qs.Select(qs.Write(qs.Param(1)))`, false},
		{"bound_write_values", `_ = qs.InsertInto("users").Values(qs.Value(1), qs.Value("Ana"), qs.Value(nil)); var driver *struct{ Payload string }; _ = qs.Value(driver)`, true},
		{"bound_write_general_expression", `_ = qs.Select(qs.Value(1))`, false},
		{"bound_write_expr_conversion", `_ = qs.Expr(qs.Value(1))`, false},
		{"bound_write_as_row", `_ = qs.AssignRow([]qs.Expr{qs.Col("a"), qs.Col("b")}, qs.Value(1))`, false},
		{"default_general_expression", `_ = qs.Select(qs.Default())`, false},
		{"default_parameter_boundary_compiles", `_ = qs.Param(qs.Default())`, true},
		{"write_row_value_general_expression", `_ = qs.Select(qs.WriteRow(qs.Write(qs.Param(1))))`, false},
		{"scalar_write_as_row", `_ = qs.AssignRow([]qs.Expr{qs.Col("a"), qs.Col("b")}, qs.Write(qs.Param(1)))`, false},
		{"row_write_as_scalar", `_ = qs.Assign(qs.Col("a"), qs.WriteTuple(qs.Write(qs.Param(1)), qs.Write(qs.Param(2))))`, false},
		{"expr_write_conversion", `_ = qs.Expr(qs.Default())`, false},
		{"condition_write_conversion", `_ = qs.Condition(qs.Default())`, false},
		{"json_document_write_conversion", `_ = qs.JSONDocument(qs.Default())`, false},
		{"jsonb_document_write_conversion", `_ = qs.JSONBDocument(qs.Default())`, false},
		{"field_write_conversion", `_ = qs.Field[int](qs.Default())`, false},
		{"row_expr_write_conversion", `_ = qs.RowExpr(qs.WriteRow(qs.Write(qs.Param(1))))`, false},
		{"tuple2_write_conversion", `_ = qs.Tuple2Expr[int,string](qs.WriteTuple(qs.Write(qs.Param(1)),qs.Write(qs.Param("x"))))`, false},
		{"tuple3_write_conversion", `_ = qs.Tuple3Expr[int,string,bool](qs.WriteTuple(qs.Write(qs.Param(1)),qs.Write(qs.Param("x")),qs.Write(qs.Param(true))))`, false},
		{"tuple4_write_conversion", `_ = qs.Tuple4Expr[int,string,bool,int64](qs.WriteTuple(qs.Write(qs.Param(1)),qs.Write(qs.Param("x")),qs.Write(qs.Param(true)),qs.Write(qs.Param(int64(4)))))`, false},
		{"write_value_conversion", `_ = qs.WriteValue(qs.WriteRow(qs.Write(qs.Param(1))))`, false},
		{"write_row_conversion", `_ = qs.WriteRowValue(qs.Default())`, false},
		{"write_row_first_required", `_ = qs.WriteRow()`, false},
		{"write_tuple_second_required", `_ = qs.WriteTuple(qs.Write(qs.Param(1)))`, false},
		{"insert_value_requires_write", `_ = qs.InsertInto("users").Values(qs.Param(1))`, false},
		{"insert_values_first_required", `_ = qs.InsertInto("users").Values()`, false},
		{"insert_set_first_required", `_ = qs.InsertInto("users").Set()`, false},
		{"insert_columns_first_required", `_ = qs.InsertInto("users").Columns()`, false},
		{"list_assignments_valid", `xs := []qs.Assignment{qs.Set("v", qs.Value(1))}; _ = qs.InsertInto("users").Set(xs).Set(xs, qs.SetDefault("w")); _ = qs.Update("users").Set(xs, qs.SetDefault("w")); _ = qs.ConflictColumns("id").DoUpdate(qs.SetAllExcluded("a"), qs.SetDefault("w")); _ = qs.Matched().ThenUpdate(xs); _ = qs.NotMatched().ThenInsert(xs)`, true},
		{"list_after_element_refused", `_ = qs.InsertInto("users").Set(qs.SetDefault("w"), []qs.Assignment{})`, false},
		{"untyped_nil_list_refused", `_ = qs.InsertInto("users").Set(nil)`, false},
		{"update_set_first_required", `_ = qs.Update("users").Set()`, false},
		{"merge_update_first_required", `_ = qs.Matched().ThenUpdate()`, false},
		{"merge_insert_first_required", `_ = qs.NotMatched().ThenInsert()`, false},
		{"list_conflict_columns_valid", `names := []string{"a", "b"}; _ = qs.ConflictColumns(names, "c"); _ = qs.ConflictIndex([]qs.IndexElement{qs.IndexColumn("a")})`, true},
		{"names_or_expressions_valid", `names := []string{"a"}; exprs := []qs.Expr{qs.Col("a")}; _ = qs.Select("a", "b").Columns(exprs...).GroupBy("a").GroupBy(exprs...); _ = qs.Select(names...); _ = qs.Select(exprs...); _ = qs.InsertInto("users").Columns(names...); _ = qs.InsertInto("users").Columns(exprs...); _ = qs.DeleteFrom("users").AllRows().Returning("id").Returning(exprs...)`, true},
		{"names_and_expressions_do_not_mix", `_ = qs.Select("a", qs.Col("b"))`, false},
		{"bare_name_list_needs_spread", `_ = qs.Select([]string{"a"})`, false},
		{"group_by_without_columns", `_ = qs.Select("a").GroupBy()`, false},
		{"returning_without_columns", `_ = qs.DeleteFrom("users").AllRows().Returning()`, false},
		{"tables_or_relations_valid", `u := qs.Table("users").As("u"); _ = qs.Select("a").From(u).Join("t", qs.True()).LeftJoin(u, qs.True()).RightJoin(u, qs.True()).FullJoin(u, qs.True()).CrossJoin(u).CrossJoin("t"); _ = qs.Select("a").From("t", "s"); _ = qs.InsertInto(u); _ = qs.Update(u).From(u).From("t"); _ = qs.DeleteFrom(u).Using(u).Using("t"); _ = qs.MergeInto(u); _ = qs.InsertInto("t").DefaultValues().Into(u)`, true},
		{"tables_and_relations_do_not_mix", `_ = qs.Select("a").From("t", qs.Table("s"))`, false},
		{"from_without_tables", `_ = qs.Select("a").From()`, false},
		{"target_is_not_an_expression", `_ = qs.Update(qs.Col("users"))`, false},
		{"set_operations_take_a_list", `sets := []qs.Rowset{qs.Select("a")}; _ = qs.Union(sets...); _ = qs.Except(qs.Select("a"), qs.Select("b"), qs.Select("c"))`, true},
		{"insert_columns_source_valid", `_ = qs.InsertInto("users").Columns("id").Values(qs.Write(qs.Param(1)))`, true},
		{"insert_columns_select_valid", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1)))`, true},
		{"insert_columns_defaults_valid", `_ = qs.InsertInto("users").Columns("id").DefaultValues()`, true},
		{"insert_assignments_source_valid", `_ = qs.InsertInto("users").Set(qs.Set("v", qs.Write(qs.Param(1))))`, true},
		{"insert_source_roles_are_independent", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).From(qs.Select(qs.Param(1)))`, false},
		{"insert_select_cannot_choose_values", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Values(qs.Write(qs.Param(1)))`, false},
		{"insert_select_source_valid", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1)))`, true},
		{"insert_select_source_replacement", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1))).From(qs.Select(qs.Param(2)))`, false},
		{"insert_arrays_source_replacement", `_ = qs.InsertInto("users").FromArrays(qs.NamedArray("id", qs.Col("ids"))).From(qs.Select(qs.Param(2)))`, false},
		{"insert_named_source_replacement", `_ = qs.InsertInto("users").FromNamed(qs.Select(qs.Col("id"))).From(qs.Select(qs.Param(2)))`, false},
		{"insert_arrays_after_columns", `_ = qs.InsertInto("users").Columns("id").FromArrays(qs.NamedArray("id", qs.Col("ids")))`, false},
		{"insert_named_after_columns", `_ = qs.InsertInto("users").Columns("id").FromNamed(qs.Select(qs.Col("id")))`, false},
		{"insert_named_clauses", `_ = qs.InsertInto("users").FromNamed(qs.Select(qs.Col("id")).From("s")).OnConflict(qs.AnyConflict().DoNothing()).Returning("id")`, true},
		{"insert_statement_is_not_rowset", `_ = qs.InsertInto("users").From(qs.Update("source"))`, false},
		{"insert_columns_statement_is_not_rowset", `_ = qs.InsertInto("users").Columns("v").From(qs.Update("source"))`, false},
		{"insert_rows_cannot_choose_assignments", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).Set(qs.Set("v",qs.Write(qs.Param(2))))`, false},
		{"insert_select_cannot_choose_assignments", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Set(qs.Set("v",qs.Write(qs.Param(2))))`, false},
		{"insert_assignments_cannot_choose_values", `_ = qs.InsertInto("users").Set(qs.Set("v",qs.Write(qs.Param(1)))).Values(qs.Write(qs.Param(2)))`, false},
		{"insert_defaults_cannot_choose_values", `_ = qs.InsertInto("users").DefaultValues().Values(qs.Write(qs.Param(1)))`, false},
		{"insert_columns_cannot_choose_assignments", `_ = qs.InsertInto("users").Columns("v").Set(qs.Set("v",qs.Write(qs.Param(1))))`, false},
		{"insert_columns_cannot_choose_into", `_ = qs.InsertInto("users").Columns("v").Into("other")`, false},
		{"insert_columns_not_statement", `var _ qs.Statement = qs.InsertInto("users").Columns("v")`, false},
		{"insert_columns_not_rowset", `var _ qs.Rowset = qs.InsertInto("users").Columns("v")`, false},
		{"insert_rows_omit_columns", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).Columns("v")`, false},
		{"insert_select_omit_columns", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Columns("v")`, false},
		{"insert_assignments_omit_columns", `_ = qs.InsertInto("users").Set(qs.Set("v",qs.Write(qs.Param(1)))).Columns("v")`, false},
		{"insert_defaults_omit_columns", `_ = qs.InsertInto("users").DefaultValues().Columns("v")`, false},
		{"insert_rows_not_rowset", `var _ qs.Rowset = qs.InsertInto("users").Values(qs.Write(qs.Param(1)))`, false},
		{"insert_select_not_rowset", `var _ qs.Rowset = qs.InsertInto("users").From(qs.Select(qs.Param(1)))`, false},
		{"insert_defaults_not_rowset", `var _ qs.Rowset = qs.InsertInto("users").DefaultValues()`, false},
		{"insert_columns_not_target", `_ = qs.InsertTarget(qs.InsertColumnsTarget{})`, false},
		{"insert_columns_not_rows", `_ = qs.InsertRows(qs.InsertColumnsTarget{})`, false},
		{"insert_columns_not_select", `_ = qs.InsertSelect(qs.InsertColumnsTarget{})`, false},
		{"insert_columns_not_defaults", `_ = qs.InsertDefaults(qs.InsertColumnsTarget{})`, false},
		{"insert_default_cannot_override", `_ = qs.InsertInto("users").DefaultValues().OverridingUserValue()`, false},
		{"insert_columns_default_cannot_override", `_ = qs.InsertInto("users").Columns("id").DefaultValues().OverridingUserValue()`, false},
		{"insert_target_is_not_statement", `var _ qs.Statement = qs.InsertInto("users")`, false},
		{"conflict_any_update_forbidden", `_ = qs.AnyConflict().DoUpdate(qs.Set("v", qs.Write(qs.Param(1))))`, false},
		{"conflict_any_target_where_forbidden", `_ = qs.AnyConflict().TargetWhere(qs.True())`, false},
		{"conflict_constraint_target_where_forbidden", `_ = qs.ConflictConstraint("u").TargetWhere(qs.True())`, false},
		{"conflict_nothing_where_forbidden", `_ = qs.ConflictColumns("id").DoNothing().Where(qs.True())`, false},
		{"conflict_selector_requires_action", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictColumns("id"))`, false},
		{"conflict_any_nothing", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.AnyConflict().DoNothing())`, true},
		{"conflict_columns_nothing", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictColumns("id").DoNothing())`, true},
		{"conflict_columns_update", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictColumns("id").DoUpdate(qs.Set("v", qs.Write(qs.Param(2)))))`, true},
		{"conflict_index_nothing", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictIndex(qs.IndexColumn("id")).DoNothing())`, true},
		{"conflict_index_update", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictIndex(qs.IndexColumn("id")).DoUpdate(qs.Set("v", qs.Write(qs.Param(2)))))`, true},
		{"conflict_constraint_update", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictConstraint("u").DoUpdate(qs.Set("v", qs.Write(qs.Param(2)))))`, true},
		{"conflict_constraint_nothing", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictConstraint("u").DoNothing())`, true},
		{"conflict_update_where", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).OnConflict(qs.ConflictColumns("id").DoUpdate(qs.Set("v", qs.Write(qs.Param(2)))).Where(qs.True()))`, true},
		{"conflict_update_first_required", `_ = qs.ConflictColumns("id").DoUpdate()`, false},
		{"conflict_selector_conversion", `_ = qs.ConflictColumnsTarget(qs.ConflictIndexTarget{})`, false},
		{"conflict_selector_inverse_conversion", `_ = qs.ConflictIndexTarget(qs.ConflictColumnsTarget{})`, false},
		{"conflict_action_conversion", `_ = qs.ConflictUpdate(qs.ConflictNothing{})`, false},
		{"conflict_action_inverse_conversion", `_ = qs.ConflictNothing(qs.ConflictUpdate{})`, false},
		{"conflict_columns_first_required", `_ = qs.ConflictColumns()`, false},
		{"conflict_index_first_required", `_ = qs.ConflictIndex()`, false},
		{"write_row_expr_forward_conversion", `var _ qs.WriteRowValue = qs.WriteRowExpr(qs.Row(qs.Col("a"), qs.Col("b")))`, true},
		{"insert_role_conversion", `_ = qs.InsertRows(qs.InsertSelect{})`, false},
		{"insert_role_pointer_conversion", `_ = (*qs.InsertRows)(&qs.InsertSelect{})`, false},
		{"merge_selector_conversion", `_ = qs.MergeMatched(qs.MergeNotMatched{})`, false},
		{"nullable_wrong_assignment", `_ = qs.TypedCol[int64]("id").SetNullable(qs.NullOf[string]())`, false},
		{"nullable_wrong_constructor", `_ = qs.Null[int]{Value: "bad", Valid:true}`, false},
		{"nullable_optional", `_ = qs.Some(qs.NullOf[string]())`, true},
		{"tuple", `_ = qs.Tuple2(qs.TypedCol[int]("id"),qs.TypedCol[string]("name")).LtValues(1,"A")`, true},
		{"tuple_wrong_type", `_ = qs.Tuple2(qs.TypedCol[int]("id"),qs.TypedCol[string]("name")).LtValues("1","A")`, false},
		{"tuple_wrong_degree", `_ = qs.Tuple2(qs.TypedCol[int]("id"),qs.TypedCol[string]("name")).LtValues(1)`, false},
		{"mixed_membership", `_ = qs.TypedCol[int]("id").In(1,"A")`, false},
		{"same_type_membership", `_ = qs.TypedCol[int]("id").In(1,2)`, true},
		{"searched_case", `_ = qs.Case().When(qs.True(),qs.Param(1)).Else(qs.Param(2)).End()`, true},
		{"simple_case", `_ = qs.CaseOf(qs.Param(3)).WhenValue(qs.Param(4),qs.Param(5)).Else(qs.Param(6)).End()`, true},
		{"searched_case_value", `_ = qs.Case().WhenValue(qs.Param(1),qs.Param(2))`, false},
		{"simple_case_condition", `_ = qs.CaseOf(qs.Param(1)).When(qs.True(),qs.Param(2))`, false},
		{"case_cross_conversion", `_ = qs.SimpleCaseBuilder(qs.Case().When(qs.True(),qs.Param(1)))`, false},
		{"merge_matched_actions", `_ = qs.Matched().ThenUpdate(qs.Set("v",qs.Write(qs.Param(1)))); _ = qs.Matched().ThenDelete(); _ = qs.Matched().ThenDoNothing()`, true},
		{"merge_source_actions", `_ = qs.NotMatchedBySource().ThenUpdate(qs.Set("v",qs.Write(qs.Param(1)))); _ = qs.NotMatchedBySource().ThenDelete(); _ = qs.NotMatchedBySource().ThenDoNothing()`, true},
		{"merge_insert_actions", `_ = qs.NotMatched().ThenInsert(qs.Set("v",qs.Write(qs.Param(1)))); _ = qs.NotMatched().ThenInsertValues([]string{"v"},qs.Write(qs.Param(2))); _ = qs.NotMatched().ThenInsertDefault(); _ = qs.NotMatched().ThenDoNothing()`, true},
		{"merge_target_actions", `_ = qs.NotMatchedByTarget().ThenInsert(qs.Set("v",qs.Write(qs.Param(1)))); _ = qs.NotMatchedByTarget().ThenInsertValues([]string{"v"},qs.Write(qs.Param(2))); _ = qs.NotMatchedByTarget().ThenInsertDefault(); _ = qs.NotMatchedByTarget().ThenDoNothing()`, true},
		{"merge_overrides", `_ = qs.NotMatched().And(qs.True()).OverridingSystemValue().ThenInsert(qs.Set("v",qs.Write(qs.Param(1)))); _ = qs.NotMatchedByTarget().OverridingUserValue().ThenInsertValues([]string{"v"},qs.Write(qs.Param(2)))`, true},
		{"merge_matched_insert", `_ = qs.Matched().ThenInsert(qs.Set("v",qs.Write(qs.Param(1))))`, false},
		{"merge_source_insert", `_ = qs.NotMatchedBySource().ThenInsert(qs.Set("v",qs.Write(qs.Param(1))))`, false},
		{"merge_missing_delete", `_ = qs.NotMatched().ThenDelete()`, false},
		{"merge_target_update", `_ = qs.NotMatchedByTarget().ThenUpdate(qs.Set("v",qs.Write(qs.Param(1))))`, false},
		{"merge_incomplete", `_ = qs.MergeInto("t").When(qs.Matched())`, false},
		{"merge_completed_action", `_ = qs.Matched().ThenDelete().ThenDoNothing()`, false},
		{"merge_completed_condition", `_ = qs.Matched().ThenDelete().And(qs.True())`, false},
		{"merge_completed_override", `_ = qs.NotMatched().ThenInsert(qs.Set("v",qs.Write(qs.Param(1)))).OverridingSystemValue()`, false},
		{"merge_override_default", `_ = qs.NotMatched().OverridingSystemValue().ThenInsertDefault()`, false},
		{"merge_override_nothing", `_ = qs.NotMatched().OverridingUserValue().ThenDoNothing()`, false},
		{"merge_category_conversion", `_ = qs.MergeNotMatched(qs.Matched())`, false},
		{"merge_override_conversion", `_ = qs.MergeNotMatched(qs.NotMatched().OverridingSystemValue())`, false},
		{"join_completions", `_ = qs.Select(qs.Star()).From(qs.InnerJoin(qs.Table("a"),qs.Table("b")).On(qs.True()),qs.LeftJoin(qs.Table("c"),qs.Table("d")).Using("id"),qs.RightJoin(qs.Table("e"),qs.Table("f")).UsingAs("u","id"),qs.FullJoin(qs.Table("g"),qs.Table("h")).On(qs.True()))`, true},
		{"join_dynamic_conditions", `c := []qs.Condition{qs.True(),qs.False()}; _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).On(c[0],c[1:]...)`, true},
		{"join_dynamic_columns", `c := []string{"id","tenant"}; _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).Using(c[0],c[1:]...); _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).UsingAs("u",c[0],c[1:]...)`, true},
		{"join_incomplete_from", `_ = qs.Select(qs.Star()).From(qs.InnerJoin(qs.Table("a"),qs.Table("b")))`, false},
		{"join_incomplete_operand", `_ = qs.InnerJoin(qs.InnerJoin(qs.Table("a"),qs.Table("b")),qs.Table("c"))`, false},
		{"join_incomplete_lateral", `_ = qs.Lateral(qs.InnerJoin(qs.Table("a"),qs.Table("b")))`, false},
		{"join_incomplete_alias", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).As("j")`, false},
		{"join_missing_on", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).On()`, false},
		{"join_missing_using", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).Using()`, false},
		{"join_missing_using_alias", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).UsingAs("u")`, false},
		{"join_cross_on", `_ = qs.CrossJoin(qs.Table("a"),qs.Table("b")).On(qs.True())`, false},
		{"join_natural_using", `_ = qs.NaturalJoin(qs.Table("a"),qs.Table("b")).Using("id")`, false},
		{"join_completed_using", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).On(qs.True()).Using("id")`, false},
		{"join_completed_alias", `_ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).Using("id").UsingAs("u")`, false},
		{"join_explicit_conversion", `_ = qs.Relation(qs.InnerJoin(qs.Table("a"),qs.Table("b")))`, false},
		{"select_join_missing_condition", `_ = qs.Select(qs.Star()).From("a").Join("b")`, false},
		{"expression_is_not_statement", `_, _, _ = qs.ToSQL(qs.Col("id"))`, false},
		{"dml_is_not_rowset", `_ = qs.Scalar(qs.DeleteFrom("users").AllRows().Returning("id"))`, false},
		{"update_selected", `_, _, _ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).Where(qs.Col("id").Eq(qs.Param(1))).ToSQL()`, true},
		{"update_all_rows", `_, _, _ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).AllRows().ToSQL()`, true},
		{"update_unselected_is_not_statement", `_, _, _ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).ToSQL()`, false},
		{"update_unselected_is_not_statement_value", `var _ qs.Statement = qs.Update("t").Set(qs.Set("a", qs.Value(1)))`, false},
		{"update_where_needs_condition", `_ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).Where()`, false},
		{"update_all_rows_then_where", `_ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).AllRows().Where(qs.Col("a").IsNull())`, false},
		{"update_all_rows_then_cursor", `_ = qs.Update("t").Set(qs.Set("a", qs.Value(1))).AllRows().WhereCurrentOf("c")`, false},
		{"update_where_then_set", `_ = qs.Update("t").Where(qs.Col("a").IsNull()).Set(qs.Set("a", qs.Value(1)))`, false},
		{"delete_selected", `_, _, _ = qs.DeleteFrom("t").Where(qs.Col("id").Eq(qs.Param(1))).Returning("id").ToSQL()`, true},
		{"delete_unselected_is_not_statement", `_, _, _ = qs.DeleteFrom("t").ToSQL()`, false},
		{"delete_unselected_has_no_returning", `_ = qs.DeleteFrom("t").Returning("id")`, false},
		{"delete_where_then_cursor", `_ = qs.DeleteFrom("t").Where(qs.Col("a").IsNull()).WhereCurrentOf("c")`, false},
		{"delete_where_then_all_rows", `_ = qs.DeleteFrom("t").Where(qs.Col("a").IsNull()).AllRows()`, false},
		{"case_with_branch", `_ = qs.Case().When(qs.True(), qs.Param(1)).Else(qs.Param(2)).End()`, true},
		{"case_without_branch", `_ = qs.Case().End()`, false},
		{"case_else_without_branch", `_ = qs.Case().Else(qs.Param(1))`, false},
		{"case_pending_is_not_expression", `var _ qs.Expr = qs.Case()`, false},
		{"simple_case_with_branch", `_ = qs.CaseOf(qs.Col("a")).WhenValue(qs.Param(1), qs.Param(2)).End()`, true},
		{"simple_case_without_branch", `_ = qs.CaseOf(qs.Col("a")).End()`, false},
		{"select_columns", `_ = qs.Select(qs.Col("a"), qs.Col("b"))`, true},
		{"select_spread_list", `var c []qs.Expr; _ = qs.Select(c...)`, true},
		{"select_no_columns", `_ = qs.SelectNoColumns().From("t")`, true},
		{"select_without_columns", `_ = qs.Select()`, false},
		{"select_sql_without_columns", `_ = qs.SelectSQL()`, false},
		{"limit_with_ties", `_ = qs.Select("a").From("t").OrderBy(qs.Col("a").Asc()).LimitWithTies(3)`, true},
		{"json_text", `_ = qs.JSONBExpr(qs.Col("doc")).Key("items").Index(-1).TextKey("name").Eq("Ada")`, true},
		{"json_text_wrong_value", `_ = qs.JSONBExpr(qs.Col("doc")).TextKey("name").Eq(42)`, false},
		{"json_key_wrong_type", `_ = qs.JSONBExpr(qs.Col("doc")).Key(0)`, false},
		{"json_index_wrong_type", `_ = qs.JSONBExpr(qs.Col("doc")).Index("0")`, false},
		{"jsonb_containment", `_ = qs.JSONBExpr(qs.Col("doc")).Contains(qs.JSONBParam(` + "`" + `{"a":1}` + "`" + `))`, true},
		{"json_containment_unavailable", `_ = qs.JSONExpr(qs.Col("doc")).Contains(qs.JSONParam("{}"))`, false},
		{"jsonb_containment_wrong_document", `_ = qs.JSONBExpr(qs.Col("doc")).Contains(qs.JSONExpr(qs.Col("doc")))`, false},
		{"json_delete_unavailable", `_ = qs.JSONExpr(qs.Col("doc")).DeleteKey("a")`, false},
		{"json_param_wrong_value", `_ = qs.JSONBParam(42)`, false},
		{"json_assignment", `_ = qs.JSONBExpr(qs.Col("doc")).Set(qs.JSONBParam("{}"))`, true},
		{"json_assignment_wrong_document", `_ = qs.JSONBExpr(qs.Col("doc")).Set(qs.JSONExpr(qs.Col("doc")))`, false},
		{"sqljson_operands", `_ = qs.JSONArray(qs.Col("a"), qs.JSONBExpr(qs.Col("b")), qs.JSONExpr(qs.Col("c")), qs.FormatJSON(qs.Col("d")), qs.FormatJSONUTF8(qs.Col("e")))`, true},
		{"sqljson_operand_rejects_bare_value", `_ = qs.JSONArray(1)`, false},
		{"sqljson_format_is_not_expression", `var _ qs.Expr = qs.FormatJSON(qs.Col("a"))`, false},
		{"sqljson_format_nests_once", `_ = qs.FormatJSON(qs.FormatJSON(qs.Col("a")))`, false},
		{"sqljson_is_json_takes_no_format", `_ = qs.IsJSON(qs.FormatJSON(qs.Col("a")))`, false},
		{"sqljson_builder_is_not_expression", `var _ qs.Expr = qs.JSONObject()`, false},
		{"sqljson_builder_expression", `var _ qs.Expr = qs.JSONObject().Expr(); var _ qs.Expr = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).As("v")`, true},
		{"sqljson_conditions", `var _ qs.Condition = qs.JSONExists(qs.Col("d"), qs.LiteralString("$")).Condition(); var _ qs.Condition = qs.IsJSON(qs.Col("d")).Condition()`, true},
		{"sqljson_value_is_not_condition", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).Condition()`, false},
		{"sqljson_object_aggregate_has_no_order", `_ = qs.JSONObjectAggregate(qs.Col("k"), qs.Col("v")).OrderBy(qs.Col("k").Asc())`, false},
		{"sqljson_array_has_no_uniqueness", `_ = qs.JSONArray(qs.Col("a")).WithUniqueKeys()`, false},
		{"sqljson_array_query_has_no_null_clause", `_ = qs.JSONArrayQuery(qs.Select("a")).NullOnNull()`, false},
		{"sqljson_array_query_needs_rowset", `_ = qs.JSONArrayQuery(qs.DeleteFrom("t").AllRows())`, false},
		{"sqljson_parse_has_no_returning", `_ = qs.JSONParse(qs.Col("a")).Returning(qs.TypeText)`, false},
		{"sqljson_exists_has_no_returning", `_ = qs.JSONExists(qs.Col("d"), qs.LiteralString("$")).Returning(qs.TypeBool)`, false},
		{"sqljson_exists_has_no_on_empty", `_ = qs.JSONExists(qs.Col("d"), qs.LiteralString("$")).ErrorOnEmpty()`, false},
		{"sqljson_exists_has_no_null_on_error", `_ = qs.JSONExists(qs.Col("d"), qs.LiteralString("$")).NullOnError()`, false},
		{"sqljson_exists_has_no_default_on_error", `_ = qs.JSONExists(qs.Col("d"), qs.LiteralString("$")).DefaultOnError(qs.Param(1))`, false},
		{"sqljson_value_has_no_wrapper", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).WithWrapper()`, false},
		{"sqljson_value_has_no_quotes", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).OmitQuotes()`, false},
		{"sqljson_value_has_no_returning_format", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).ReturningFormatJSON(qs.TypeText)`, false},
		{"sqljson_value_has_no_empty_array", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).EmptyArrayOnError()`, false},
		{"sqljson_value_has_no_true", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).TrueOnError()`, false},
		{"sqljson_query_has_no_true", `_ = qs.JSONQuery(qs.Col("d"), qs.LiteralString("$")).TrueOnError()`, false},
		{"sqljson_query_has_no_unknown", `_ = qs.JSONQuery(qs.Col("d"), qs.LiteralString("$")).UnknownOnEmpty()`, false},
		{"sqljson_query_accepts_its_set", `_ = qs.JSONQuery(qs.Col("d"), qs.LiteralString("$")).ReturningFormatJSON(qs.TypeText).WithWrapper().KeepQuotes().EmptyArrayOnEmpty().DefaultOnError(qs.Param(1))`, true},
		{"sqljson_returning_utf8", `_ = qs.JSONSerialize(qs.Col("a")).ReturningFormatJSONUTF8()`, true},
		{"sqljson_returning_utf8_is_bytea_only", `_ = qs.JSONSerialize(qs.Col("a")).ReturningFormatJSONUTF8(qs.TypeText)`, false},
		{"sqljson_default_needs_value", `_ = qs.JSONValue(qs.Col("d"), qs.LiteralString("$")).DefaultOnEmpty()`, false},
		{"sqljson_table", `_ = qs.Select(qs.Star()).From(qs.JSONTable(qs.Col("d"), "$", qs.JSONOrdinality("n"), qs.JSONColumn("a", qs.TypeText), qs.JSONExistsColumn("e", qs.TypeBool), qs.JSONNested("$.x", qs.JSONColumn("b", qs.TypeText))).As("jt"))`, true},
		{"sqljson_table_needs_column", `_ = qs.JSONTable(qs.Col("d"), "$")`, false},
		{"sqljson_table_path_is_constant", `_ = qs.JSONTable(qs.Col("d"), qs.Col("p"), qs.JSONOrdinality("n"))`, false},
		{"sqljson_table_is_not_relation", `var _ qs.Relation = qs.JSONTable(qs.Col("d"), "$", qs.JSONOrdinality("n"))`, false},
		{"sqljson_table_is_not_expression", `_ = qs.Select(qs.JSONTable(qs.Col("d"), "$", qs.JSONOrdinality("n")))`, false},
		{"sqljson_table_has_no_null_on_error", `_ = qs.JSONTable(qs.Col("d"), "$", qs.JSONOrdinality("n")).NullOnError()`, false},
		{"sqljson_table_has_no_on_empty", `_ = qs.JSONTable(qs.Col("d"), "$", qs.JSONOrdinality("n")).ErrorOnEmpty()`, false},
		{"sqljson_nested_needs_column", `_ = qs.JSONNested("$")`, false},
		{"sqljson_nested_has_no_behavior", `_ = qs.JSONNested("$", qs.JSONOrdinality("n")).ErrorOnError()`, false},
		{"sqljson_ordinality_has_no_path", `_ = qs.JSONOrdinality("n").Path("$")`, false},
		{"sqljson_exists_column_has_no_on_empty", `_ = qs.JSONExistsColumn("e", qs.TypeBool).ErrorOnEmpty()`, false},
		{"sqljson_exists_column_has_no_wrapper", `_ = qs.JSONExistsColumn("e", qs.TypeBool).WithWrapper()`, false},
		{"sqljson_exists_column_has_no_format", `_ = qs.JSONExistsColumn("e", qs.TypeBool).FormatJSON()`, false},
		{"sqljson_exists_column_has_no_null", `_ = qs.JSONExistsColumn("e", qs.TypeBool).NullOnError()`, false},
		{"sqljson_value_column_has_no_true", `_ = qs.JSONColumn("a", qs.TypeText).TrueOnError()`, false},
		{"sqljson_column_is_not_expression", `_ = qs.Select(qs.JSONColumn("a", qs.TypeText))`, false},
		{"sqljson_foreign_column", `type column struct{}; _ = qs.JSONTable(qs.Col("d"), "$", column{})`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkClient(t, tc.body)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, type-check error=%v", tc.valid, err)
			}
		})
	}
	for _, tc := range []struct {
		name, valid, missing string
	}{
		{"set_row_first_required", `_ = qs.SetRow([]string{"id"}, qs.Value(1))`, `_ = qs.SetRow([]string{"id"})`},
		{"coalesce_first_required", `_ = qs.Coalesce(qs.Col("id"))`, `_ = qs.Coalesce()`},
		{"greatest_first_required", `_ = qs.Greatest(qs.Col("id"))`, `_ = qs.Greatest()`},
		{"least_first_required", `_ = qs.Least(qs.Col("id"))`, `_ = qs.Least()`},
		{"merge_insert_value_required", `_ = qs.NotMatched().ThenInsertValues([]string{"v"}, qs.Value(1))`, `_ = qs.NotMatched().ThenInsertValues([]string{"v"})`},
		{"merge_target_insert_value_required", `_ = qs.NotMatchedByTarget().ThenInsertValues([]string{"v"}, qs.Value(1))`, `_ = qs.NotMatchedByTarget().ThenInsertValues([]string{"v"})`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkClient(t, tc.valid); err != nil {
				t.Fatalf("required operand supplied: %v", err)
			}
			err := checkClient(t, tc.missing)
			if err == nil || !strings.Contains(err.Error(), "not enough arguments") {
				t.Fatalf("missing operand: want arity error, got %v", err)
			}
		})
	}
}
