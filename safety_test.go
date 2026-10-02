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
		{"statement_methods", `type output interface { ToSQL() (string, []any, error); ToSQLWith(qs.Options) (string, []any, error); AppendSQL([]byte, []any) ([]byte, []any, error); AppendWith([]byte, []any, qs.Options) ([]byte, []any, error) }; var _ output = (*qs.SelectBuilder)(nil); var _ output = (*qs.InsertBuilder)(nil); var _ output = (*qs.UpdateBuilder)(nil); var _ output = (*qs.DeleteBuilder)(nil); var _ output = (*qs.MergeBuilder)(nil); var _ output = (*qs.SetBuilder)(nil); var _ output = (*qs.ValuesBuilder)(nil); var _ output = (*qs.TableBuilder)(nil); var _ output = (*qs.ExplainBuilder)(nil); var _ output = (*qs.TruncateBuilder)(nil); var _ output = (*qs.ExecuteBuilder)(nil); var _ output = (*qs.CreateTableAsBuilder)(nil); var _ output = (*qs.MaterializedViewBuilder)(nil); var _ output = (*qs.DeclareCursorBuilder)(nil); var _ output = (*qs.SelectIntoBuilder)(nil); var _ output = (*qs.SQLStatement)(nil)`, true},
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
		{"typed_insert_extra_value", `_ = qs.InsertInto("users").Set(qs.Typed[int]("id").Set(42, 43))`, false},
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
		{"merge_matched_actions", `_ = qs.Matched().ThenUpdate(qs.Set("v",1)); _ = qs.Matched().ThenDelete(); _ = qs.Matched().ThenDoNothing()`, true},
		{"merge_source_actions", `_ = qs.NotMatchedBySource().ThenUpdate(qs.Set("v",1)); _ = qs.NotMatchedBySource().ThenDelete(); _ = qs.NotMatchedBySource().ThenDoNothing()`, true},
		{"merge_insert_actions", `_ = qs.NotMatched().ThenInsert(qs.Set("v",1)); _ = qs.NotMatched().ThenInsertValues([]string{"v"},qs.Param(2)); _ = qs.NotMatched().ThenInsertDefault(); _ = qs.NotMatched().ThenDoNothing()`, true},
		{"merge_target_actions", `_ = qs.NotMatchedByTarget().ThenInsert(qs.Set("v",1)); _ = qs.NotMatchedByTarget().ThenInsertValues([]string{"v"},qs.Param(2)); _ = qs.NotMatchedByTarget().ThenInsertDefault(); _ = qs.NotMatchedByTarget().ThenDoNothing()`, true},
		{"merge_overrides", `_ = qs.NotMatched().And(qs.True()).OverridingSystemValue().ThenInsert(qs.Set("v",1)); _ = qs.NotMatchedByTarget().OverridingUserValue().ThenInsertValues([]string{"v"},qs.Param(2))`, true},
		{"merge_matched_insert", `_ = qs.Matched().ThenInsert(qs.Set("v",1))`, false},
		{"merge_source_insert", `_ = qs.NotMatchedBySource().ThenInsert(qs.Set("v",1))`, false},
		{"merge_missing_delete", `_ = qs.NotMatched().ThenDelete()`, false},
		{"merge_target_update", `_ = qs.NotMatchedByTarget().ThenUpdate(qs.Set("v",1))`, false},
		{"merge_incomplete", `_ = qs.MergeInto("t").When(qs.Matched())`, false},
		{"merge_completed_action", `_ = qs.Matched().ThenDelete().ThenDoNothing()`, false},
		{"merge_completed_condition", `_ = qs.Matched().ThenDelete().And(qs.True())`, false},
		{"merge_completed_override", `_ = qs.NotMatched().ThenInsert(qs.Set("v",1)).OverridingSystemValue()`, false},
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
