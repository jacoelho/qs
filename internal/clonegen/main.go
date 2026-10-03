// Command clonegen generates the typed clone walkers for package qs.
//
// The generator intentionally uses only the standard library. It type-checks
// the package, discovers Statement implementations from their sealed method,
// and follows the concrete fields reachable from those roots. Runtime cloning
// never uses reflection; this program emits ordinary Go assignments and loops.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const generatedName = "clone_generated.go"

const (
	policyApplication     = "application"
	policyExpr            = "Expr"
	policyExprSlice       = "[]Expr"
	policyIdentifierParts = "identifierParts"
	policyNil             = "nil"
	policyRecordFunctions = "[]RecordFunction"
	policyRowset          = "Rowset"
	policyShallow         = "shallow"
	fieldValue            = "value"
	typeStringScalar      = "string"
	typeDataType          = "DataType"
	typeJSONConstructor   = "jsonConstructor"
	typeRelation          = "Relation"
	typeStatement         = "Statement"
)

type config struct {
	dir    string
	output string
	check  bool
}

type packageInfo struct {
	dir      string
	pkg      *types.Package
	files    []*ast.File
	typeInfo *types.Info
	named    map[string]*types.Named
	structs  map[string]*types.Struct
	roots    []string
}

type generator struct {
	info *packageInfo

	// visited contains the named struct types for which a value walker is
	// emitted. The map is also the reachability set used by validation.
	visited map[string]bool

	// These are the three tagged any payloads in the AST. Their cases are
	// deliberately policy, rather than inferred from a switch in clone code:
	// adding a tag or payload therefore fails generation until the policy is
	// reviewed.
	exprPayload     map[string]string
	relationPayload map[string]string
	jsonPayload     map[string]string
	// shallowPayload records the concrete values permitted by Expr cases whose
	// payload is copied without traversal. Application parameters remain
	// intentionally opaque; every other shallow case is checked against a
	// package-owned representation so a new pointer or slice cannot inherit the
	// shallow rule accidentally.
	shallowPayload map[string]string
}

func main() {
	var c config
	flag.StringVar(&c.dir, "dir", ".", "package directory")
	flag.StringVar(&c.output, "output", "", "generated file (default <dir>/clone_generated.go)")
	flag.BoolVar(&c.check, "check", false, "check that generated output is current")
	flag.Parse()
	if c.output == "" {
		c.output = filepath.Join(c.dir, generatedName)
	}
	if err := run(c); err != nil {
		fmt.Fprintln(os.Stderr, "clonegen:", err)
		os.Exit(1)
	}
}

func run(c config) error {
	info, err := loadPackage(c.dir)
	if err != nil {
		return err
	}
	g := newGenerator(info)
	if validateErr := g.validatePolicy(); validateErr != nil {
		return validateErr
	}
	if discoverErr := g.discover(); discoverErr != nil {
		return discoverErr
	}
	data, err := g.generate()
	if err != nil {
		return err
	}
	if c.check {
		current, err := os.ReadFile(c.output)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("generated file is missing: %s", c.output)
			}
			return err
		}
		if !bytes.Equal(current, data) {
			return fmt.Errorf("generated file is stale: %s", c.output)
		}
		return nil
	}
	//nolint:gosec // Generated source uses normal repository file permissions.
	return os.WriteFile(c.output, data, 0o644)
}

func loadPackage(dir string) (*packageInfo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	fs := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == generatedName {
			continue
		}
		file, parseErr := parser.ParseFile(fs, filepath.Join(abs, name), nil, parser.ParseComments)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", name, parseErr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files in %s", abs)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name.Name < files[j].Name.Name })
	// clone.go calls the generated graph entrypoints. During the first
	// generation there is intentionally no generated file to type-check, so
	// provide declarations for those two entrypoints in a synthetic file. The
	// file is never emitted and is skipped once the package has generated code.
	bootstrap, err := parser.ParseFile(fs, "<clonegen-bootstrap>", []byte(`package qs
func (c *cloneContext) statement(s Statement) Statement { return s }
func (c *cloneContext) rowset(s Rowset) Rowset { return s }
`), 0)
	if err != nil {
		return nil, fmt.Errorf("parse generator bootstrap: %w", err)
	}
	files = append(files, bootstrap)
	var typeErrors []error
	conf := types.Config{
		Importer: importer.Default(),
		Error:    func(err error) { typeErrors = append(typeErrors, err) },
	}
	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	pkg, err := conf.Check("github.com/jacoelho/qs", fs, files, info)
	if err != nil {
		if len(typeErrors) != 0 {
			return nil, fmt.Errorf("type-check package: %w", typeErrors[0])
		}
		return nil, fmt.Errorf("type-check package: %w", err)
	}
	p := &packageInfo{
		dir:      abs,
		pkg:      pkg,
		files:    files,
		typeInfo: info,
		named:    make(map[string]*types.Named),
		structs:  make(map[string]*types.Struct),
	}
	for _, name := range pkg.Scope().Names() {
		obj, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok || named.Obj().Pkg() != pkg {
			continue
		}
		p.named[name] = named
		if st, ok := named.Underlying().(*types.Struct); ok {
			p.structs[name] = st
		}
	}
	return p, nil
}

func newGenerator(info *packageInfo) *generator {
	return &generator{
		info:    info,
		visited: make(map[string]bool),
		exprPayload: map[string]string{
			"exprInvalid":         policyShallow,
			"exprIdentifier":      policyShallow,
			"exprIdentifierParts": policyIdentifierParts,
			"exprParameter":       policyShallow,
			"exprRaw":             policyShallow,
			"exprLiteral":         policyShallow,
			"exprKeyword":         policyShallow,
			"exprStringLiteral":   policyShallow,
			"exprBinary":          policyShallow,
			"exprPrefix":          policyShallow,
			"exprPostfix":         policyShallow,
			"exprGroup":           policyExprSlice,
			"exprFragment":        policyExprSlice,
			"exprAlias":           policyShallow,
			"exprCast":            "*castExpression",
			"exprCall":            "*callExpression",
			"exprSpecialCall":     policyShallow,
			"exprSubquery":        "*subqueryExpression",
			"exprExists":          "*subqueryExpression",
			"exprList":            policyExprSlice,
			"exprMembership":      "*membershipExpression",
			"exprBetween":         "*betweenExpression",
			"exprCase":            "*caseExpression",
			"exprQuantified":      policyShallow,
			"exprSubscript":       policyShallow,
			"exprSlice":           "*sliceExpression",
			"exprCollate":         policyShallow,
			"exprSQLJSON":         "*JSONQueryBuilder",
			"exprVersioned":       "*versionedExpression",
			"exprField":           policyShallow,
			"exprFields":          policyShallow,
			"exprSQLSyntax":       "*sqlSyntaxExpression",
			"exprXML":             "*xmlExpression",
			"exprJSONConstructor": "*jsonConstructor",
		},
		relationPayload: map[string]string{
			"relationTable":      policyShallow,
			"relationTableIdent": policyIdentifierParts,
			"relationSubquery":   policyRowset,
			"relationJoin":       "*joinExpression",
			"relationFunction":   "RecordFunction",
			"relationRowsFrom":   policyRecordFunctions,
			"relationSQL":        policyExpr,
			"relationJSONTable":  "*JSONTableBuilder",
			"relationXMLTable":   "*XMLTableBuilder",
		},
		jsonPayload: map[string]string{
			"jsonObjectConstructor":     "*jsonObjectConstructorPayload",
			"jsonArrayConstructor":      "*jsonArrayConstructorPayload",
			"jsonObjectAggregate":       "*jsonObjectAggregatePayload",
			"jsonArrayAggregate":        "*jsonArrayAggregatePayload",
			"jsonArrayQueryConstructor": "*jsonArrayQueryPayload",
			"jsonParseConstructor":      "*jsonParsePayload",
			"jsonScalarConstructor":     "*jsonScalarPayload",
			"jsonSerializeConstructor":  "*jsonSerializePayload",
			"jsonIsPredicate":           "*jsonIsPredicatePayload",
		},
		shallowPayload: map[string]string{
			"exprInvalid":       "*invalidExpression",
			"exprIdentifier":    policyNil,
			"exprParameter":     policyApplication,
			"exprRaw":           policyNil,
			"exprLiteral":       policyNil,
			"exprKeyword":       policyNil,
			"exprStringLiteral": policyNil,
			"exprBinary":        policyNil,
			"exprPrefix":        policyNil,
			"exprPostfix":       policyNil,
			"exprAlias":         policyNil,
			"exprSpecialCall":   policyNil,
			"exprQuantified":    policyNil,
			"exprSubscript":     policyNil,
			"exprCollate":       policyNil,
			"exprField":         policyNil,
			"exprFields":        policyNil,
		},
	}
}

func (g *generator) validatePolicy() error {
	if err := g.validateEnum("exprKind", g.exprPayload); err != nil {
		return err
	}
	if err := g.validateEnum("relationKind", g.relationPayload); err != nil {
		return err
	}
	if err := g.validateEnum("jsonConstructorKind", g.jsonPayload); err != nil {
		return err
	}
	if err := g.validateJSONConstructors(); err != nil {
		return err
	}
	if err := g.validateDataType(); err != nil {
		return err
	}
	if err := g.validateShallowPayloads(); err != nil {
		return err
	}
	if err := g.validateUnionConstructors(policyExpr, "exprKind", g.exprPayload, g.shallowPayload); err != nil {
		return err
	}
	if err := g.validateUnionConstructors(typeRelation, "relationKind", g.relationPayload, nil); err != nil {
		return err
	}
	if err := g.validateUnionAssignments(policyExpr, g.exprPayload, g.shallowPayload); err != nil {
		return err
	}
	if err := g.validateUnionAssignments(typeRelation, g.relationPayload, nil); err != nil {
		return err
	}
	for _, pair := range [][2]string{{policyExpr, fieldValue}, {typeRelation, fieldValue}, {typeJSONConstructor, "payload"}} {
		owner, field := pair[0], pair[1]
		st, ok := g.info.structs[owner]
		if !ok {
			return fmt.Errorf("clone policy owner %s is missing", owner)
		}
		f := findField(st, field)
		if f == nil || !isAny(f.Type()) {
			return fmt.Errorf("clone policy expects %s.%s to remain any", owner, field)
		}
	}
	return nil
}

func (g *generator) validateEnum(typeName string, policy map[string]string) error {
	named, ok := g.info.named[typeName]
	if !ok {
		return fmt.Errorf("clone policy enum %s is missing", typeName)
	}
	actual := make(map[string]bool)
	for _, name := range g.info.pkg.Scope().Names() {
		obj, ok := g.info.pkg.Scope().Lookup(name).(*types.Const)
		if !ok || !types.Identical(obj.Type(), named) {
			continue
		}
		actual[name] = true
	}
	for name := range actual {
		if _, ok := policy[name]; !ok {
			return fmt.Errorf("clone policy %s has no case for %s", typeName, name)
		}
	}
	for name := range policy {
		if !actual[name] {
			return fmt.Errorf("clone policy %s names missing constant %s", typeName, name)
		}
	}
	return nil
}

func (g *generator) validateJSONConstructors() error {
	actual := make(map[string]string)
	for _, file := range g.info.files {
		var firstErr error
		ast.Inspect(file, func(node ast.Node) bool {
			if firstErr != nil {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			fun, ok := call.Fun.(*ast.Ident)
			if !ok || fun.Name != "jsonConstructorExpr" {
				return true
			}
			kindIdent, ok := call.Args[0].(*ast.Ident)
			if !ok {
				firstErr = fmt.Errorf("clone policy JSON constructor kind must be a named constant")
				return true
			}
			constant, ok := g.info.typeInfo.Uses[kindIdent].(*types.Const)
			if !ok {
				firstErr = fmt.Errorf("clone policy JSON constructor kind %s is not a constant", kindIdent.Name)
				return true
			}
			want, classified := g.jsonPayload[constant.Name()]
			if !classified {
				firstErr = fmt.Errorf("clone policy JSON constructor case %s is unclassified", constant.Name())
				return true
			}
			value := g.info.typeInfo.Types[call.Args[1]].Type
			if value == nil {
				firstErr = fmt.Errorf("clone policy JSON constructor case %s payload type is unavailable", constant.Name())
				return true
			}
			got := typeString(value, g.info.pkg)
			if got != want {
				firstErr = fmt.Errorf("clone policy JSON constructor %s payload changed: want %s, have %s", constant.Name(), want, got)
				return true
			}
			actual[constant.Name()] = got
			return true
		})
		if firstErr != nil {
			return firstErr
		}
	}
	if len(actual) != len(g.jsonPayload) {
		return fmt.Errorf("clone policy JSON constructor calls changed: want %d cases, found %d", len(g.jsonPayload), len(actual))
	}
	for kind := range g.jsonPayload {
		if _, ok := actual[kind]; !ok {
			return fmt.Errorf("clone policy JSON constructor case %s has no constructor call", kind)
		}
	}
	return nil
}

func (g *generator) validateDataType() error {
	st, ok := g.info.structs[typeDataType]
	if !ok {
		return fmt.Errorf("clone atomic type DataType is missing")
	}
	want := []struct {
		name string
		typ  string
	}{
		{"name", typeStringScalar}, {"invalid", typeStringScalar}, {"parts", "[]string"}, {"params", "[]int"}, {"arrays", "int"},
	}
	if st.NumFields() != len(want) {
		return fmt.Errorf("clone atomic type DataType shape changed: want %d fields, have %d", len(want), st.NumFields())
	}
	for i, field := range want {
		got := st.Field(i)
		if got.Name() != field.name || typeString(got.Type(), g.info.pkg) != field.typ {
			return fmt.Errorf("clone atomic type DataType shape changed at field %d", i)
		}
	}
	return nil
}

func (g *generator) validateShallowPayloads() error {
	for kind, payload := range g.exprPayload {
		if payload != policyShallow {
			if _, ok := g.shallowPayload[kind]; ok {
				return fmt.Errorf("clone policy shallow payload %s is not a shallow expression case", kind)
			}
			continue
		}
		if _, ok := g.shallowPayload[kind]; !ok {
			return fmt.Errorf("clone policy shallow expression case %s has no payload type", kind)
		}
	}
	for kind := range g.shallowPayload {
		if g.exprPayload[kind] != policyShallow {
			return fmt.Errorf("clone policy shallow payload %s is not declared shallow", kind)
		}
	}

	if err := g.validateImmutableStruct("invalidExpression", []fieldShape{
		{name: "clause", typ: "string"},
		{name: "detail", typ: "string"},
	}); err != nil {
		return err
	}
	if err := g.validateClassifiedStruct("queryUtilitySource", []fieldShape{
		{name: "query", typ: policyRowset},
		{name: "execute", typ: "*ExecuteBuilder"},
		{name: "executeSource", typ: "bool"},
	}); err != nil {
		return err
	}
	for _, file := range g.info.files {
		var firstErr error
		ast.Inspect(file, func(node ast.Node) bool {
			if firstErr != nil {
				return false
			}
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !g.isExprComposite(literal) {
				return true
			}
			kind, ok := g.exprKindLiteral(literal)
			if !ok {
				// Internal render helpers can use an Expr-shaped temporary with
				// no kind. It is not an expression constructor policy boundary.
				return true
			}
			expected, ok := g.shallowPayload[kind]
			if !ok {
				return true
			}
			value := exprCompositeField(literal, fieldValue)
			if err := validateShallowValue(expected, value, g.info.typeInfo, g.info.pkg); err != nil {
				firstErr = fmt.Errorf("clone policy %s payload: %w", kind, err)
			}
			return firstErr == nil
		})
		if firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func (g *generator) validateUnionConstructors(owner, kindName string, policy, shallow map[string]string) error {
	kindType, ok := g.info.named[kindName]
	if !ok {
		return fmt.Errorf("clone policy union %s is missing", kindName)
	}
	for _, file := range g.info.files {
		var firstErr error
		ast.Inspect(file, func(node ast.Node) bool {
			if firstErr != nil {
				return false
			}
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !g.isNamedComposite(literal, owner) {
				return true
			}
			kind, ok := g.unionKindLiteral(literal, kindType)
			if !ok {
				return true
			}
			payload, classified := policy[kind]
			if !classified {
				firstErr = fmt.Errorf("clone policy %s case %s is unclassified", kindName, kind)
				return true
			}
			if payload == policyShallow {
				payload = policyNil
				if shallow != nil {
					payload = shallow[kind]
				}
			}
			if err := validateShallowValue(payload, exprCompositeField(literal, fieldValue), g.info.typeInfo, g.info.pkg); err != nil {
				firstErr = fmt.Errorf("clone policy %s case %s payload: %w", kindName, kind, err)
			}
			return firstErr == nil
		})
		if firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func (g *generator) validateUnionAssignments(owner string, policy, shallow map[string]string) error {
	allowed := make(map[string]bool)
	application := false
	for kind, payload := range policy {
		if payload == policyShallow {
			if shallowType := shallow[kind]; shallowType == policyApplication {
				application = true
				continue
			} else if shallowType != "" {
				allowed[shallowType] = true
				continue
			}
		}
		allowed[payload] = true
	}
	for _, file := range g.info.files {
		var firstErr error
		ast.Inspect(file, func(node ast.Node) bool {
			if firstErr != nil {
				return false
			}
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				selector, ok := lhs.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != fieldValue {
					continue
				}
				if g.selectedNamedType(selector.X) != owner {
					continue
				}
				if len(assign.Rhs) != len(assign.Lhs) {
					firstErr = fmt.Errorf("clone policy %s assignment has unsupported arity", owner)
					return false
				}
				rhsType := g.info.typeInfo.Types[assign.Rhs[i]].Type
				if rhsType == nil {
					firstErr = fmt.Errorf("clone policy %s assignment payload type is unavailable", owner)
					return false
				}
				if isAny(rhsType) {
					if owner == policyExpr && assignmentInParameterBoundary(file, assign) && application {
						continue
					}
					firstErr = fmt.Errorf("clone policy %s assignment payload type any is only allowed at the parameter boundary", owner)
					return false
				}
				if !allowed[typeString(rhsType, g.info.pkg)] {
					firstErr = fmt.Errorf("clone policy %s assignment payload type %s is unclassified", owner, typeString(rhsType, g.info.pkg))
					return false
				}
			}
			return true
		})
		if firstErr != nil {
			return firstErr
		}
	}
	return nil
}

func (g *generator) selectedNamedType(expr ast.Expr) string {
	typ := g.info.typeInfo.Types[expr].Type
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, ok := typ.(*types.Named)
	if !ok || named.Obj().Pkg() != g.info.pkg {
		return ""
	}
	return named.Obj().Name()
}

func assignmentInParameterBoundary(file *ast.File, assignment *ast.AssignStmt) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name == nil || function.Body == nil {
			continue
		}
		if function.Body.Pos() <= assignment.Pos() && assignment.End() <= function.Body.End() {
			return function.Name.Name == "parameter"
		}
	}
	return false
}

type fieldShape struct {
	name string
	typ  string
}

func (g *generator) validateImmutableStruct(name string, want []fieldShape) error {
	return g.validateStructShape("immutable", name, want)
}

func (g *generator) validateClassifiedStruct(name string, want []fieldShape) error {
	return g.validateStructShape("classified", name, want)
}

func (g *generator) validateStructShape(kind, name string, want []fieldShape) error {
	st, ok := g.info.structs[name]
	if !ok {
		return fmt.Errorf("clone %s type %s is missing", kind, name)
	}
	if st.NumFields() != len(want) {
		return fmt.Errorf("clone %s type %s shape changed: want %d fields, have %d", kind, name, len(want), st.NumFields())
	}
	for i, expected := range want {
		field := st.Field(i)
		if field.Name() != expected.name || typeString(field.Type(), g.info.pkg) != expected.typ {
			return fmt.Errorf("clone %s type %s shape changed at field %d", kind, name, i)
		}
	}
	return nil
}

func (g *generator) isExprComposite(literal *ast.CompositeLit) bool {
	return g.isNamedComposite(literal, policyExpr)
}

func (g *generator) isNamedComposite(literal *ast.CompositeLit, name string) bool {
	typ := g.info.typeInfo.Types[literal].Type
	named, ok := typ.(*types.Named)
	return ok && named.Obj().Pkg() == g.info.pkg && named.Obj().Name() == name
}

func (g *generator) exprKindLiteral(literal *ast.CompositeLit) (string, bool) {
	return g.unionKindLiteral(literal, g.info.named["exprKind"])
}

func (g *generator) unionKindLiteral(literal *ast.CompositeLit, kindType *types.Named) (string, bool) {
	field := exprCompositeField(literal, "kind")
	ident, ok := field.(*ast.Ident)
	if !ok {
		return "", false
	}
	constant, ok := g.info.typeInfo.Uses[ident].(*types.Const)
	if !ok || !types.Identical(constant.Type(), kindType) {
		return "", false
	}
	return constant.Name(), true
}

func exprCompositeField(literal *ast.CompositeLit, name string) ast.Expr {
	for _, element := range literal.Elts {
		keyed, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyed.Key.(*ast.Ident)
		if ok && key.Name == name {
			return keyed.Value
		}
	}
	return nil
}

func validateShallowValue(expected string, value ast.Expr, info *types.Info, pkg *types.Package) error {
	if expected == policyApplication {
		return nil
	}
	if value == nil || isNilIdent(value) {
		if expected == policyNil || expected == "*invalidExpression" {
			return nil
		}
		return fmt.Errorf("want %s, have nil", expected)
	}
	actual := info.Types[value].Type
	if actual == nil {
		return fmt.Errorf("cannot determine value type")
	}
	got := typeString(actual, pkg)
	if got != expected {
		return fmt.Errorf("want %s, have %s", expected, got)
	}
	return nil
}

func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

func (g *generator) discover() error {
	for _, name := range g.info.pkg.Scope().Names() {
		obj, ok := g.info.pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok || named.Obj().Pkg() != g.info.pkg || named.TypeParams().Len() != 0 {
			continue
		}
		if hasMethod(types.NewMethodSet(types.NewPointer(named)), "statement") {
			g.info.roots = append(g.info.roots, name)
		}
	}
	sort.Strings(g.info.roots)
	if len(g.info.roots) == 0 {
		return fmt.Errorf("no Statement roots found")
	}
	for _, name := range g.info.roots {
		statement, ok := g.info.named[typeStatement]
		if !ok {
			return fmt.Errorf("type %s has statement marker but Statement is missing", name)
		}
		statementInterface, interfaceOK := statement.Underlying().(*types.Interface)
		if !interfaceOK || !types.Implements(types.NewPointer(g.info.named[name]), statementInterface) {
			return fmt.Errorf("type %s has statement marker but does not implement Statement", name)
		}
		if err := g.visitNamed(name); err != nil {
			return err
		}
	}
	// The tagged unions are reached through any fields, which type information
	// cannot follow. Visit their typed policy cases so ordinary fields inside a
	// payload are still generated and checked.
	for _, typ := range g.exprPayload {
		if err := g.visitPolicyType(typ); err != nil {
			return err
		}
	}
	for _, typ := range g.relationPayload {
		if err := g.visitPolicyType(typ); err != nil {
			return err
		}
	}
	for _, typ := range g.jsonPayload {
		if err := g.visitPolicyType(typ); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) visitPolicyType(typ string) error {
	typ = strings.TrimPrefix(typ, "*")
	if typ == policyShallow || typ == policyIdentifierParts || typ == policyRowset || typ == policyExpr || strings.HasPrefix(typ, "[]") {
		if typ == policyRecordFunctions {
			return g.visitNamed("RecordFunction")
		}
		return nil
	}
	if _, ok := g.info.structs[typ]; !ok {
		return fmt.Errorf("clone policy payload type %s is missing", typ)
	}
	return g.visitNamed(typ)
}

func (g *generator) visitNamed(name string) error {
	if g.visited[name] {
		return nil
	}
	st, ok := g.info.structs[name]
	if !ok {
		return fmt.Errorf("clone graph type %s is missing", name)
	}
	if name == typeDataType {
		return nil
	}
	g.visited[name] = true
	for field := range st.Fields() {
		if isUnionField(name, field.Name()) {
			continue
		}
		if err := g.visitType(field.Type()); err != nil {
			return fmt.Errorf("%s.%s: %w", name, field.Name(), err)
		}
	}
	return nil
}

func (g *generator) visitType(t types.Type) error {
	switch x := t.(type) {
	case *types.Basic:
		return nil
	case *types.Named:
		if x.Obj().Pkg() != g.info.pkg {
			return fmt.Errorf("foreign named type %s is reachable from clone graph", x.String())
		}
		if x.Obj().Name() == typeDataType || isKnownInterfaceName(x.Obj().Name()) {
			return nil
		}
		under := x.Underlying()
		if _, ok := under.(*types.Interface); ok {
			return fmt.Errorf("unclassified interface %s is reachable from clone graph", x.Obj().Name())
		}
		if _, ok := under.(*types.Map); ok {
			return fmt.Errorf("unclassified map %s is reachable from clone graph", x.Obj().Name())
		}
		if _, ok := under.(*types.Struct); ok {
			return g.visitNamed(x.Obj().Name())
		}
		if _, ok := under.(*types.Pointer); ok {
			return fmt.Errorf("named pointer %s is unclassified; use a named struct pointer field", x.String())
		}
		return g.visitType(under)
	case *types.Pointer:
		named, ok := x.Elem().(*types.Named)
		if !ok || named.Obj().Pkg() != g.info.pkg {
			return fmt.Errorf("foreign or unnamed pointer %s is reachable from clone graph", x.String())
		}
		if named.Obj().Name() == typeDataType {
			return fmt.Errorf("pointer to atomic DataType is unclassified")
		}
		if _, ok := named.Underlying().(*types.Struct); !ok {
			return fmt.Errorf("pointer to non-struct %s is unclassified", x.String())
		}
		return g.visitNamed(named.Obj().Name())
	case *types.Slice:
		return g.visitType(x.Elem())
	case *types.Array:
		return g.visitType(x.Elem())
	case *types.Interface:
		return fmt.Errorf("unclassified anonymous interface %s is reachable from clone graph", x.String())
	case *types.Map:
		return fmt.Errorf("unclassified map %s is reachable from clone graph", x.String())
	case *types.TypeParam:
		return fmt.Errorf("unclassified type parameter %s is reachable from clone graph", x.String())
	default:
		return fmt.Errorf("unclassified clone field type %s", t.String())
	}
}

func (g *generator) generate() ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by internal/clonegen; DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package qs")
	fmt.Fprintln(&b)
	g.emitStatementDispatch(&b)
	fmt.Fprintln(&b)
	for _, name := range g.orderedTypes() {
		g.emitValueWalker(&b, name)
		fmt.Fprintln(&b)
	}
	g.emitUnionWalkers(&b)
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w\n%s", err, b.String())
	}
	return formatted, nil
}

func (g *generator) orderedTypes() []string {
	names := make([]string, 0, len(g.visited))
	for name := range g.visited {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (g *generator) emitStatementDispatch(b *bytes.Buffer) {
	fmt.Fprintln(b, "func cloneableStatement(s Statement) bool {")
	fmt.Fprintln(b, "\tswitch b := s.(type) {")
	for _, name := range g.info.roots {
		fmt.Fprintf(b, "\tcase *%s:\n\t\treturn b != nil\n", name)
	}
	fmt.Fprintln(b, "\tdefault:")
	fmt.Fprintln(b, "\t\treturn false")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) statement(s Statement) Statement {")
	fmt.Fprintln(b, "\tif !cloneableStatement(s) {")
	fmt.Fprintln(b, "\t\treturn s")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tif v, ok := c.seen[s]; ok {")
	fmt.Fprintln(b, "\t\treturn v")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tswitch v := s.(type) {")
	for _, name := range g.info.roots {
		fmt.Fprintf(b, "\tcase *%s:\n\t\treturn c.clone%s(v)\n", name, name)
	}
	fmt.Fprintln(b, "\tdefault:")
	fmt.Fprintln(b, "\t\treturn s")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	for _, name := range g.info.roots {
		fmt.Fprintf(b, "func (c *cloneContext) clone%s(b *%s) *%s {\n", name, name, name)
		fmt.Fprintln(b, "\tn := *b")
		fmt.Fprintln(b, "\tc.seen[b] = &n")
		st := g.info.structs[name]
		for field := range st.Fields() {
			if isUnionField(name, field.Name()) {
				continue
			}
			g.emitFieldClone(b, field, "n."+field.Name(), "b."+field.Name(), 1)
		}
		fmt.Fprintln(b, "\treturn &n")
		fmt.Fprintln(b, "}")
		fmt.Fprintln(b)
	}
}

func (g *generator) emitValueWalker(b *bytes.Buffer, name string) {
	if isCustomType(name) || isRoot(g.info.roots, name) {
		return
	}
	fmt.Fprintf(b, "func (c *cloneContext) cloneValue%s(v %s) %s {\n", name, name, name)
	fmt.Fprintln(b, "\tn := v")
	st := g.info.structs[name]
	for field := range st.Fields() {
		if isUnionField(name, field.Name()) {
			continue
		}
		g.emitFieldClone(b, field, "n."+field.Name(), "v."+field.Name(), 1)
	}
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintf(b, "func (c *cloneContext) clonePtr%s(v *%s) *%s {\n", name, name, name)
	fmt.Fprintln(b, "\tif v == nil {")
	fmt.Fprintln(b, "\t\treturn nil")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintf(b, "\tn := c.cloneValue%s(*v)\n", name)
	fmt.Fprintln(b, "\treturn &n")
	fmt.Fprintln(b, "}")
}

func (g *generator) emitFieldClone(b *bytes.Buffer, field *types.Var, dst, src string, indent int) {
	g.emitCloneValue(b, dst, src, field.Type(), indent)
}

// emitCloneValue emits a direct assignment for the field and recursively
// expands slices/arrays. Existing cloneSlice semantics (including nil and
// empty slices) are deliberately retained.
func (g *generator) emitCloneValue(b *bytes.Buffer, dst, src string, t types.Type, indent int) {
	prefix := strings.Repeat("\t", indent)
	if named, ok := t.(*types.Named); ok {
		name := named.Obj().Name()
		if name == typeDataType || isAtomicNamed(named) {
			return
		}
		if name == policyExpr {
			fmt.Fprintf(b, "%s%s = c.expr(%s)\n", prefix, dst, src)
			return
		}
		if name == typeRelation {
			fmt.Fprintf(b, "%s%s = c.relation(%s)\n", prefix, dst, src)
			return
		}
		if name == typeJSONConstructor {
			fmt.Fprintf(b, "%s%s = c.cloneValuejsonConstructor(%s)\n", prefix, dst, src)
			return
		}
		if name == "aggregateTail" {
			fmt.Fprintf(b, "%s%s = c.cloneValue%s(%s)\n", prefix, dst, name, src)
			return
		}
		if name == "queryUtilitySource" {
			fmt.Fprintf(b, "%s%s = c.utilitySource(%s)\n", prefix, dst, src)
			return
		}
		if name == typeStatement {
			fmt.Fprintf(b, "%s%s = c.statement(%s)\n", prefix, dst, src)
			return
		}
		if name == policyRowset {
			fmt.Fprintf(b, "%s%s = c.rowset(%s)\n", prefix, dst, src)
			return
		}
		if _, ok := named.Underlying().(*types.Struct); ok {
			if isRoot(g.info.roots, name) {
				fmt.Fprintf(b, "%s%s = ownedPayload[*%s](c.statement(%s))\n", prefix, dst, name, src)
				return
			}
			fmt.Fprintf(b, "%s%s = c.cloneValue%s(%s)\n", prefix, dst, name, src)
			return
		}
		if array, ok := named.Underlying().(*types.Array); ok {
			for i := range array.Len() {
				g.emitCloneValue(b, fmt.Sprintf("%s[%d]", dst, i), fmt.Sprintf("%s[%d]", src, i), array.Elem(), indent)
			}
			return
		}
		if name == policyIdentifierParts {
			fmt.Fprintf(b, "%s%s = identifierParts(cloneSlice(%s))\n", prefix, dst, src)
			return
		}
		// Named slices are rare in the graph. Preserve their named type while
		// recursively cloning the element values.
		if slice, ok := named.Underlying().(*types.Slice); ok {
			g.emitCloneSlice(b, dst, src, slice.Elem(), indent)
			return
		}
	}
	switch x := t.(type) {
	case *types.Basic:
		return
	case *types.Pointer:
		if named, ok := x.Elem().(*types.Named); ok {
			name := named.Obj().Name()
			if name == policyExpr {
				fmt.Fprintf(b, "%sif %s == nil { %s = nil } else { v := c.expr(*%s); %s = &v }\n", prefix, src, dst, src, dst)
				return
			}
			if name == typeRelation {
				fmt.Fprintf(b, "%sif %s == nil { %s = nil } else { v := c.relation(*%s); %s = &v }\n", prefix, src, dst, src, dst)
				return
			}
			if isRoot(g.info.roots, name) {
				fmt.Fprintf(b, "%s%s = ownedPayload[*%s](c.statement(%s))\n", prefix, dst, name, src)
				return
			}
			fmt.Fprintf(b, "%s%s = c.clonePtr%s(%s)\n", prefix, dst, name, src)
			return
		}
	case *types.Interface:
		// Interfaces are accepted only when their declared role is one of the
		// two statement graph edges. Any fields are tagged-union policy fields
		// and are skipped by their owner walker.
		fmt.Fprintf(b, "%s%s = c.cloneInterface(%s)\n", prefix, dst, src)
		return
	case *types.Slice:
		g.emitCloneSlice(b, dst, src, x.Elem(), indent)
		return
	case *types.Array:
		for i := range x.Len() {
			g.emitCloneValue(b, fmt.Sprintf("%s[%d]", dst, i), fmt.Sprintf("%s[%d]", src, i), x.Elem(), indent)
		}
	}
}

func (g *generator) emitCloneSlice(b *bytes.Buffer, dst, src string, elem types.Type, indent int) {
	prefix := strings.Repeat("\t", indent)
	fmt.Fprintf(b, "%s%s = cloneSlice(%s)\n", prefix, dst, src)
	if !g.needsClone(elem, make(map[string]bool)) {
		return
	}
	index := fmt.Sprintf("i%d", indent)
	fmt.Fprintf(b, "%sfor %s := range %s {\n", prefix, index, dst)
	g.emitCloneValue(b, dst+"["+index+"]", src+"["+index+"]", elem, indent+1)
	fmt.Fprintf(b, "%s}\n", prefix)
}

func (g *generator) needsClone(t types.Type, seen map[string]bool) bool {
	if named, ok := t.(*types.Named); ok {
		name := named.Obj().Name()
		if name == typeDataType || isAtomicNamed(named) {
			return false
		}
		if name == policyExpr || name == typeRelation || name == typeStatement || name == policyRowset {
			return true
		}
		if seen[name] {
			return false
		}
		if _, ok := named.Underlying().(*types.Basic); ok {
			return false
		}
		seen[name] = true
		result := g.needsClone(named.Underlying(), seen)
		delete(seen, name)
		return result
	}
	switch x := t.(type) {
	case *types.Basic:
		return false
	case *types.Pointer:
		return true
	case *types.Slice:
		// A slice owns backing storage even when its elements are scalar. The
		// outer clone must therefore run so a later mutation cannot alias it.
		return true
	case *types.Array:
		return g.needsClone(x.Elem(), seen)
	case *types.Interface:
		return true
	case *types.Struct:
		for field := range x.Fields() {
			// The three any payloads are handled by their tagged union walkers.
			if isUnionField("", field.Name()) {
				continue
			}
			if g.needsClone(field.Type(), seen) {
				return true
			}
		}
	}
	return false
}

func (g *generator) emitUnionOwnerFields(b *bytes.Buffer, owner, dst, src string) {
	st := g.info.structs[owner]
	fields := make([]*types.Var, 0, st.NumFields())
	for field := range st.Fields() {
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name() < fields[j].Name() })
	for _, field := range fields {
		destination := dst + "." + field.Name()
		source := src + "." + field.Name()
		if !isUnionField(owner, field.Name()) {
			g.emitFieldClone(b, field, destination, source, 1)
			continue
		}
		switch owner {
		case policyExpr:
			fmt.Fprintf(b, "\tn.%s = c.cloneExprPayload(%s.kind, %s.%s)\n", field.Name(), src, src, field.Name())
		case typeRelation:
			fmt.Fprintf(b, "\tn.%s = c.cloneRelationPayload(%s.kind, %s.%s)\n", field.Name(), src, src, field.Name())
		case typeJSONConstructor:
			fmt.Fprintf(b, "\tn.%s = c.cloneJSONPayload(%s.kind, %s.%s)\n", field.Name(), src, src, field.Name())
		}
	}
}

//nolint:funlen // Erased union dispatch is emitted from one policy boundary.
func (g *generator) emitUnionWalkers(b *bytes.Buffer) {
	// Expr is the only value whose payload is both an arbitrary application
	// value and a tagged internal union. The generated switch keeps the old
	// shallow application-value rule while making every internal case explicit.
	fmt.Fprintln(b, "func (c *cloneContext) expr(e Expr) Expr {")
	fmt.Fprintln(b, "\tn := e")
	g.emitUnionOwnerFields(b, policyExpr, "n", "e")
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) cloneExprPayload(kind exprKind, value any) any {")
	fmt.Fprintln(b, "\tswitch kind {")
	for _, name := range sortedPolicyKeys(g.exprPayload) {
		payload := g.exprPayload[name]
		fmt.Fprintf(b, "\tcase %s:\n", name)
		switch payload {
		case policyShallow:
			fmt.Fprintln(b, "\t\treturn value")
		case policyIdentifierParts:
			fmt.Fprintln(b, "\t\treturn identifierParts(cloneSlice(ownedPayload[identifierParts](value)))")
		case policyExprSlice:
			fmt.Fprintln(b, "\t\treturn c.cloneExprs(ownedPayload[[]Expr](value))")
		default:
			if pointer, ok := strings.CutPrefix(payload, "*"); ok {
				fmt.Fprintf(b, "\t\treturn c.clonePtr%s(ownedPayload[%s](value))\n", pointer, payload)
			} else {
				fmt.Fprintf(b, "\t\treturn c.cloneValue%s(ownedPayload[%s](value))\n", payload, payload)
			}
		}
	}
	fmt.Fprintln(b, "\tdefault:")
	fmt.Fprintln(b, "\t\treturn value")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)

	fmt.Fprintln(b, "func (c *cloneContext) cloneExprs(v []Expr) []Expr {")
	fmt.Fprintln(b, "\tn := cloneSlice(v)")
	fmt.Fprintln(b, "\tfor i := range n {")
	fmt.Fprintln(b, "\t\tn[i] = c.expr(v[i])")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)

	// Relation has a second tagged any payload. Its invalid/unknown kind is
	// preserved shallowly so renderer validation remains the authority for
	// malformed descriptors.
	fmt.Fprintln(b, "func (c *cloneContext) relation(r Relation) Relation {")
	fmt.Fprintln(b, "\tn := r")
	g.emitUnionOwnerFields(b, typeRelation, "n", "r")
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) cloneRelationPayload(kind relationKind, value any) any {")
	fmt.Fprintln(b, "\tif value == nil {")
	fmt.Fprintln(b, "\t\treturn nil")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "\tswitch kind {")
	for _, name := range sortedPolicyKeys(g.relationPayload) {
		payload := g.relationPayload[name]
		fmt.Fprintf(b, "\tcase %s:\n", name)
		switch payload {
		case policyShallow:
			fmt.Fprintln(b, "\t\treturn value")
		case policyIdentifierParts:
			fmt.Fprintln(b, "\t\treturn identifierParts(cloneSlice(ownedPayload[identifierParts](value)))")
		case policyRowset:
			fmt.Fprintln(b, "\t\treturn ownedPayload[Rowset](c.rowset(ownedPayload[Rowset](value)))")
		case policyExpr:
			fmt.Fprintln(b, "\t\treturn c.expr(ownedPayload[Expr](value))")
		case policyRecordFunctions:
			fmt.Fprintln(b, "\t\tn := cloneSlice(ownedPayload[[]RecordFunction](value))")
			fmt.Fprintln(b, "\t\tfor i := range n { n[i] = c.cloneValueRecordFunction(n[i]) }")
			fmt.Fprintln(b, "\t\treturn n")
		default:
			if pointer, ok := strings.CutPrefix(payload, "*"); ok {
				fmt.Fprintf(b, "\t\treturn c.clonePtr%s(ownedPayload[%s](value))\n", pointer, payload)
			} else {
				fmt.Fprintf(b, "\t\treturn c.cloneValue%s(ownedPayload[%s](value))\n", payload, payload)
			}
		}
	}
	fmt.Fprintln(b, "\tdefault:")
	fmt.Fprintln(b, "\t\treturn value")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)

	// SQL/JSON constructors have a third tagged any payload. The concrete
	// payload pointer is copied through generated typed walkers.
	fmt.Fprintln(b, "func (c *cloneContext) cloneValuejsonConstructor(v jsonConstructor) jsonConstructor {")
	fmt.Fprintln(b, "\tn := v")
	g.emitUnionOwnerFields(b, typeJSONConstructor, "n", "v")
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) clonePtrjsonConstructor(v *jsonConstructor) *jsonConstructor {")
	fmt.Fprintln(b, "\tif v == nil { return nil }")
	fmt.Fprintln(b, "\tn := c.cloneValuejsonConstructor(*v)")
	fmt.Fprintln(b, "\treturn &n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) cloneJSONPayload(kind jsonConstructorKind, value any) any {")
	fmt.Fprintln(b, "\tswitch kind {")
	for _, name := range sortedPolicyKeys(g.jsonPayload) {
		payload := g.jsonPayload[name]
		fmt.Fprintf(b, "\tcase %s:\n\t\treturn c.clonePtr%s(ownedPayload[%s](value))\n", name, strings.TrimPrefix(payload, "*"), payload)
	}
	fmt.Fprintln(b, "\tdefault:")
	fmt.Fprintln(b, "\t\treturn value")
	fmt.Fprintln(b, "\t}")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)

	// Rows and assignments are shared helper roles. Keeping these names stable
	// makes the generated code easy to inspect and lets API roles add fields
	// without adding handwritten clone logic.
	fmt.Fprintln(b, "func (c *cloneContext) rows(v [][]Expr) [][]Expr {")
	fmt.Fprintln(b, "\tn := cloneSlice(v)")
	fmt.Fprintln(b, "\tfor i := range n { n[i] = c.cloneExprs(v[i]) }")
	fmt.Fprintln(b, "\treturn n")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) exprs(v []Expr) []Expr { return c.cloneExprs(v) }")
	fmt.Fprintln(b)
	fmt.Fprintln(b, "func (c *cloneContext) rowset(s Rowset) Rowset {")
	fmt.Fprintln(b, "\tif s == nil { return nil }")
	fmt.Fprintln(b, "\treturn ownedPayload[Rowset](c.statement(s))")
	fmt.Fprintln(b, "}")
	fmt.Fprintln(b)
}

func sortedPolicyKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func findField(st *types.Struct, name string) *types.Var {
	for field := range st.Fields() {
		if field.Name() == name {
			return field
		}
	}
	return nil
}

func hasMethod(methods *types.MethodSet, name string) bool {
	for method := range methods.Methods() {
		if method.Obj().Name() != name {
			continue
		}
		sig, ok := method.Obj().Type().(*types.Signature)
		if ok && sig.Params().Len() == 0 && sig.Results().Len() == 0 {
			return true
		}
	}
	return false
}

func isKnownInterfaceName(name string) bool { return name == typeStatement || name == policyRowset }

func isAny(t types.Type) bool {
	return t == types.Universe.Lookup("any").Type() || t.String() == "interface{}"
}

func isUnionField(owner, field string) bool {
	return owner == policyExpr && field == fieldValue || owner == typeRelation && field == fieldValue || owner == typeJSONConstructor && field == "payload"
}

func isCustomType(name string) bool {
	switch name {
	case policyExpr, typeRelation, typeJSONConstructor, "queryUtilitySource":
		return true
	default:
		return false
	}
}

func isRoot(roots []string, name string) bool {
	return slices.Contains(roots, name)
}

func isAtomicNamed(named *types.Named) bool {
	if named.Obj().Name() == typeDataType {
		return true
	}
	_, ok := named.Underlying().(*types.Basic)
	return ok
}

func typeString(t types.Type, pkg *types.Package) string {
	return types.TypeString(t, func(other *types.Package) string {
		if other == pkg {
			return ""
		}
		return other.Name()
	})
}
