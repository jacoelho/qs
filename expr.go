package qs

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	sqlTrue  = "TRUE"
	sqlFalse = "FALSE"
	sqlArray = "ARRAY"
)

// exprKind tags the leaf expressions, which need only text or a bound value.
// Every composite expression is an exprNode.
type exprKind uint8

const (
	exprZero          exprKind = iota // the zero Expr, which is invalid
	exprIdentifier                    // text is one identifier
	exprQualified                     // value is the qualifying identifier, a string, and text the name
	exprParameter                     // value is the bound value
	exprRaw                           // text is trusted SQL
	exprLiteral                       // text is the literal's SQL spelling
	exprKeyword                       // text is a keyword
	exprStringLiteral                 // text is the unescaped string
	exprNode                          // value is a node
)

// Expr is an immutable SQL expression. A zero Expr is invalid, never an omitted
// filter. Use Param for values, Col for identifiers, and UnsafeSQL only for
// trusted syntax. It represents a SQL expression, not a schema proof.
type Expr struct {
	value any // the bound value of an exprParameter, the node of an exprNode or the qualifier of an exprQualified
	text  string
	kind  exprKind
}

// node is a composite expression form. The renderer and the structural checks
// dispatch on its concrete type, so a form carries exactly the fields it
// renders and no tag can disagree with its payload.
type node interface{ exprNode() }

// nodeExpr is the only constructor of a composite expression.
func nodeExpr(n node) Expr { return Expr{kind: exprNode, value: n} }

// qualifier returns the first identifier of an exprQualified. It is kept in
// value rather than in a field of its own: a second string would widen every
// Expr by 16 bytes for the benefit of one kind.
func (e Expr) qualifier() string {
	if name, ok := e.value.(string); ok {
		return name
	}
	return ""
}

// node returns the composite form of e for a type switch, or nil for a leaf.
func (e Expr) node() any {
	if e.kind != exprNode {
		return nil
	}
	return e.value
}

type (
	invalidExpression struct{ clause, detail string }
	// qualifiedIdentifier is a name of two or more parts. pair is the storage
	// of a two-part name, so that table.column costs one allocation.
	qualifiedIdentifier struct {
		pair  [2]string
		parts identifierParts
	}
	starExpression   struct{ qualifier identifierParts }
	binaryExpression struct {
		left     Expr
		operator string
		right    Expr
	}
	prefixExpression struct {
		operator string
		operand  Expr
	}
	postfixExpression struct {
		operator string
		operand  Expr
	}
	// groupExpression is items joined by separator inside one pair of parentheses.
	groupExpression struct {
		separator string
		items     []Expr
	}
	fragmentExpression struct{ parts []Expr }
	aliasExpression    struct {
		alias string
		expr  Expr
	}
	castExpression struct {
		expr Expr
		typ  DataType
	}
	fieldExpression struct {
		name string
		base Expr
	}
	fieldsExpression struct{ base Expr }
	// subqueryExpression is keyword(query); the keyword is empty for a scalar
	// subquery and otherwise carries its trailing space.
	subqueryExpression struct {
		query         Rowset
		keyword       string
		expectedWidth int
	}
	// listExpression is prefix(items), or ARRAY[items]. An empty or ROW prefix
	// is a row constructor.
	listExpression struct {
		prefix string
		items  []Expr
	}
	membershipExpression struct {
		left     Expr
		operator string
		query    Rowset
		values   []Expr
		width    int
	}
	betweenExpression struct {
		value        Expr
		operator     string
		lower, upper Expr
	}
	quantifiedExpression struct {
		quantifier string
		array      Expr
	}
	subscriptExpression struct{ base, index Expr }
	sliceExpression     struct {
		base, lower, upper Expr
		hasLower, hasUpper bool
	}
	collateExpression struct {
		expr Expr
		name identifierParts
	}
)

func (*invalidExpression) exprNode()    {}
func (*qualifiedIdentifier) exprNode()  {}
func (*starExpression) exprNode()       {}
func (*binaryExpression) exprNode()     {}
func (*prefixExpression) exprNode()     {}
func (*postfixExpression) exprNode()    {}
func (*groupExpression) exprNode()      {}
func (*fragmentExpression) exprNode()   {}
func (*aliasExpression) exprNode()      {}
func (*castExpression) exprNode()       {}
func (*fieldExpression) exprNode()      {}
func (*fieldsExpression) exprNode()     {}
func (*subqueryExpression) exprNode()   {}
func (*listExpression) exprNode()       {}
func (*membershipExpression) exprNode() {}
func (*betweenExpression) exprNode()    {}
func (*quantifiedExpression) exprNode() {}
func (*subscriptExpression) exprNode()  {}
func (*sliceExpression) exprNode()      {}
func (*collateExpression) exprNode()    {}
func (*callExpression) exprNode()       {}
func (*extractExpression) exprNode()    {}
func (*sqlSyntaxExpression) exprNode()  {}
func (*caseExpression) exprNode()       {}

func (Expr) qxExpression()      {}
func (Condition) qxExpression() {}
func (Null[T]) qxNull()         {}

func invalidExpr(clause, detail string) Expr {
	return nodeExpr(&invalidExpression{clause, detail})
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
// A nil slice binds an empty array, never SQL NULL, so that ANY, ALL and NOT
// agree with In and NotIn on an empty list; use NullExpr(TypeArray(element))
// for a NULL array. The driver must support encoding []T. qs does not copy a
// non-nil slice, which must not be mutated before execution completes.
func ArrayParam[T any](values []T, element DataType) Expr {
	if values == nil {
		values = []T{}
	}
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
	return Expr{kind: exprLiteral, text: sqlFalse}
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
// Operators render it verbatim; use Parenthesized when its own operators could
// bind to a neighbouring operand. See AsCondition for combined conditions.
func UnsafeSQL(trusted string) Expr { return Expr{kind: exprRaw, text: trusted} }

// Fragment concatenates expressions with no implicit separators or parentheses.
// SQL syntax must be explicit UnsafeSQL parts; values remain Param expressions.
// See AsCondition for the boundary qs adds when it combines conditions.
func Fragment(parts ...Expr) Expr {
	if len(parts) == 0 {
		return invalidExpr("fragment", "empty SQL fragment")
	}
	return nodeExpr(&fragmentExpression{cloneSlice(parts)})
}

// Parenthesized preserves an expression boundary. For example, a.Index(i).
// Parenthesized().Index(j) indexes the result of a[i], whereas a.Index(i).
// Index(j) applies a multidimensional subscript to a.
func (e Expr) Parenthesized() Expr {
	return nodeExpr(&groupExpression{items: []Expr{e}})
}

// orderTermSyntax returns the expression PostgreSQL parses for an ORDER BY
// term. Its parser discards grouping parentheses around a single expression,
// so ORDER BY (0x1) is still an ordinal and ORDER BY ("x") may still name an
// output column.
func orderTermSyntax(e Expr) Expr {
	for {
		n, ok := e.node().(*groupExpression)
		if !ok || len(n.items) != 1 {
			return e
		}
		e = n.items[0]
	}
}

// enclosed concatenates parts inside one pair of parentheses. Structural forms
// spelled with trusted syntax use it rather than Fragment so that they carry
// their own precedence boundary.
func enclosed(parts ...Expr) Expr {
	return nodeExpr(&groupExpression{items: cloneSlice(parts)})
}

// opaque reports whether e is trusted SQL without its own precedence boundary.
func (e Expr) opaque() bool {
	if e.kind == exprRaw {
		return true
	}
	_, fragment := e.node().(*fragmentExpression)
	return fragment
}

// StatementSQL is an extension point for trusted statement syntax, not a parser.
// It intentionally does not implement Rowset; subqueries should be structural.
func StatementSQL(parts ...Expr) SQLStatement { return SQLStatement{expr: Fragment(parts...)} }

// SQLStatement is a trusted statement fragment created by StatementSQL. It is
// rendered as supplied and does not implement Rowset or parse SQL syntax.
type SQLStatement struct{ expr Expr }

// As aliases an expression with a validated SQL identifier.
func (e Expr) As(alias string) Expr {
	return nodeExpr(&aliasExpression{expr: e, alias: alias})
}

// Cast adds an explicit PostgreSQL type cast to an expression.
func (e Expr) Cast(typ DataType) Expr {
	return nodeExpr(&castExpression{expr: e, typ: typ})
}

// Field selects one field of a composite value; name is one identifier.
// Col("a", "b") instead names a qualified column, which PostgreSQL resolves
// by different rules.
func (e Expr) Field(name string) Expr {
	return nodeExpr(&fieldExpression{base: e, name: name})
}

// Fields expands a composite value in PostgreSQL expression-list positions.
// Its width is unknown until PostgreSQL resolves the composite type.
func (e Expr) Fields() Expr {
	return nodeExpr(&fieldsExpression{base: e})
}

// Collate applies a PostgreSQL collation to an expression. Each argument is
// one identifier: Collate("C"), or Collate("pg_catalog", "default").
func (e Expr) Collate(first string, rest ...string) Expr {
	return nodeExpr(&collateExpression{expr: e, name: qualifiedName(first, rest)})
}

// Scalar requires one projected column when the projection width is known.
// PostgreSQL enforces the scalar subquery's run-time row count.
func Scalar(query Rowset) Expr {
	return nodeExpr(&subqueryExpression{query: query, expectedWidth: 1})
}

func binary(left Expr, operator string, right Expr) Expr {
	return nodeExpr(&binaryExpression{left, operator, right})
}
func prefix(operator string, right Expr) Expr {
	return nodeExpr(&prefixExpression{operator, right})
}
func postfix(left Expr, operator string) Expr {
	return nodeExpr(&postfixExpression{operator: operator, operand: left})
}
func listExpr(prefix string, items []Expr) Expr {
	return nodeExpr(&listExpression{prefix: prefix, items: cloneSlice(items)})
}

// rowList returns the list of e when e is a ROW(...) or bare tuple constructor.
func rowList(e Expr) (*listExpression, bool) {
	n, ok := e.node().(*listExpression)
	return n, ok && (n.prefix == sqlRow || n.prefix == "")
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
	return nodeExpr(&subscriptExpression{base: e, index: index})
}

// Slice returns a PostgreSQL slice with both lower and upper bounds.
func (e Expr) Slice(lower, upper Expr) Expr {
	return nodeExpr(&sliceExpression{e, lower, upper, true, true})
}

// SliceFrom returns a PostgreSQL slice with only a lower bound.
func (e Expr) SliceFrom(lower Expr) Expr {
	return nodeExpr(&sliceExpression{base: e, lower: lower, hasLower: true})
}

// SliceTo returns a PostgreSQL slice with only an upper bound.
func (e Expr) SliceTo(upper Expr) Expr {
	return nodeExpr(&sliceExpression{base: e, upper: upper, hasUpper: true})
}

// SliceAll emits [:], retaining PostgreSQL's full-slice semantics.
func (e Expr) SliceAll() Expr {
	return nodeExpr(&sliceExpression{base: e})
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
	if op == "" || len(op) > 63 || op == "=>" || strings.Contains(op, "--") || strings.Contains(op, "/*") {
		return false
	}
	for _, c := range op {
		if !strings.ContainsRune("+-*/<>=~!@#%^&|`?", c) {
			return false
		}
	}
	if len(op) > 1 && (op[len(op)-1] == '+' || op[len(op)-1] == '-') && !strings.ContainsAny(op, "~!@#%^&|`?") {
		return false
	}
	return true
}

// QualifiedOperator supports extension operators selected by schema.
func QualifiedOperator(left Expr, schema, operator string, right Expr) Expr {
	if !validOperator(operator) {
		return invalidExpr("operator", "invalid symbolic operator")
	}
	return enclosed(left, UnsafeSQL(" OPERATOR("), unqualifiedColumn(schema), UnsafeSQL("."+operator+") "), right)
}

func (w *renderer) exprs(exprs []Expr, separator string) {
	if w.err != nil {
		return
	}
	for i, e := range exprs {
		if i != 0 {
			w.text(separator)
		}
		w.expr(e)
		if w.err != nil {
			w.errorPath("expression", i+1)
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
	case exprIdentifier:
		w.identifierPart(e.text)
	case exprQualified:
		w.identifierPart(e.qualifier())
		w.byte('.')
		w.identifierPart(e.text)
	case exprParameter:
		w.bind(e.value)
	case exprRaw:
		w.renderRaw(e.text)
	case exprLiteral:
		w.text(e.text)
	case exprKeyword:
		w.renderKeyword(e.text)
	case exprStringLiteral:
		w.renderStringLiteral(e.text)
	case exprNode:
		w.node(e.value)
	default:
		w.fail(ErrInvalid, "expression", "zero expression")
	}
}

func (w *renderer) node(n any) {
	switch n := n.(type) {
	case *invalidExpression:
		w.fail(ErrInvalid, n.clause, n.detail)
	case *qualifiedIdentifier:
		w.qualifiedName(n.parts, "identifier")
	case *starExpression:
		w.star(n)
	case *binaryExpression:
		w.renderBinary(n)
	case *prefixExpression:
		w.renderPrefix(n)
	case *postfixExpression:
		w.renderPostfix(n)
	case *groupExpression:
		w.byte('(')
		w.exprs(n.items, n.separator)
		w.byte(')')
	case *fragmentExpression:
		w.exprs(n.parts, "")
	case *aliasExpression:
		w.expr(n.expr)
		w.text(" AS ")
		w.identifierPart(n.alias)
	case *castExpression:
		w.renderCast(n)
	case *fieldExpression:
		w.indirectionBase(n.base)
		w.byte('.')
		w.identifierPart(n.name)
	case *fieldsExpression:
		w.indirectionBase(n.base)
		w.text(".*")
	case *callExpression:
		w.call(n)
	case *extractExpression:
		w.extract(n)
	case *sqlSyntaxExpression:
		w.sqlSyntax(n)
	case *subqueryExpression:
		w.renderSubquery(n)
	case *listExpression:
		w.renderList(n)
	case *membershipExpression:
		w.membership(n)
	case *betweenExpression:
		w.renderBetween(n)
	case *caseExpression:
		w.caseExpr(n)
	case *quantifiedExpression:
		w.renderQuantified(n)
	case *subscriptExpression:
		w.renderSubscript(n)
	case *sliceExpression:
		w.renderSlice(n)
	case *collateExpression:
		w.renderCollate(n)
	default:
		w.jsonNode(n)
	}
}

func (w *renderer) renderPrefix(n *prefixExpression) {
	w.byte('(')
	w.text(n.operator)
	w.byte(' ')
	w.expr(n.operand)
	w.byte(')')
}

func (w *renderer) renderPostfix(n *postfixExpression) {
	w.byte('(')
	w.expr(n.operand)
	w.byte(' ')
	w.text(n.operator)
	w.byte(')')
}

func (w *renderer) renderCast(n *castExpression) {
	w.byte('(')
	w.expr(n.expr)
	w.text(")::")
	w.dataType(n.typ)
}

func (w *renderer) renderQuantified(n *quantifiedExpression) {
	w.text(n.quantifier)
	w.byte('(')
	w.expr(n.array)
	w.byte(')')
}

func (w *renderer) renderSubscript(n *subscriptExpression) {
	w.indirectionBase(n.base)
	w.byte('[')
	w.expr(n.index)
	w.byte(']')
}

func (w *renderer) renderCollate(n *collateExpression) {
	w.byte('(')
	w.expr(n.expr)
	w.text(" COLLATE ")
	w.qualifiedName(n.name, "COLLATE")
	w.byte(')')
}

func (w *renderer) renderKeyword(keyword string) {
	if keyword == sqlDefault {
		w.fail(ErrInvalid, "expression", "DEFAULT is only allowed directly in a destination write")
		return
	}
	w.text(keyword)
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

func (w *renderer) renderBinary(n *binaryExpression) {
	w.byte('(')
	w.expr(n.left)
	w.byte(' ')
	w.text(n.operator)
	w.byte(' ')
	w.expr(n.right)
	w.byte(')')
}

func (w *renderer) renderSubquery(n *subqueryExpression) {
	width := statementWidth(n.query, w.options.MaxDepth)
	if n.expectedWidth > 0 && width >= 0 && width != n.expectedWidth {
		w.fail(ErrInvalid, "subquery", "projection has the wrong number of columns")
		return
	}
	w.text(n.keyword)
	w.byte('(')
	w.statement(n.query)
	w.byte(')')
	if w.err != nil {
		if n.keyword == "" {
			w.errorPath("scalar subquery", 0)
		} else {
			w.errorPath("query operand", 0)
		}
	}
}

func (w *renderer) renderList(n *listExpression) {
	w.text(n.prefix)
	array := n.prefix == sqlArray
	if array {
		w.byte('[')
	} else {
		w.byte('(')
	}
	w.exprs(n.items, ", ")
	if array {
		w.byte(']')
	} else {
		w.byte(')')
	}
}

func (w *renderer) renderBetween(n *betweenExpression) {
	w.byte('(')
	w.expr(n.value)
	w.byte(' ')
	w.text(n.operator)
	w.byte(' ')
	w.expr(n.lower)
	w.text(" AND ")
	w.expr(n.upper)
	w.byte(')')
}

func (w *renderer) renderSlice(n *sliceExpression) {
	w.indirectionBase(n.base)
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

func (w *renderer) indirectionBase(e Expr) {
	switch e.node().(type) {
	case *fieldExpression, *fieldsExpression, *subscriptExpression, *sliceExpression, *groupExpression:
		w.expr(e)
	default:
		w.byte('(')
		w.expr(e)
		w.byte(')')
	}
}

type identifierParts []string
