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
func TestCoreHasNoReflectionUnsafeOrExternalImports(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
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
			first, _, _ := strings.Cut(path, "/")
			if path == "reflect" || path == "unsafe" || path == "database/sql" || path == "context" || strings.Contains(first, ".") {
				t.Errorf("%s imports forbidden core dependency %s", name, path)
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
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"field_value", `_ = qs.Typed[int64]("id").Eq(42)`, true},
		{"field_wrong_value", `_ = qs.Typed[int64]("id").Eq("42")`, false},
		{"field_expression", `_=qs.Typed[int64]("id").EqField(qs.Typed[int64]("other_id"))`, true},
		{"statement_valid", `_,_,_=qs.ToSQL(qs.Select(qs.Param(1)))`, true},
		{"statement_options", `_,_,_=qs.Select(qs.Param(1)).ToSQLWith(qs.Options{PlaceholderStyle:qs.Question})`, true},
		{"statement_append_options", `_,_,_=qs.Select(qs.Param(1)).AppendWith(nil,nil,qs.Options{PlaceholderStyle:qs.Question})`, true},
		{"statement_methods", `type output interface { ToSQL() (string, []any, error); ToSQLWith(qs.Options) (string, []any, error); AppendSQL([]byte, []any) ([]byte, []any, error); AppendWith([]byte, []any, qs.Options) ([]byte, []any, error) }; var _ output = (*qs.SelectBuilder)(nil); var _ output = (*qs.InsertRows)(nil); var _ output = (*qs.InsertSelect)(nil); var _ output = (*qs.InsertAssignments)(nil); var _ output = (*qs.InsertDefaults)(nil); var _ output = (*qs.UpdateBuilder)(nil); var _ output = (*qs.DeleteBuilder)(nil); var _ output = (*qs.MergeBuilder)(nil); var _ output = (*qs.SetBuilder)(nil); var _ output = (*qs.ValuesBuilder)(nil); var _ output = (*qs.TableBuilder)(nil); var _ output = (*qs.ExplainBuilder)(nil); var _ output = (*qs.TruncateBuilder)(nil); var _ output = (*qs.ExecuteBuilder)(nil); var _ output = (*qs.CreateTableAsBuilder)(nil); var _ output = (*qs.MaterializedViewBuilder)(nil); var _ output = (*qs.DeclareCursorBuilder)(nil); var _ output = (*qs.SelectIntoBuilder)(nil); var _ output = (*qs.SQLStatement)(nil)`, true},
		{"version_named", `var version qs.PostgreSQLVersion = qs.PostgreSQL17; _ = qs.Options{PostgreSQL:version}`, true},
		{"version_dynamic_int", `var version int = 17; _ = qs.Options{PostgreSQL:version}`, false},
		{"version_wrong_string", `_ = qs.Options{PostgreSQL:"17"}`, false},
		{"version_wrong_enum", `_ = qs.Options{PostgreSQL:qs.Question}`, false},
		{"placeholder_wrong_type", `_ = qs.Options{PlaceholderStyle:"?"}`, false},
		{"old_build_function", `_,_,_=qs.Build(qs.Select(qs.Param(1)))`, false},
		{"old_build_method", `_,_,_=qs.Select(qs.Param(1)).Build()`, false},
		{"old_build_with", `_,_,_=qs.BuildWith(qs.Select(qs.Param(1)),qs.Options{})`, false},
		{"old_build_expr", `_,_,_=qs.BuildExpr(qs.Param(1))`, false},
		{"old_must_build", `_,_=qs.MustBuild(qs.Select(qs.Param(1)))`, false},
		{"old_build_error", `var _ *qs.BuildError`, false},
		{"field_wrong_expression", `_ = qs.Typed[int64]("id").EqField(qs.Typed[string]("name"))`, false},
		{"nullable_assignment", `_ = qs.Typed[string]("email").SetNullable(qs.NullOf[string]())`, true},
		{"typed_insert", `_ = qs.InsertInto("users").Set(qs.Typed[int]("id").Set(42), qs.Typed[string]("name").Set("Ana"))`, true},
		{"typed_insert_wrong_value", `_ = qs.InsertInto("users").Set(qs.Typed[int]("id").Set("42"))`, false},
		{"typed_insert_missing_value", `_ = qs.InsertInto("users").Set(qs.Typed[int]("id").Set())`, false},
		{"typed_insert_extra_value", `_ = qs.Typed[int]("id").Set(42, 43)`, false},
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
		{"insert_targets_first_required", `_ = qs.InsertInto("users").Targets()`, false},
		{"insert_columns_source_valid", `_ = qs.InsertInto("users").Columns("id").Values(qs.Write(qs.Param(1)))`, true},
		{"insert_columns_select_valid", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1)))`, true},
		{"insert_columns_defaults_valid", `_ = qs.InsertInto("users").Columns("id").DefaultValues()`, true},
		{"insert_assignments_source_valid", `_ = qs.InsertInto("users").Set(qs.Set("v", qs.Write(qs.Param(1))))`, true},
		{"insert_source_roles_are_independent", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).From(qs.Select(qs.Param(1)))`, false},
		{"insert_select_cannot_choose_values", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Values(qs.Write(qs.Param(1)))`, false},
		{"insert_select_source_valid", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1)))`, true},
		{"insert_select_source_replacement_valid", `_ = qs.InsertInto("users").Columns("id").From(qs.Select(qs.Param(1))).From(qs.Select(qs.Param(2)))`, true},
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
		{"insert_rows_omit_targets", `_ = qs.InsertInto("users").Values(qs.Write(qs.Param(1))).Targets(qs.Col("v"))`, false},
		{"insert_select_omit_columns", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Columns("v")`, false},
		{"insert_select_omit_targets", `_ = qs.InsertInto("users").From(qs.Select(qs.Param(1))).Targets(qs.Col("v"))`, false},
		{"insert_assignments_omit_columns", `_ = qs.InsertInto("users").Set(qs.Set("v",qs.Write(qs.Param(1)))).Columns("v")`, false},
		{"insert_assignments_omit_targets", `_ = qs.InsertInto("users").Set(qs.Set("v",qs.Write(qs.Param(1)))).Targets(qs.Col("v"))`, false},
		{"insert_defaults_omit_columns", `_ = qs.InsertInto("users").DefaultValues().Columns("v")`, false},
		{"insert_defaults_omit_targets", `_ = qs.InsertInto("users").DefaultValues().Targets(qs.Col("v"))`, false},
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
		{"conflict_any_update_slice_forbidden", `_ = qs.AnyConflict().DoUpdateSlice(qs.SetAllExcluded("v"))`, false},
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
		{"nullable_wrong_assignment", `_ = qs.Typed[int64]("id").SetNullable(qs.NullOf[string]())`, false},
		{"nullable_wrong_constructor", `_ = qs.Null[int]{Value: "bad", Valid:true}`, false},
		{"nullable_optional", `_ = qs.Some(qs.NullOf[string]())`, true},
		{"tuple", `_ = qs.Tuple2(qs.Typed[int]("id"),qs.Typed[string]("name")).LtValues(1,"A")`, true},
		{"tuple_wrong_type", `_ = qs.Tuple2(qs.Typed[int]("id"),qs.Typed[string]("name")).LtValues("1","A")`, false},
		{"tuple_wrong_degree", `_ = qs.Tuple2(qs.Typed[int]("id"),qs.Typed[string]("name")).LtValues(1)`, false},
		{"mixed_membership", `_ = qs.In("id",1,"A")`, false},
		{"same_type_membership", `_ = qs.In("id",1,2)`, true},
		{"searched_case", `_ = qs.Case().When(qs.True(),qs.Param(1)).Else(qs.Param(2)).End()`, true},
		{"simple_case", `_ = qs.CaseOf(qs.Param(3)).WhenValue(qs.Param(4),qs.Param(5)).Else(qs.Param(6)).End()`, true},
		{"searched_case_value", `_ = qs.Case().WhenValue(qs.Param(1),qs.Param(2))`, false},
		{"simple_case_condition", `_ = qs.CaseOf(qs.Param(1)).When(qs.True(),qs.Param(2))`, false},
		{"case_cross_conversion", `_ = qs.SimpleCaseBuilder(*qs.Case())`, false},
		{"case_pointer_conversion", `_ = (*qs.SimpleCaseBuilder)(qs.Case())`, false},
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
		{"join_completions", `_ = qs.Select(qs.Star()).FromExpr(qs.InnerJoin(qs.Table("a"),qs.Table("b")).On(qs.True()),qs.LeftJoin(qs.Table("c"),qs.Table("d")).Using("id"),qs.RightJoin(qs.Table("e"),qs.Table("f")).UsingAs("u","id"),qs.FullJoin(qs.Table("g"),qs.Table("h")).On(qs.True()))`, true},
		{"join_dynamic_conditions", `c := []qs.Condition{qs.True(),qs.False()}; _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).On(c[0],c[1:]...)`, true},
		{"join_dynamic_columns", `c := []string{"id","tenant"}; _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).Using(c[0],c[1:]...); _ = qs.InnerJoin(qs.Table("a"),qs.Table("b")).UsingAs("u",c[0],c[1:]...)`, true},
		{"join_incomplete_from", `_ = qs.Select(qs.Star()).FromExpr(qs.InnerJoin(qs.Table("a"),qs.Table("b")))`, false},
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
		{"dml_is_not_rowset", `_ = qs.Scalar(qs.DeleteFrom("users").ReturningCols("id"))`, false},
		{"json_text", `_ = qs.JSONBCol("doc").Key("items").Index(-1).TextKey("name").Eq("Ada")`, true},
		{"json_text_wrong_value", `_ = qs.JSONBCol("doc").TextKey("name").Eq(42)`, false},
		{"json_key_wrong_type", `_ = qs.JSONBCol("doc").Key(0)`, false},
		{"json_index_wrong_type", `_ = qs.JSONBCol("doc").Index("0")`, false},
		{"jsonb_containment", `_ = qs.JSONBCol("doc").Contains(qs.JSONBParam(` + "`" + `{"a":1}` + "`" + `))`, true},
		{"json_containment_unavailable", `_ = qs.JSONCol("doc").Contains(qs.JSONParam("{}"))`, false},
		{"jsonb_containment_wrong_document", `_ = qs.JSONBCol("doc").Contains(qs.JSONCol("doc"))`, false},
		{"json_delete_unavailable", `_ = qs.JSONCol("doc").DeleteKey("a")`, false},
		{"json_param_wrong_value", `_ = qs.JSONBParam(42)`, false},
		{"json_assignment", `_ = qs.JSONBCol("doc").Set(qs.JSONBParam("{}"))`, true},
		{"json_assignment_wrong_document", `_ = qs.JSONBCol("doc").Set(qs.JSONCol("doc"))`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `package client; import "github.com/jacoelho/qs"; func run(){` + tc.body + `}`
			f, err := parser.ParseFile(fset, "client.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			c := types.Config{Importer: localImporter{pkg, standard}}
			_, err = c.Check("client", fset, []*ast.File{f}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, type-check error=%v", tc.valid, err)
			}
		})
	}
}
