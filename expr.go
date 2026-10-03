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
	value any
	node  *expression
	text  string
	kind  exprKind
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
	query  Rowset
	values []Expr
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
func Param(value any) Expr { return parameter(value) }

func parameter(value any) Expr {
	switch value.(type) {
	case interface{ qxExpression() }, Statement, WriteValue, WriteRowValue, *WriteValue, *WriteRowValue:
		return invalidExpr("parameter", "an expression, statement or destination write cannot be bound as a value")
	case interface{ optionalValue() }:
		return invalidExpr("parameter", "test Optional.Present before binding its Value")
	case interface{ qxNull() }:
		return invalidExpr("parameter", "use ParamNull to bind qs.Null")
	}
	return Expr{kind: exprParameter, value: value}
}

// ParamNull binds a valid value or binds SQL NULL when value is invalid.
func ParamNull[T any](value Null[T]) Expr {
	if !value.Valid {
		return parameter(nil)
	}
	return parameter(value.Value)
}

// ArrayParam binds one driver-encoded array, with an explicit PostgreSQL cast.
// The driver must support encoding []T; qs does not convert or copy the slice.
func ArrayParam[T any](values []T, element DataType) Expr {
	return Param(values).Cast(TypeArray(element))
}

// NullExpr is a literal, explicitly typed SQL NULL and has no parameter.
func NullExpr(typ DataType) Expr { return NullLiteral().Cast(typ) }

// NullLiteral returns an untyped SQL NULL literal.
func NullLiteral() Expr { return Expr{kind: exprLiteral, text: "NULL"} }

// LiteralInt is for SQL grammar positions such as an ordinal. Normal application
// values should use Param so the query shape is independent of their values.
func LiteralInt(value int64) Expr {
	return Expr{kind: exprLiteral, text: strconv.FormatInt(value, 10)}
}

// LiteralBool returns a SQL TRUE or FALSE literal.
func LiteralBool(value bool) Expr {
	if value {
		return Expr{kind: exprLiteral, text: sqlTrue}
	}
	return Expr{kind: exprLiteral, text: "FALSE"}
}

// LiteralFloat returns a finite SQL numeric literal. Non-finite values produce
// an invalid expression; bind those values with Param instead.
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

// SQLStatement is a trusted statement fragment created by StatementSQL. It is
// rendered as supplied and does not implement Rowset or parse SQL syntax.
type SQLStatement struct{ expr Expr }

// As aliases an expression with a validated SQL identifier.
func (e Expr) As(alias string) Expr {
	return Expr{kind: exprAlias, text: alias, node: &expression{left: e}}
}

// Cast adds an explicit PostgreSQL type cast to an expression.
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

// Collate applies a PostgreSQL collation name to an expression.
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

// Add returns the SQL addition of e and other.
func (e Expr) Add(other Expr) Expr { return binary(e, "+", other) }

// Sub returns the SQL subtraction of other from e.
func (e Expr) Sub(other Expr) Expr { return binary(e, "-", other) }

// Mul returns the SQL multiplication of e and other.
func (e Expr) Mul(other Expr) Expr { return binary(e, "*", other) }

// Div returns the SQL division of e by other.
func (e Expr) Div(other Expr) Expr { return binary(e, "/", other) }

// Mod returns the SQL remainder of e divided by other.
func (e Expr) Mod(other Expr) Expr { return binary(e, "%", other) }

// Concat returns the SQL string concatenation of e and other.
func (e Expr) Concat(other Expr) Expr { return binary(e, "||", other) }

// Negate returns the SQL unary negation of e.
func (e Expr) Negate() Expr { return prefix("-", e) }

// BitAnd returns the SQL bitwise AND of e and other.
func (e Expr) BitAnd(other Expr) Expr { return binary(e, "&", other) }

// BitOr returns the SQL bitwise OR of e and other.
func (e Expr) BitOr(other Expr) Expr { return binary(e, "|", other) }

// BitXor returns the SQL bitwise exclusive OR of e and other.
func (e Expr) BitXor(other Expr) Expr { return binary(e, "#", other) }

// BitNot returns the SQL bitwise complement of e.
func (e Expr) BitNot() Expr { return prefix("~", e) }

// ShiftLeft returns the SQL left shift of e by other.
func (e Expr) ShiftLeft(other Expr) Expr { return binary(e, "<<", other) }

// ShiftRight returns the SQL right shift of e by other.
func (e Expr) ShiftRight(other Expr) Expr { return binary(e, ">>", other) }

// AtTimeZone applies PostgreSQL's AT TIME ZONE operator to e and zone.
func (e Expr) AtTimeZone(zone Expr) Expr { return binary(e, "AT TIME ZONE", zone) }

// Index is PostgreSQL subscripting (SQL arrays are conventionally 1-based).
// Index returns a PostgreSQL subscript expression for e and index.
func (e Expr) Index(index Expr) Expr {
	return Expr{kind: exprSubscript, node: &expression{left: e, right: index}}
}

// Slice returns a PostgreSQL slice with both lower and upper bounds.
func (e Expr) Slice(lower, upper Expr) Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{e, lower, upper, true, true}}
}

// SliceFrom returns a PostgreSQL slice with only a lower bound.
func (e Expr) SliceFrom(lower Expr) Expr {
	return Expr{kind: exprSlice, value: &sliceExpression{value: e, lower: lower, hasLower: true}}
}

// SliceTo returns a PostgreSQL slice with only an upper bound.
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
		w.renderInvalid(e)
	case exprIdentifier:
		w.identifierPath(e.text, true)
	case exprIdentifierParts:
		// The names live in a dedicated payload to avoid splitting dotted paths.
		w.identifierParts(e)
	case exprParameter:
		w.bind(e.value)
	case exprRaw:
		w.renderRaw(e.text)
	case exprLiteral, exprKeyword:
		w.renderLiteral(e)
	case exprStringLiteral:
		w.renderStringLiteral(e.text)
	case exprBinary:
		w.renderBinary(e)
	case exprPrefix:
		w.renderPrefix(e)
	case exprPostfix:
		w.renderPostfix(e)
	case exprGroup:
		w.renderGroup(e)
	case exprFragment:
		w.renderFragment(e)
	case exprAlias:
		w.renderAlias(e)
	case exprCast:
		w.renderCast(e)
	case exprField, exprFields:
		w.renderField(e)
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
		w.renderSubquery(e)
	case exprList:
		w.renderList(e)
	case exprMembership:
		w.membership(e)
	case exprBetween:
		w.renderBetween(e)
	case exprCase:
		w.caseExpr(ownedPayload[*caseExpression](e.value))
	case exprQuantified:
		w.renderQuantified(e)
	case exprSubscript:
		w.renderSubscript(e)
	case exprSlice:
		w.renderSlice(e)
	case exprCollate:
		w.renderCollate(e)
	case exprSQLJSON:
		w.sqlJSON(e)
	case exprVersioned:
		w.renderVersioned(e)
	default:
		w.fail(ErrInvalid, "expression", "unknown expression kind")
	}
}

func (w *renderer) renderLiteral(e Expr) {
	if e.kind == exprKeyword && e.text == sqlDefault {
		w.fail(ErrInvalid, "expression", "DEFAULT is only allowed directly in a destination write")
		return
	}
	w.text(e.text)
}

func (w *renderer) renderInvalid(e Expr) {
	if v, ok := e.value.(*invalidExpression); ok {
		w.fail(ErrInvalid, v.clause, v.detail)
		return
	}
	w.fail(ErrInvalid, "expression", "zero expression")
}

func (w *renderer) renderRaw(sql string) {
	if w.require(strings.TrimSpace(sql) != "" && strings.IndexByte(sql, 0) < 0 && utf8.ValidString(sql), "SQL", "empty, NUL-containing or invalid UTF-8 raw SQL") {
		w.text(sql)
	}
}

func (w *renderer) renderStringLiteral(value string) {
	if !w.require(utf8.ValidString(value) && strings.IndexByte(value, 0) < 0, "literal", "invalid UTF-8 or NUL in string literal") {
		return
	}
	w.text("E'")
	for i := range len(value) {
		switch value[i] {
		case '\\':
			w.text("\\\\")
		case '\'':
			w.text("''")
		default:
			w.byte(value[i])
		}
	}
	w.byte('\'')
}

func (w *renderer) renderBinary(e Expr) {
	w.byte('(')
	w.expr(e.node.left)
	w.byte(' ')
	w.text(e.text)
	w.byte(' ')
	w.expr(e.node.right)
	w.byte(')')
}

func (w *renderer) renderPrefix(e Expr) {
	w.byte('(')
	w.text(e.text)
	w.byte(' ')
	w.expr(e.node.right)
	w.byte(')')
}

func (w *renderer) renderPostfix(e Expr) {
	w.byte('(')
	w.expr(e.node.left)
	w.byte(' ')
	w.text(e.text)
	w.byte(')')
}

func (w *renderer) renderGroup(e Expr) {
	w.byte('(')
	w.exprs(ownedPayload[[]Expr](e.value), e.text)
	w.byte(')')
}

func (w *renderer) renderFragment(e Expr) {
	w.exprs(ownedPayload[[]Expr](e.value), "")
}

func (w *renderer) renderAlias(e Expr) {
	w.expr(e.node.left)
	w.text(" AS ")
	w.identifierPart(e.text)
}

func (w *renderer) renderCast(e Expr) {
	n := ownedPayload[*castExpression](e.value)
	w.byte('(')
	w.expr(n.expr)
	w.text(")::")
	w.dataType(n.typ)
}

func (w *renderer) renderField(e Expr) {
	w.indirectionBase(e.node.left)
	w.byte('.')
	if e.kind == exprField {
		w.identifierPart(e.text)
		return
	}
	w.byte('*')
}

func (w *renderer) renderSubquery(e Expr) {
	n := ownedPayload[*subqueryExpression](e.value)
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
}

func (w *renderer) renderList(e Expr) {
	w.text(e.text)
	array := e.text == sqlArray
	if array {
		w.byte('[')
	} else {
		w.byte('(')
	}
	w.exprs(ownedPayload[[]Expr](e.value), ", ")
	if array {
		w.byte(']')
	} else {
		w.byte(')')
	}
}

func (w *renderer) renderBetween(e Expr) {
	n := ownedPayload[*betweenExpression](e.value)
	w.byte('(')
	w.expr(n.value)
	w.byte(' ')
	w.text(e.text)
	w.byte(' ')
	w.expr(n.lower)
	w.text(" AND ")
	w.expr(n.upper)
	w.byte(')')
}

func (w *renderer) renderQuantified(e Expr) {
	w.text(e.text)
	w.byte('(')
	w.expr(e.node.left)
	w.byte(')')
}

func (w *renderer) renderSubscript(e Expr) {
	w.indirectionBase(e.node.left)
	w.byte('[')
	w.expr(e.node.right)
	w.byte(']')
}

func (w *renderer) renderSlice(e Expr) {
	n := ownedPayload[*sliceExpression](e.value)
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
}

func (w *renderer) renderCollate(e Expr) {
	w.byte('(')
	w.expr(e.node.left)
	w.text(" COLLATE ")
	w.identifierPath(e.text, false)
	w.byte(')')
}

func (w *renderer) renderVersioned(e Expr) {
	n := ownedPayload[*versionedExpression](e.value)
	w.feature(n.version, n.feature)
	w.expr(n.expr)
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
	parts := ownedPayload[identifierParts](e.value)
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
	feature string
	expr    Expr
	version PostgreSQLVersion
}

func versionExpression(version PostgreSQLVersion, feature string, expr Expr) Expr {
	return Expr{kind: exprVersioned, value: &versionedExpression{version: version, feature: feature, expr: expr}}
}
