// Package discarded reports calls into github.com/jacoelho/qs whose result is
// thrown away.
//
// qs builders are immutable values: every method returns a new builder and
// leaves its receiver unchanged. A statement such as
//
//	q.Where(qs.Col("deleted_at").IsNull())
//
// therefore adds no filter, and nothing at build or render time can tell. The
// core package performs no I/O and keeps no state, so a call whose results are
// all discarded has no effect whatever it is; the analyzer reports each one.
//
// A call assigned to the blank identifier is deliberate and is not reported.
// MustToSQL is exempt, because a test may call it only to learn whether it
// panics. The projection package is not checked: its row readers have effects.
package discarded

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// Analyzer reports qs calls whose result is discarded.
var Analyzer = &analysis.Analyzer{
	Name: "qsdiscarded",
	Doc:  "report calls into github.com/jacoelho/qs whose result is discarded; builders are values, so such a call has no effect",
	URL:  "https://pkg.go.dev/github.com/jacoelho/qs/qsvet/discarded",
	Run:  run,
}

// corePath is matched as text: the analyzer needs no qs code, only its name.
const corePath = "github.com/jacoelho/qs"

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			if statement, ok := n.(*ast.ExprStmt); ok {
				check(pass, statement)
			}
			return true
		})
	}
	return nil, nil //nolint:nilnil // the analysis framework requires a nil result when ResultType is unset
}

func check(pass *analysis.Pass, statement *ast.ExprStmt) {
	call, ok := ast.Unparen(statement.X).(*ast.CallExpr)
	if !ok {
		return
	}
	fn := callee(pass.TypesInfo, call)
	// Unexported functions are qs internals, which may mutate render state.
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != corePath || !fn.Exported() {
		return
	}
	signature := fn.Signature()
	if signature.Results().Len() == 0 {
		return
	}
	if signature.Recv() == nil && fn.Name() == "MustToSQL" {
		return
	}
	pass.ReportRangef(call, "result of %s is discarded: qs builders are values, so this call has no effect", name(fn, signature))
}

// callee is the declared function or method a call names, or nil for a call
// through a function value. Origin undoes instantiation, so a generic method
// reports as declared.
func callee(info *types.Info, call *ast.CallExpr) *types.Func {
	fun := ast.Unparen(call.Fun)
	switch instantiated := fun.(type) {
	case *ast.IndexExpr:
		fun = ast.Unparen(instantiated.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(instantiated.X)
	}
	var id *ast.Ident
	switch named := fun.(type) {
	case *ast.Ident:
		id = named
	case *ast.SelectorExpr:
		id = named.Sel
	default:
		return nil
	}
	fn, ok := info.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	return fn.Origin()
}

// name is qs.Func for a function and qs.Type.Method for a method.
func name(fn *types.Func, signature *types.Signature) string {
	if recv := signature.Recv(); recv != nil {
		t := recv.Type()
		if pointer, ok := t.(*types.Pointer); ok {
			t = pointer.Elem()
		}
		if named, ok := t.(*types.Named); ok {
			return "qs." + named.Obj().Name() + "." + fn.Name()
		}
	}
	return "qs." + fn.Name()
}
