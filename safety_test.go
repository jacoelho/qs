package qx

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
	qx       *types.Package
	standard types.Importer
}

func (i localImporter) Import(path string) (*types.Package, error) {
	if path == "github.com/jacoelho/qx" {
		return i.qx, nil
	}
	return i.standard.Import(path)
}

// Compile-negative tests are important: a runtime assertion alone cannot prove
// that an incorrectly typed expression is rejected by the public API.
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
	pkg, err := cfg.Check("github.com/jacoelho/qx", fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"field_value", `_ = qx.Typed[int64]("id").Eq(42)`, true},
		{"field_wrong_value", `_ = qx.Typed[int64]("id").Eq("42")`, false},
		{"field_expression", `_=qx.Typed[int64]("id").EqField(qx.Typed[int64]("other_id"))`, true},
		{"statement_valid", `_,_,_=qx.ToSQL(qx.Select(qx.Param(1)))`, true},
		{"statement_options", `_,_,_=qx.Select(qx.Param(1)).ToSQLWith(qx.Options{PlaceholderStyle:qx.Question})`, true},
		{"statement_append_options", `_,_,_=qx.Select(qx.Param(1)).AppendWith(nil,nil,qx.Options{PlaceholderStyle:qx.Question})`, true},
		{"statement_methods", `type output interface { ToSQL() (string, []any, error); ToSQLWith(qx.Options) (string, []any, error); AppendSQL([]byte, []any) ([]byte, []any, error); AppendWith([]byte, []any, qx.Options) ([]byte, []any, error) }; var _ output = (*qx.SelectBuilder)(nil); var _ output = (*qx.InsertBuilder)(nil); var _ output = (*qx.UpdateBuilder)(nil); var _ output = (*qx.DeleteBuilder)(nil); var _ output = (*qx.MergeBuilder)(nil); var _ output = (*qx.SetBuilder)(nil); var _ output = (*qx.ValuesBuilder)(nil); var _ output = (*qx.TableBuilder)(nil); var _ output = (*qx.ExplainBuilder)(nil); var _ output = (*qx.TruncateBuilder)(nil); var _ output = (*qx.ExecuteBuilder)(nil); var _ output = (*qx.CreateTableAsBuilder)(nil); var _ output = (*qx.MaterializedViewBuilder)(nil); var _ output = (*qx.DeclareCursorBuilder)(nil); var _ output = (*qx.SelectIntoBuilder)(nil); var _ output = (*qx.SQLStatement)(nil)`, true},
		{"version_named", `var version qx.PostgreSQLVersion = qx.PostgreSQL17; _ = qx.Options{PostgreSQL:version}`, true},
		{"version_dynamic_int", `var version int = 17; _ = qx.Options{PostgreSQL:version}`, false},
		{"version_wrong_string", `_ = qx.Options{PostgreSQL:"17"}`, false},
		{"version_wrong_enum", `_ = qx.Options{PostgreSQL:qx.Question}`, false},
		{"placeholder_wrong_type", `_ = qx.Options{PlaceholderStyle:"?"}`, false},
		{"old_build_function", `_,_,_=qx.Build(qx.Select(qx.Param(1)))`, false},
		{"old_build_method", `_,_,_=qx.Select(qx.Param(1)).Build()`, false},
		{"old_build_with", `_,_,_=qx.BuildWith(qx.Select(qx.Param(1)),qx.Options{})`, false},
		{"old_build_expr", `_,_,_=qx.BuildExpr(qx.Param(1))`, false},
		{"old_must_build", `_,_=qx.MustBuild(qx.Select(qx.Param(1)))`, false},
		{"old_build_error", `var _ *qx.BuildError`, false},
		{"field_wrong_expression", `_ = qx.Typed[int64]("id").EqField(qx.Typed[string]("name"))`, false},
		{"nullable_assignment", `_ = qx.Typed[string]("email").SetNullable(qx.NullOf[string]())`, true},
		{"typed_insert", `_ = qx.InsertInto("users").Set(qx.Typed[int]("id").Set(42), qx.Typed[string]("name").Set("Ana"))`, true},
		{"typed_insert_wrong_value", `_ = qx.InsertInto("users").Set(qx.Typed[int]("id").Set("42"))`, false},
		{"typed_insert_missing_value", `_ = qx.InsertInto("users").Set(qx.Typed[int]("id").Set())`, false},
		{"typed_insert_extra_value", `_ = qx.InsertInto("users").Set(qx.Typed[int]("id").Set(42, 43))`, false},
		{"nullable_wrong_assignment", `_ = qx.Typed[int64]("id").SetNullable(qx.NullOf[string]())`, false},
		{"nullable_wrong_constructor", `_ = qx.Null[int]{Value: "bad", Valid:true}`, false},
		{"nullable_optional", `_ = qx.Some(qx.NullOf[string]())`, true},
		{"tuple", `_ = qx.Tuple2(qx.Typed[int]("id"),qx.Typed[string]("name")).LtValues(1,"A")`, true},
		{"tuple_wrong_type", `_ = qx.Tuple2(qx.Typed[int]("id"),qx.Typed[string]("name")).LtValues("1","A")`, false},
		{"tuple_wrong_degree", `_ = qx.Tuple2(qx.Typed[int]("id"),qx.Typed[string]("name")).LtValues(1)`, false},
		{"mixed_membership", `_ = qx.In("id",1,"A")`, false},
		{"same_type_membership", `_ = qx.In("id",1,2)`, true},
		{"expression_is_not_statement", `_, _, _ = qx.ToSQL(qx.Col("id"))`, false},
		{"dml_is_not_rowset", `_ = qx.Scalar(qx.DeleteFrom("users").ReturningCols("id"))`, false},
		{"json_text", `_ = qx.JSONBCol("doc").Key("items").Index(-1).TextKey("name").Eq("Ada")`, true},
		{"json_text_wrong_value", `_ = qx.JSONBCol("doc").TextKey("name").Eq(42)`, false},
		{"json_key_wrong_type", `_ = qx.JSONBCol("doc").Key(0)`, false},
		{"json_index_wrong_type", `_ = qx.JSONBCol("doc").Index("0")`, false},
		{"jsonb_containment", `_ = qx.JSONBCol("doc").Contains(qx.JSONBParam(` + "`" + `{"a":1}` + "`" + `))`, true},
		{"json_containment_unavailable", `_ = qx.JSONCol("doc").Contains(qx.JSONParam("{}"))`, false},
		{"jsonb_containment_wrong_document", `_ = qx.JSONBCol("doc").Contains(qx.JSONCol("doc"))`, false},
		{"json_delete_unavailable", `_ = qx.JSONCol("doc").DeleteKey("a")`, false},
		{"json_param_wrong_value", `_ = qx.JSONBParam(42)`, false},
		{"json_assignment", `_ = qx.JSONBCol("doc").Set(qx.JSONBParam("{}"))`, true},
		{"json_assignment_wrong_document", `_ = qx.JSONBCol("doc").Set(qx.JSONCol("doc"))`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `package client; import "github.com/jacoelho/qx"; func run(){` + tc.body + `}`
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
