package qs

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type exprKind uint8

const (
	exprInvalid exprKind = iota
	exprIdentifier
	exprIdentifierParts
	exprParameter
	exprRaw
	exprLiteral
	exprKeyword
	exprStringLiteral
	exprBinary
	exprPrefix
	exprPostfix
	exprGroup
	exprFragment
	exprAlias
	exprCast
	exprCall
	exprSpecialCall
	exprSubquery
	exprExists
	exprList
	exprMembership
	exprBetween
	exprCase
	exprQuantified
	exprSubscript
	exprSlice
	exprCollate
	exprSQLJSON
	exprVersioned
	exprField
	exprFields
	exprSQLSyntax
	exprXML
	exprJSONConstructor
)

// Expr is an immutable SQL expression. A zero Expr is invalid, never an omitted
// filter. Use Param for values, Col/Ident for identifiers, and UnsafeSQL only
// for trusted syntax. It represents a SQL expression, not a schema proof.
type Expr struct {
	kind  exprKind
	text  string
	value any
	node  *expression
}

type expression struct{ left, right Expr }
type invalidExpression struct{ clause, detail string }
type castExpression struct {
	expr Expr
	typ  DataType
}
type subqueryExpression struct {
	query         Rowset
	expectedWidth int
}
type membershipExpression struct {
	left   Expr
	values []Expr
	query  Rowset
	width  int
}
type betweenExpression struct{ value, lower, upper Expr }
type sliceExpression struct {
	value, lower, upper Expr
	hasLower, hasUpper  bool
}

func (Expr) qxExpression()      {}
func (Condition) qxExpression() {}
func (Null[T]) qxNull()         {}

func invalidExpr(clause, detail string) Expr {
	return Expr{kind: exprInvalid, value: &invalidExpression{clause, detail}}
}

// Param binds exactly one value. It does not inspect pointers, expand slices,
// invoke driver.Valuer or call a codec. Use ParamNull for qs.Null values and In
// for expanded lists. The value must not be mutated before execution completes.
func Param[T any](value T) Expr { return parameter(value) }

func parameter(value any) Expr {
	switch value.(type) {
	case interface{ qxExpression() }, Statement:
		return invalidExpr("parameter", "an expression or statement cannot be bound as a value")
	case interface{ optionalValue() }:
		return invalidExpr("parameter", "test Optional.Present before binding its Value")
	case interface{ qxNull() }:
		return invalidExpr("parameter", "use ParamNull to bind qs.Null")
	}
	return Expr{kind: exprParameter, value: value}
}

func ParamNull[T any](value Null[T]) Expr {
	if !value.Valid {
		return parameter(nil)
	}
	return parameter(value.Value)
}

// ArrayParam binds one driver-encoded array, with an explicit PostgreSQL cast.
// The driver must support encoding []T; qs does not convert or copy the slice.
func ArrayParam[T any](values []T, element DataType) Expr {
	return Param(values).Cast(ArrayType(element))
}

// NullExpr is a literal, explicitly typed SQL NULL and has no parameter.
func NullExpr(typ DataType) Expr { return NullLiteral().Cast(typ) }
func NullLiteral() Expr          { return Expr{kind: exprLiteral, text: "NULL"} }

// Default requests the destination column's default; it is not a literal value.
func Default() Expr { return Expr{kind: exprKeyword, text: "DEFAULT"} }

// LiteralInt is for SQL grammar positions such as an ordinal. Normal application
// values should use Param so the query shape is independent of their values.
func LiteralInt(value int64) Expr {
	return Expr{kind: exprLiteral, text: strconv.FormatInt(value, 10)}
}
func LiteralBool(value bool) Expr {
	if value {
		return Expr{kind: exprLiteral, text: "TRUE"}
	}
	return Expr{kind: exprLiteral, text: "FALSE"}
}
func LiteralFloat(value float64) Expr {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return invalidExpr("literal", "non-finite float literal; bind it instead")
	}
	return Expr{kind: exprLiteral, text: strconv.FormatFloat(value, 'g', -1, 64)}
}

// LiteralString emits an escaped PostgreSQL E-string. Prefer Param for normal
// values. E-strings are independent of standard_conforming_strings settings.
func LiteralString(value string) Expr { return Expr{kind: exprStringLiteral, text: value} }

// UnsafeSQL accepts trusted SQL syntax. It does not process placeholders or
// parse statements. Never pass request data or manually numbered bind slots.
func UnsafeSQL(trusted string) Expr { return Expr{kind: exprRaw, text: trusted} }

// Fragment concatenates expressions with no implicit separators or parentheses.
// SQL syntax must be explicit UnsafeSQL parts; values remain Param expressions.
func Fragment(parts ...Expr) Expr {
	if len(parts) == 0 {
		return invalidExpr("fragment", "empty SQL fragment")
	}
	return Expr{kind: exprFragment, value: cloneSlice(parts)}
}

// Parenthesized preserves an expression boundary. For example, a.Index(i).
// Parenthesized().Index(j) indexes the result of a[i], whereas a.Index(i).
// Index(j) applies a multidimensional subscript to a.
func (e Expr) Parenthesized() Expr {
	return Expr{kind: exprGroup, value: []Expr{e}}
}

// StatementSQL is an extension point for trusted statement syntax, not a parser.
// It intentionally does not implement Rowset; subqueries should be structural.
func StatementSQL(parts ...Expr) *SQLStatement { return &SQLStatement{expr: Fragment(parts...)} }

type SQLStatement struct{ expr Expr }

func (e Expr) As(alias string) Expr {
	return Expr{kind: exprAlias, text: alias, node: &expression{left: e}}
}
func (e Expr) Cast(typ DataType) Expr {
	return Expr{kind: exprCast, value: &castExpression{expr: e, typ: typ}}
}

// Field selects one literal field name from a composite value. Col("a.b")
// instead refers to a qualified column; these have different PostgreSQL rules.
func (e Expr) Field(name string) Expr {
	return Expr{kind: exprField, text: name, node: &expression{left: e}}
}

// Fields expands a composite value in PostgreSQL expression-list positions.
// Its width is unknown until PostgreSQL resolves the composite type.
func (e Expr) Fields() Expr {
	return Expr{kind: exprFields, node: &expression{left: e}}
}
func (e Expr) Collate(name string) Expr {
	return Expr{kind: exprCollate, text: name, node: &expression{left: e}}
}

// Scalar requires one projected column when the projection width is known.
// PostgreSQL enforces the scalar subquery's run-time row count.
func Scalar(query Rowset) Expr {
	return Expr{kind: exprSubquery, value: &subqueryExpression{query: query, expectedWidth: 1}}
}

func binary(left Expr, operator string, right Expr) Expr {
	return Expr{kind: exprBinary, text: operator, node: &expression{left, right}}
}
func prefix(operator string, right Expr) Expr {
	return Expr{kind: exprPrefix, text: operator, node: &expression{right: right}}
}
func postfix(left Expr, operator string) Expr {
	return Expr{kind: exprPostfix, text: operator, node: &expression{left: left}}
}
func listExpr(prefix string, items []Expr) Expr {
	return Expr{kind: exprList, text: prefix, value: cloneSlice(items)}
}

func (e Expr) Add(other Expr) Expr        { return binary(e, "+", other) }
func (e Expr) Sub(other Expr) Expr        { return binary(e, "-", other) }
func (e Expr) Mul(other Expr) Expr        { return binary(e, "*", other) }
func (e Expr) Div(other Expr) Expr        { return binary(e, "/", other) }
func (e Expr) Mod(other Expr) Expr        { return binary(e, "%", other) }
func (e Expr) Concat(other Expr) Expr     { return binary(e, "||", other) }
func (e Expr) Negate() Expr               { return prefix("-", e) }
func (e Expr) BitAnd(other Expr) Expr     { return binary(e, "&", other) }
func (e Expr) BitOr(other Expr) Expr      { return binary(e, "|", other) }
func (e Expr) BitXor(other Expr) Expr     { return binary(e, "#", other) }
func (e Expr) BitNot() Expr               { return prefix("~", e) }
func (e Expr) ShiftLeft(other Expr) Expr  { return binary(e, "<<", other) }
func (e Expr) ShiftRight(other Expr) Expr { return binary(e, ">>", other) }
func (e Expr) AtTimeZone(zone Expr) Expr  { return binary(e, "AT TIME ZONE", zone) }

// Index is PostgreSQL subscripting (SQL arrays are conventionally 1-based).
func (e Expr) Index(index Expr) Expr {
	return Expr{kind: exprSubscript, node: &expression{left: e, right: index}}
}
func (e Expr) Slice(lower, upper Expr) Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{e, lower, upper, true, true}}
}
func (e Expr) SliceFrom(lower Expr) Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{value: e, lower: lower, hasLower: true}}
}
func (e Expr) SliceTo(upper Expr) Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{value: e, upper: upper, hasUpper: true}}
}

// SliceAll emits [:], retaining PostgreSQL's full-slice semantics.
func (e Expr) SliceAll() Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{value: e}}
}

// Operator uses a custom symbolic PostgreSQL operator. It validates the operator
// alphabet and rejects comment delimiters; words require dedicated helpers.
func Operator(left Expr, operator string, right Expr) Expr {
	if !validOperator(operator) {
		return invalidExpr("operator", "invalid symbolic operator")
	}
	return binary(left, operator, right)
}

// PrefixOperator applies a validated symbolic unary operator, including
// PostgreSQL's square-root and cube-root operators or extension operators.
func PrefixOperator(operator string, operand Expr) Expr {
	if !validOperator(operator) {
		return invalidExpr("operator", "invalid symbolic operator")
	}
	return prefix(operator, operand)
}

func validOperator(op string) bool {
	if op == "" || strings.Contains(op, "--") || strings.Contains(op, "/*") {
		return false
	}
	for _, c := range op {
		if !strings.ContainsRune("+-*/<>=~!@#%^&|`?", c) {
			return false
		}
	}
	return true
}

// QualifiedOperator supports extension operators selected by schema.
func QualifiedOperator(left Expr, schema, operator string, right Expr) Expr {
	if !validOperator(operator) {
		return invalidExpr("operator", "invalid symbolic operator")
	}
	return Fragment(UnsafeSQL("("), left, UnsafeSQL(" OPERATOR("), Ident(schema), UnsafeSQL("."+operator+") "), right, UnsafeSQL(")"))
}

func (w *renderer) exprs(exprs []Expr, separator string) {
	for i, e := range exprs {
		if i != 0 {
			w.text(separator)
		}
		w.expr(e)
		if w.err != nil {
			return
		}
	}
}

func (w *renderer) expr(e Expr) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	switch e.kind {
	case exprInvalid:
		if v, ok := e.value.(*invalidExpression); ok {
			w.fail(ErrInvalid, v.clause, v.detail)
		} else {
			w.fail(ErrInvalid, "expression", "zero expression")
		}
	case exprIdentifier:
		w.identifierPath(e.text, true)
	case exprIdentifierParts:
		// The names live in a dedicated payload to avoid splitting dotted paths.
		w.identifierParts(e)
	case exprParameter:
		w.bind(e.value)
	case exprRaw:
		if w.require(strings.TrimSpace(e.text) != "" && strings.IndexByte(e.text, 0) < 0 && utf8.ValidString(e.text), "SQL", "empty, NUL-containing or invalid UTF-8 raw SQL") {
			w.text(e.text)
		}
	case exprLiteral, exprKeyword:
		w.text(e.text)
	case exprStringLiteral:
		if !w.require(utf8.ValidString(e.text) && strings.IndexByte(e.text, 0) < 0, "literal", "invalid UTF-8 or NUL in string literal") {
			return
		}
		w.text("E'")
		for i := range len(e.text) {
			switch e.text[i] {
			case '\\':
				w.text("\\\\")
			case '\'':
				w.text("''")
			default:
				w.byte(e.text[i])
			}
		}
		w.byte('\'')
	case exprBinary:
		w.byte('(')
		w.expr(e.node.left)
		w.byte(' ')
		w.text(e.text)
		w.byte(' ')
		w.expr(e.node.right)
		w.byte(')')
	case exprPrefix:
		w.byte('(')
		w.text(e.text)
		w.byte(' ')
		w.expr(e.node.right)
		w.byte(')')
	case exprPostfix:
		w.byte('(')
		w.expr(e.node.left)
		w.byte(' ')
		w.text(e.text)
		w.byte(')')
	case exprGroup:
		w.byte('(')
		w.exprs(e.value.([]Expr), e.text)
		w.byte(')')
	case exprFragment:
		w.exprs(e.value.([]Expr), "")
	case exprAlias:
		w.expr(e.node.left)
		w.text(" AS ")
		w.identifierPart(e.text)
	case exprCast:
		n := e.value.(*castExpression)
		w.byte('(')
		w.expr(n.expr)
		w.text(")::")
		w.dataType(n.typ)
	case exprField, exprFields:
		w.indirectionBase(e.node.left)
		w.byte('.')
		if e.kind == exprField {
			w.identifierPart(e.text)
		} else {
			w.byte('*')
		}
	case exprCall:
		w.call(e)
	case exprSpecialCall:
		w.specialCall(e)
	case exprSQLSyntax:
		w.sqlSyntax(e)
	case exprXML:
		w.xml(e)
	case exprJSONConstructor:
		w.jsonConstructor(e)
	case exprSubquery, exprExists:
		n := e.value.(*subqueryExpression)
		width := statementWidth(n.query, w.options.MaxDepth)
		if n.expectedWidth > 0 && width >= 0 && width != n.expectedWidth {
			w.fail(ErrInvalid, "subquery", "projection has the wrong number of columns")
			return
		}
		if e.kind == exprExists {
			w.text(e.text)
		}
		w.byte('(')
		w.statement(n.query)
		w.byte(')')
	case exprList:
		w.text(e.text)
		if e.text == "ARRAY" {
			w.byte('[')
		} else {
			w.byte('(')
		}
		w.exprs(e.value.([]Expr), ", ")
		if e.text == "ARRAY" {
			w.byte(']')
		} else {
			w.byte(')')
		}
	case exprMembership:
		w.membership(e)
	case exprBetween:
		n := e.value.(*betweenExpression)
		w.byte('(')
		w.expr(n.value)
		w.byte(' ')
		w.text(e.text)
		w.byte(' ')
		w.expr(n.lower)
		w.text(" AND ")
		w.expr(n.upper)
		w.byte(')')
	case exprCase:
		w.caseExpr(e.value.(*CaseBuilder))
	case exprQuantified:
		w.text(e.text)
		w.byte('(')
		w.expr(e.node.left)
		w.byte(')')
	case exprSubscript:
		w.indirectionBase(e.node.left)
		w.byte('[')
		w.expr(e.node.right)
		w.byte(']')
	case exprSlice:
		n := e.value.(*sliceExpression)
		w.indirectionBase(n.value)
		w.byte('[')
		if n.hasLower {
			w.expr(n.lower)
		}
		w.byte(':')
		if n.hasUpper {
			w.expr(n.upper)
		}
		w.byte(']')
	case exprCollate:
		w.byte('(')
		w.expr(e.node.left)
		w.text(" COLLATE ")
		w.identifierPath(e.text, false)
		w.byte(')')
	case exprSQLJSON:
		w.sqlJSON(e)
	case exprVersioned:
		n := e.value.(*versionedExpression)
		w.feature(n.version, n.feature)
		w.expr(n.expr)
	default:
		w.fail(ErrInvalid, "expression", "unknown expression kind")
	}
}

func (w *renderer) indirectionBase(e Expr) {
	switch e.kind {
	case exprField, exprFields, exprSubscript, exprSlice, exprGroup:
		w.expr(e)
	default:
		w.byte('(')
		w.expr(e)
		w.byte(')')
	}
}

type identifierParts []string

func (w *renderer) identifierParts(e Expr) {
	parts := e.value.(identifierParts)
	if !w.require(len(parts) > 0, "identifier", "empty identifier") {
		return
	}
	for i, part := range parts {
		if i != 0 {
			w.byte('.')
		}
		w.identifierPart(part)
	}
}

type versionedExpression struct {
	version PostgreSQLVersion
	feature string
	expr    Expr
}

func versionExpression(version PostgreSQLVersion, feature string, expr Expr) Expr {
	return Expr{kind: exprVersioned, value: &versionedExpression{version, feature, expr}}
}
