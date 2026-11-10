package parsetree

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func bind(number int) any { return number }

func TestBuildClassifiesTrees(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		tree   string
		kind   Kind
		detail string
		sql    string
	}{
		{
			name: "unconditional_update_states_all_rows",
			tree: `{"UpdateStmt":{"relation":{"relname":"t","inh":true,"relpersistence":"p"},"targetList":[{"ResTarget":{"name":"a","val":{"A_Const":{"ival":{"ival":1}}}}}]}}`,
			kind: Built, sql: `UPDATE "t" SET "a" = 1`,
		},
		{
			name: "unconditional_delete_states_all_rows",
			tree: `{"DeleteStmt":{"relation":{"relname":"t","relpersistence":"p"}}}`,
			kind: Built, sql: `DELETE FROM ONLY "t"`,
		},
		{
			name: "update_where_true_has_no_spelling",
			tree: `{"UpdateStmt":{"relation":{"relname":"t","inh":true,"relpersistence":"p"},"targetList":[{"ResTarget":{"name":"a","val":{"A_Const":{"ival":{"ival":1}}}}}],"whereClause":{"A_Const":{"boolval":{"boolval":true}}}}}`,
			kind: Gap, detail: "mutation with WHERE TRUE; AllRows renders no WHERE",
		},
		{
			name: "delete_where_true_has_no_spelling",
			tree: `{"DeleteStmt":{"relation":{"relname":"t","inh":true,"relpersistence":"p"},"whereClause":{"A_Const":{"boolval":{"boolval":true}}}}}`,
			kind: Gap, detail: "mutation with WHERE TRUE; AllRows renders no WHERE",
		},
		{
			name: "delete_where_false_keeps_its_predicate",
			tree: `{"DeleteStmt":{"relation":{"relname":"t","inh":true,"relpersistence":"p"},"whereClause":{"A_Const":{"boolval":{}}}}}`,
			kind: Built, sql: `DELETE FROM "t" WHERE FALSE`,
		},
		{
			name: "zero_valued_constant",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"A_Const":{"ival":{}}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: Built, sql: `SELECT 0`,
		},
		{
			name: "removed_statement",
			tree: `{"TruncateStmt":{"relations":[{"RangeVar":{"relname":"t","inh":true,"relpersistence":"p"}}],"behavior":"DROP_RESTRICT"}}`,
			kind: OutOfScope, detail: "TRUNCATE",
		},
		{
			// Scope is reported for the whole tree, sorted, not for the first node met.
			name: "removed_features_are_listed_together",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"XmlExpr":{"op":"IS_XMLFOREST","named_args":[{"ResTarget":{"val":{"XmlSerialize":{}}}}],"xmloption":"XMLOPTION_DOCUMENT"}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: OutOfScope, detail: "XMLFOREST, XMLSERIALIZE",
		},
		{
			// A removed feature wins over an unrelated gap in the same query.
			name: "scope_precedes_gap",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"RowExpr":{"row_format":"COERCE_EXPLICIT_CALL"}}}},{"ResTarget":{"val":{"XmlSerialize":{}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: OutOfScope, detail: "XMLSERIALIZE",
		},
		{
			// The tree records only a NULL clause that differs from the form's
			// default: arrays drop NULLs unless told otherwise, objects keep them.
			name: "json_null_clause_is_relative_to_the_form",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"JsonArrayConstructor":{"exprs":[{"JsonValueExpr":{"raw_expr":{"A_Const":{"ival":{"ival":1}}},"format":{"format_type":"JS_FORMAT_DEFAULT","encoding":"JS_ENC_DEFAULT"}}}]}}}},{"ResTarget":{"val":{"JsonObjectConstructor":{"exprs":[{"JsonKeyValue":{"key":{"A_Const":{"sval":{"sval":"k"}}},"value":{"raw_expr":{"A_Const":{"ival":{"ival":1}}},"format":{"format_type":"JS_FORMAT_JSON","encoding":"JS_ENC_UTF8"}}}}],"absent_on_null":true}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: Built, sql: `SELECT JSON_ARRAY(1 NULL ON NULL), JSON_OBJECT(E'k' : 1 FORMAT JSON ENCODING UTF8 ABSENT ON NULL)`,
		},
		{
			// The grammar accepts any behavior on any SQL/JSON function; the
			// analyzer does not, and neither do the typed builders.
			name: "json_behavior_postgresql_rejects",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"JsonFuncExpr":{"op":"JSON_VALUE_OP","context_item":{"raw_expr":{"ColumnRef":{"fields":[{"String":{"sval":"doc"}}]}},"format":{"format_type":"JS_FORMAT_DEFAULT","encoding":"JS_ENC_DEFAULT"}},"pathspec":{"A_Const":{"sval":{"sval":"$"}}},"on_error":{"btype":"JSON_BEHAVIOR_EMPTY_ARRAY"},"wrapper":"JSW_UNSPEC","quotes":"JS_QUOTES_UNSPEC"}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: Gap, detail: "JSON_VALUE ON ERROR does not accept EMPTY ARRAY; PostgreSQL rejects it",
		},
		{
			name: "missing_constructor",
			tree: `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"SQLValueFunction":{"op":"SVFOP_CURRENT_ROLE","typmod":-1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`,
			kind: Gap, detail: "CURRENT_ROLE has no constructor",
		},
		{
			name: "unknown_statement",
			tree: `{"VacuumStmt":{}}`,
			kind: Gap, detail: "unsupported statement node VacuumStmt",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Build([]byte(test.tree), bind)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != test.kind || got.Detail != test.detail {
				t.Fatalf("got %s %q, want %s %q", got.Kind, got.Detail, test.kind, test.detail)
			}
			if (got.Statement != nil) != (test.kind == Built) {
				t.Fatalf("statement presence disagrees with kind %s", got.Kind)
			}
			if test.kind != Built {
				return
			}
			sql, _, err := got.Statement.ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			if sql != test.sql {
				t.Fatalf("got %q, want %q", sql, test.sql)
			}
		})
	}
}

func TestBuildBindsSourceParameterNumbers(t *testing.T) {
	t.Parallel()
	// SELECT $3, $1
	tree := `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"ParamRef":{"number":3}}}},{"ResTarget":{"val":{"ParamRef":{"number":1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	got, err := Build([]byte(tree), bind)
	if err != nil {
		t.Fatal(err)
	}
	_, args, err := got.Statement.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != 3 || args[1] != 1 {
		t.Fatalf("arguments %v, want [3 1]", args)
	}
}

func TestBuildRejectsNonTrees(t *testing.T) {
	t.Parallel()
	for _, tree := range []string{
		``, `7`, `[]`, `{"SelectStmt":`,
		`{"SelectStmt":{"op":"SETOP_NONE"}} {}`,
		`{"SelectStmt":{"op":"SETOP_NONE"},"InsertStmt":{}}`,
		`{"SelectStmt":[]}`,
		`{"SelectStmt":{"op":7}}`,
		`{"SelectStmt":{"op":"SETOP_NONE","targetList":{}}}`,
		`{"SelectStmt":{"op":"SETOP_NONE","targetList":[{"ResTarget":{"val":{"ParamRef":{"number":1.5}}}}]}}`,
	} {
		if got, err := Build([]byte(tree), bind); err == nil {
			t.Errorf("tree %q: got %s %q, want an error", tree, got.Kind, got.Detail)
		}
	}
}

// The corpus measures what the typed API can express. A trusted-SQL escape
// hatch would let any query pass, so the adapter must not reach for one.
func TestAdapterAvoidsTrustedSQL(t *testing.T) {
	t.Parallel()
	files := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	selectors := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(files, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			selector, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			selectors++
			// qs marks every trusted-SQL entry point with one of these names.
			if name := selector.Sel.Name; name == "Fragment" || strings.HasSuffix(name, "SQL") {
				t.Errorf("%s: adapter uses trusted SQL through %s", files.Position(selector.Pos()), name)
			}
			return true
		})
	}
	if selectors == 0 {
		t.Fatal("no adapter sources were inspected")
	}
}
