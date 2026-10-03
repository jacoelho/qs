package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedWalkerIncludesNewClassifiedSlice(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["select.go"] = replaceText(t, files["select.go"],
			"type SelectBuilder struct {\n\tcolumns       []Expr",
			"type SelectBuilder struct {\n\tfixture       []Expr\n\tcolumns       []Expr")
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_fixture_test.go", []byte(`package qs

import "testing"

func TestGeneratedFixture(t *testing.T) {
	child := Select(Param("child"))
	original := Select(Scalar(child))
	original.fixture = []Expr{Scalar(child), Scalar(child)}
	clone := Clone(original)
	originalChild := original.columns[0].value.(*subqueryExpression).query
	cloneChild := clone.columns[0].value.(*subqueryExpression).query
	if originalChild == cloneChild {
		t.Fatal("generated clone shares a statement child")
	}
	for i := range clone.fixture {
		if got := clone.fixture[i].value.(*subqueryExpression).query; got != cloneChild {
			t.Fatal("generated clone did not preserve repeated child sharing")
		}
	}
	clone.fixture[0] = LiteralString("clone fixture")
	if original.fixture[0].kind != exprSubquery {
		t.Fatal("generated clone shares classified []Expr storage")
	}
	originalChild.(*SelectBuilder).Columns(Param("original"))
	cloneChild.(*SelectBuilder).Columns(Param("clone"))
	if _, args, err := originalChild.(*SelectBuilder).ToSQL(); err != nil || args[len(args)-1] != "original" {
		t.Fatalf("original child changed through clone: args=%#v err=%v", args, err)
	}
	if _, args, err := cloneChild.(*SelectBuilder).ToSQL(); err != nil || args[len(args)-1] != "clone" {
		t.Fatalf("clone child changed through original: args=%#v err=%v", args, err)
	}
}
`))
	runGoTest(t, dir, "TestGeneratedFixture")
}

func TestGeneratedExprOwnerTraversesNewFields(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["expr.go"] = replaceText(t, files["expr.go"],
			"type Expr struct {\n\tvalue any",
			"type Expr struct {\n\tvalue any\n\tfuture *SelectBuilder")
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_expr_owner_fixture_test.go", []byte(`package qs

import (
	"reflect"
	"testing"
)

func TestGeneratedExprOwnerFixture(t *testing.T) {
	child := Select(Param("child"))
	expression := Scalar(child)
	expression.future = child
	original := Select(expression)
	clone := Clone(original)
	cloneFuture := clone.columns[0].future
	cloneSubquery := clone.columns[0].value.(*subqueryExpression).query.(*SelectBuilder)
	if cloneFuture == child || cloneFuture != cloneSubquery {
		t.Fatal("generated Expr field did not preserve cloned statement sharing")
	}
	beforeSQL, beforeArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	child.Where(Eq("id", "original"))
	afterSQL, afterArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if beforeSQL != afterSQL || !reflect.DeepEqual(beforeArgs, afterArgs) {
		t.Fatal("generated Expr field shares the original child")
	}
	originalSQL, originalArgs, err := original.ToSQL()
	if err != nil || originalSQL == beforeSQL || reflect.DeepEqual(originalArgs, beforeArgs) {
		t.Fatalf("original child did not remain independently mutable: sql=%q args=%#v err=%v", originalSQL, originalArgs, err)
	}
}
`))
	runGoTest(t, dir, "TestGeneratedExprOwnerFixture")
}

func TestGeneratedRelationOwnerTraversesNewFields(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["relation.go"] = replaceText(t, files["relation.go"],
			"type Relation struct {\n\tvalue      any",
			"type Relation struct {\n\tvalue      any\n\tfuture     *SelectBuilder")
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_relation_owner_fixture_test.go", []byte(`package qs

import (
	"reflect"
	"testing"
)

func TestGeneratedRelationOwnerFixture(t *testing.T) {
	child := Select(Param("child"))
	relation := Subquery(child, "s")
	relation.future = child
	original := Select(Star()).FromExpr(relation)
	clone := Clone(original)
	cloneRelation := clone.from[0]
	cloneFuture := cloneRelation.future
	cloneSubquery := cloneRelation.value.(*SelectBuilder)
	if cloneFuture == child || cloneFuture != cloneSubquery {
		t.Fatal("generated Relation field did not preserve cloned statement sharing")
	}
	beforeSQL, beforeArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	child.Where(Eq("id", "original"))
	afterSQL, afterArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if beforeSQL != afterSQL || !reflect.DeepEqual(beforeArgs, afterArgs) {
		t.Fatal("generated Relation field shares the original child")
	}
	originalSQL, originalArgs, err := original.ToSQL()
	if err != nil || originalSQL == beforeSQL || reflect.DeepEqual(originalArgs, beforeArgs) {
		t.Fatalf("original child did not remain independently mutable: sql=%q args=%#v err=%v", originalSQL, originalArgs, err)
	}
}
`))
	runGoTest(t, dir, "TestGeneratedRelationOwnerFixture")
}

func TestGeneratedJSONOwnerTraversesNewFields(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["json_constructor.go"] = replaceText(t, files["json_constructor.go"],
			"type jsonConstructor struct {\n\tpayload any",
			"type jsonConstructor struct {\n\tpayload any\n\tfuture  *SelectBuilder")
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_json_owner_fixture_test.go", []byte(`package qs

import (
	"reflect"
	"testing"
)

func TestGeneratedJSONOwnerFixture(t *testing.T) {
	child := Select(Param("child"))
	expression := JSONObject(JSONPair(Param("key"), Scalar(child))).Expr()
	expression.value.(*jsonConstructor).future = child
	original := Select(expression)
	clone := Clone(original)
	constructor := clone.columns[0].value.(*jsonConstructor)
	cloneFuture := constructor.future
	payload := constructor.payload.(*jsonObjectConstructorPayload)
	cloneSubquery := payload.members[0].value.expr.value.(*subqueryExpression).query.(*SelectBuilder)
	if cloneFuture == child || cloneFuture != cloneSubquery {
		t.Fatal("generated JSON field did not preserve cloned statement sharing")
	}
	beforeSQL, beforeArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	child.Where(Eq("id", "original"))
	afterSQL, afterArgs, err := clone.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if beforeSQL != afterSQL || !reflect.DeepEqual(beforeArgs, afterArgs) {
		t.Fatal("generated JSON field shares the original child")
	}
	originalSQL, originalArgs, err := original.ToSQL()
	if err != nil || originalSQL == beforeSQL || reflect.DeepEqual(originalArgs, beforeArgs) {
		t.Fatalf("original child did not remain independently mutable: sql=%q args=%#v err=%v", originalSQL, originalArgs, err)
	}
}
`))
	runGoTest(t, dir, "TestGeneratedJSONOwnerFixture")
}

func TestGeneratedNestedSliceOwnsStructData(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["select.go"] = replaceText(t, files["select.go"],
			"type SelectBuilder struct {\n\tcolumns       []Expr",
			"type SelectBuilder struct {\n\tfixtureOwned []fixtureOwned\n\tcolumns       []Expr")
		files["select.go"] = append(files["select.go"], []byte(`

type fixtureOwned struct {
	names []string
}
`)...)
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_nested_fixture_test.go", []byte(`package qs

import "testing"

func TestGeneratedNestedFixture(t *testing.T) {
	original := Select(Param(1))
	original.fixtureOwned = []fixtureOwned{{names: []string{"original"}}}
	clone := Clone(original)
	clone.fixtureOwned[0].names[0] = "clone"
	if original.fixtureOwned[0].names[0] != "original" {
		t.Fatal("generated clone shares nested owned slice data")
	}
}
`))
	runGoTest(t, dir, "TestGeneratedNestedFixture")
}

func TestGeneratedNamedArrayTraversesGraph(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["select.go"] = replaceText(t, files["select.go"],
			"type SelectBuilder struct {\n\tcolumns       []Expr",
			"type SelectBuilder struct {\n\tfixtureArray fixtureExprArray\n\tcolumns       []Expr")
		files["select.go"] = append(files["select.go"], []byte(`

type fixtureExprArray [1]Expr
`)...)
	})
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "clone_array_fixture_test.go", []byte(`package qs

import "testing"

func TestGeneratedArrayFixture(t *testing.T) {
	child := Select(Param("child"))
	original := Select(Scalar(child))
	original.fixtureArray = fixtureExprArray{Scalar(child)}
	clone := Clone(original)
	originalChild := original.columns[0].value.(*subqueryExpression).query
	cloneChild := clone.columns[0].value.(*subqueryExpression).query
	fixtureChild := clone.fixtureArray[0].value.(*subqueryExpression).query
	if fixtureChild != cloneChild {
		t.Fatal("generated clone did not preserve sharing through named array")
	}
	clone.fixtureArray[0] = LiteralString("clone")
	if original.fixtureArray[0].kind != exprSubquery {
		t.Fatal("generated clone shares named array storage")
	}
	if originalChild == cloneChild {
		t.Fatal("generated clone shares statement child through named array")
	}
}
`))
	runGoTest(t, dir, "TestGeneratedArrayFixture")
}

func TestNamedPointerFailsGeneration(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["select.go"] = replaceText(t, files["select.go"],
			"type SelectBuilder struct {\n\tcolumns       []Expr",
			"type SelectBuilder struct {\n\tfixturePointer fixturePointer\n\tcolumns       []Expr")
		files["select.go"] = append(files["select.go"], []byte(`

type fixturePointer *fixtureOwned
type fixtureOwned struct{}
`)...)
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "fixturePointer is unclassified") {
		t.Fatalf("run() error = %v, want named pointer rejection", err)
	}
}

func TestUnclassifiedCloneGraphTypesFail(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{name: "interface", field: "fixture interface{}", want: "unclassified anonymous interface"},
		{name: "map", field: "fixture map[string]Expr", want: "unclassified map"},
		{name: "pointer", field: "fixture *error", want: "foreign or unnamed pointer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copyPackage(t, func(files map[string][]byte) {
				files["select.go"] = replaceText(t, files["select.go"],
					"type SelectBuilder struct {\n\tcolumns       []Expr",
					"type SelectBuilder struct {\n\t"+test.field+"\n\tcolumns       []Expr")
			})
			err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestForeignStatementMarkerFails(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["clone_fixture.go"] = []byte(`package qs

type unclassifiedRoot struct{}

func (*unclassifiedRoot) statement() {}
`)
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "does not implement Statement") {
		t.Fatalf("run() error = %v, want statement implementation failure", err)
	}
}

func TestJSONPolicyChangesFailGeneration(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string][]byte)
		want string
	}{
		{
			name: "new enum",
			edit: func(files map[string][]byte) {
				files["json_constructor.go"] = append(files["json_constructor.go"], []byte("\nconst jsonUnclassifiedKind jsonConstructorKind = 99\n")...)
			},
			want: "has no case for jsonUnclassifiedKind",
		},
		{
			name: "new payload",
			edit: func(files map[string][]byte) {
				files["json_constructor.go"] = append(files["json_constructor.go"], []byte(`
type jsonUnclassifiedPayload struct{ value Expr }

func unclassifiedJSON() Expr {
	return jsonConstructorExpr(jsonScalarConstructor, &jsonUnclassifiedPayload{})
}
`)...)
			},
			want: "payload changed",
		},
		{
			name: "changed payload type",
			edit: func(files map[string][]byte) {
				files["json_constructor.go"] = replaceText(t, files["json_constructor.go"],
					"return jsonConstructorExpr(jsonScalarConstructor, &jsonScalarPayload{value: value})",
					"return jsonConstructorExpr(jsonScalarConstructor, &jsonSerializePayload{value: jsonConstructorValue{expr: value}})")
			},
			want: "payload changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copyPackage(t, test.edit)
			err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestExprAndRelationPayloadChangesFailGeneration(t *testing.T) {
	tests := []struct {
		name string
		file string
		old  string
		new  string
		want string
	}{
		{
			name: "expression payload",
			file: "expr.go",
			old:  "return Expr{kind: exprCast, value: &castExpression{expr: e, typ: typ}}",
			new:  "return Expr{kind: exprCast, value: &sliceExpression{value: e}}",
			want: "exprCast payload",
		},
		{
			name: "relation payload",
			file: "relation.go",
			old:  "return Relation{kind: relationJoin, value: &joinExpression{left: left, right: right, kind: kind, natural: natural}}",
			new:  "return Relation{kind: relationJoin, value: &sampleClause{}}",
			want: "relationJoin payload",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copyPackage(t, func(files map[string][]byte) {
				files[test.file] = replaceText(t, files[test.file], test.old, test.new)
			})
			err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want payload rejection", err)
			}
		})
	}
}

func TestExprAssignmentPayloadChangesFailGeneration(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["expr.go"] = append(files["expr.go"], []byte(`

func unclassifiedExprAssignment(e Expr) Expr {
	e.value = &sampleClause{}
	return e
}
`)...)
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "Expr assignment payload type *sampleClause is unclassified") {
		t.Fatalf("run() error = %v, want typed assignment rejection", err)
	}
}

func TestInvalidExpressionShapeChangeFailsGeneration(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["expr.go"] = replaceText(t, files["expr.go"],
			"type invalidExpression struct{ clause, detail string }",
			"type invalidExpression struct { clause, detail string; extra []Expr }")
		files["expr.go"] = replaceText(t, files["expr.go"],
			"&invalidExpression{clause, detail}",
			"&invalidExpression{clause: clause, detail: detail}")
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "clone immutable type invalidExpression shape changed") {
		t.Fatalf("run() error = %v, want invalidExpression shape failure", err)
	}
}

func TestClassifiedUtilitySourceShapeChangeFailsGeneration(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["query_utility.go"] = replaceText(t, files["query_utility.go"],
			"type queryUtilitySource struct {\n\tquery         Rowset\n\texecute       *ExecuteBuilder\n\texecuteSource bool\n}",
			"type queryUtilitySource struct {\n\tquery         Rowset\n\texecute       *ExecuteBuilder\n\texecuteSource bool\n\textra         []Expr\n}")
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "clone classified type queryUtilitySource shape changed") {
		t.Fatalf("run() error = %v, want queryUtilitySource shape failure", err)
	}
}

func TestDataTypeShapeChangeFailsGeneration(t *testing.T) {
	dir := copyPackage(t, func(files map[string][]byte) {
		files["type.go"] = replaceText(t, files["type.go"], "arrays  int", "arrays  int64")
	})
	err := run(config{dir: dir, output: filepath.Join(dir, generatedName)})
	if err == nil || !strings.Contains(err.Error(), "DataType shape changed") {
		t.Fatalf("run() error = %v, want DataType shape failure", err)
	}
}

func TestCheckIsNonDestructiveAndDeterministic(t *testing.T) {
	dir := copyPackage(t, nil)
	out := filepath.Join(dir, generatedName)
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	stale := append([]byte(nil), original...)
	stale[0] = 'x'
	if err := os.WriteFile(out, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(config{dir: dir, output: out, check: true}); err == nil || !strings.Contains(err.Error(), "generated file is stale") {
		t.Fatalf("check error = %v, want stale output error", err)
	}
	afterCheck, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterCheck, stale) {
		t.Fatal("-check rewrote stale generated output")
	}
	if err := run(config{dir: dir, output: out}); err != nil {
		t.Fatal(err)
	}
	regenerated, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(regenerated, original) {
		t.Fatal("regeneration is not deterministic")
	}
	if err := run(config{dir: dir, output: out, check: true}); err != nil {
		t.Fatal(err)
	}
}

func copyPackage(t *testing.T, edit func(map[string][]byte)) string {
	t.Helper()
	source := repositoryRoot(t)
	dir := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == generatedName {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = data
	}
	if edit != nil {
		edit(files)
	}
	for name, data := range files {
		writeFile(t, dir, name, data)
	}
	data, err := os.ReadFile(filepath.Join(source, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "go.mod", data)
	return dir
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func replaceText(t *testing.T, source []byte, old, replacement string) []byte {
	t.Helper()
	text := string(source)
	if !strings.Contains(text, old) {
		t.Fatalf("fixture source does not contain %q", old)
	}
	return []byte(strings.Replace(text, old, replacement, 1))
}

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGoTest(t *testing.T, dir, testName string) {
	t.Helper()
	cache := filepath.Join(t.TempDir(), "cache")
	cmd := exec.CommandContext(t.Context(), "go", "test", ".", "-run", "^"+testName+"$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+cache)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated fixture test failed: %v\n%s", err, output)
	}
}
