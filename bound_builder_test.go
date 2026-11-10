package qs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// A builder still needs Expr, End or Condition to become an expression. Passed
// without it where a value is bound, it must fail rendering as an expression
// does, not reach the driver as an argument.
func TestUnconvertedBuilderIsNotBoundAsAValue(t *testing.T) {
	t.Parallel()

	doc, path := Col("doc"), LiteralString("$")
	for name, statement := range map[string]Statement{
		"json_object":       Values(1, JSONObject()),
		"json_value":        Select(Param(JSONValue(doc, path))),
		"json_exists":       Select(Param(JSONExists(doc, path))),
		"json_formatted":    Select(Param(FormatJSON(doc))),
		"searched_case":     Values(Case().When(Col("a").IsNull(), LiteralInt(1))),
		"simple_case":       Values(CaseOf(Col("a")).WhenValue(LiteralInt(1), LiteralInt(2))),
		"pending_case":      Values(Case()),
		"pending_case_of":   Values(CaseOf(Col("a"))),
		"tuple":             Select(Param(Tuple2(TypedCol[int]("a"), TypedCol[int]("b")))),
		"pointer_to_object": Select(Param(new(JSONObject()))),
		"seek_after":        Select(Col("t", "a")).From("t").OrderBy(Col("t", "a").Asc()).SeekAfter(JSONObject()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}

// Every type with a zero-argument Expr, End or Condition method carries the
// marker parameter refuses. A pending stage that reaches an expression only
// through a later builder is outside this scan and is marked by hand.
func TestEveryExpressionBuilderCarriesTheMarker(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	converts, marked := map[string]string{}, map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			method, ok := declaration.(*ast.FuncDecl)
			if !ok || method.Recv == nil {
				continue
			}
			receiver := receiverName(method.Recv.List[0].Type)
			switch name := method.Name.Name; name {
			case "qxExpression":
				marked[receiver] = true
			case "Expr", "End", "Condition":
				if method.Type.Params.NumFields() == 0 && ast.IsExported(receiver) {
					converts[receiver] = name
				}
			}
		}
	}
	if len(converts) < 20 {
		t.Fatalf("found only %d converting types; the scan is broken", len(converts))
	}
	for receiver, method := range converts {
		if !marked[receiver] {
			t.Errorf("%s has %s but no qxExpression marker: Param(%s) would bind it", receiver, method, receiver)
		}
	}
}

func receiverName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverName(e.X)
	case *ast.IndexExpr:
		return receiverName(e.X)
	case *ast.IndexListExpr:
		return receiverName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}
