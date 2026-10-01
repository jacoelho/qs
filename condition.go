package qx

// Condition is a SQL boolean expression. Unknown (SQL NULL) retains PostgreSQL
// three-valued semantics; qx never rewrites equality according to a bound value.
type Condition struct{ expr Expr }

func (c Condition) Expr() Expr        { return c.expr }
func AsCondition(expr Expr) Condition { return Condition{expr} }
func True() Condition                 { return AsCondition(LiteralBool(true)) }
func False() Condition                { return AsCondition(LiteralBool(false)) }

func And(conditions ...Condition) Condition { return junction(" AND ", true, conditions) }
func Or(conditions ...Condition) Condition  { return junction(" OR ", false, conditions) }
func Not(condition Condition) Condition     { return AsCondition(prefix("NOT", condition.expr)) }
func (c Condition) And(other ...Condition) Condition {
	return c.junction(" AND ", other)
}
func (c Condition) Or(other ...Condition) Condition {
	return c.junction(" OR ", other)
}
func (c Condition) Not() Condition { return Not(c) }

func junction(operator string, identity bool, conditions []Condition) Condition {
	if len(conditions) == 0 {
		return AsCondition(LiteralBool(identity))
	}
	return conditions[0].junction(operator, conditions[1:])
}

func (c Condition) junction(operator string, other []Condition) Condition {
	if len(other) == 0 {
		return c
	}
	values := make([]Expr, 1+len(other))
	values[0] = c.expr
	for i := range other {
		values[i+1] = other[i].expr
	}
	return AsCondition(Expr{kind: exprGroup, text: operator, value: values})
}

func compare(left Expr, operator string, right Expr) Condition {
	return AsCondition(binary(left, operator, right))
}

func (e Expr) Eq(value any) Condition                  { return e.EqExpr(parameter(value)) }
func (e Expr) Ne(value any) Condition                  { return e.NeExpr(parameter(value)) }
func (e Expr) Lt(value any) Condition                  { return e.LtExpr(parameter(value)) }
func (e Expr) Lte(value any) Condition                 { return e.LteExpr(parameter(value)) }
func (e Expr) Gt(value any) Condition                  { return e.GtExpr(parameter(value)) }
func (e Expr) Gte(value any) Condition                 { return e.GteExpr(parameter(value)) }
func (e Expr) EqExpr(other Expr) Condition             { return compare(e, "=", other) }
func (e Expr) NeExpr(other Expr) Condition             { return compare(e, "<>", other) }
func (e Expr) LtExpr(other Expr) Condition             { return compare(e, "<", other) }
func (e Expr) LteExpr(other Expr) Condition            { return compare(e, "<=", other) }
func (e Expr) GtExpr(other Expr) Condition             { return compare(e, ">", other) }
func (e Expr) GteExpr(other Expr) Condition            { return compare(e, ">=", other) }
func (e Expr) IsDistinctFromExpr(other Expr) Condition { return compare(e, "IS DISTINCT FROM", other) }
func (e Expr) IsNotDistinctFromExpr(other Expr) Condition {
	return compare(e, "IS NOT DISTINCT FROM", other)
}

func (e Expr) IsNull() Condition       { return AsCondition(postfix(e, "IS NULL")) }
func (e Expr) IsNotNull() Condition    { return AsCondition(postfix(e, "IS NOT NULL")) }
func (e Expr) IsTrue() Condition       { return AsCondition(postfix(e, "IS TRUE")) }
func (e Expr) IsNotTrue() Condition    { return AsCondition(postfix(e, "IS NOT TRUE")) }
func (e Expr) IsFalse() Condition      { return AsCondition(postfix(e, "IS FALSE")) }
func (e Expr) IsNotFalse() Condition   { return AsCondition(postfix(e, "IS NOT FALSE")) }
func (e Expr) IsUnknown() Condition    { return AsCondition(postfix(e, "IS UNKNOWN")) }
func (e Expr) IsNotUnknown() Condition { return AsCondition(postfix(e, "IS NOT UNKNOWN")) }

func IsNull(column string) Condition              { return Col(column).IsNull() }
func IsNotNull(column string) Condition           { return Col(column).IsNotNull() }
func Eq[T any](column string, value T) Condition  { return Col(column).Eq(value) }
func Ne[T any](column string, value T) Condition  { return Col(column).Ne(value) }
func Lt[T any](column string, value T) Condition  { return Col(column).Lt(value) }
func Lte[T any](column string, value T) Condition { return Col(column).Lte(value) }
func Gt[T any](column string, value T) Condition  { return Col(column).Gt(value) }
func Gte[T any](column string, value T) Condition { return Col(column).Gte(value) }
func EqColumns(left, right string) Condition      { return Col(left).EqExpr(Col(right)) }
func NeColumns(left, right string) Condition      { return Col(left).NeExpr(Col(right)) }
func IsDistinctFrom[T any](column string, value Null[T]) Condition {
	return Col(column).IsDistinctFromExpr(ParamNull(value))
}
func IsNotDistinctFrom[T any](column string, value Null[T]) Condition {
	return Col(column).IsNotDistinctFromExpr(ParamNull(value))
}

func between(value Expr, operator string, lower, upper Expr) Condition {
	return AsCondition(Expr{kind: exprBetween, text: operator, value: &betweenExpression{value, lower, upper}})
}
func (e Expr) Between(lower, upper any) Condition {
	return e.BetweenExpr(parameter(lower), parameter(upper))
}
func (e Expr) NotBetween(lower, upper any) Condition {
	return e.NotBetweenExpr(parameter(lower), parameter(upper))
}
func (e Expr) BetweenExpr(lower, upper Expr) Condition { return between(e, "BETWEEN", lower, upper) }
func (e Expr) NotBetweenExpr(lower, upper Expr) Condition {
	return between(e, "NOT BETWEEN", lower, upper)
}
func (e Expr) BetweenSymmetric(lower, upper Expr) Condition {
	return between(e, "BETWEEN SYMMETRIC", lower, upper)
}
func (e Expr) NotBetweenSymmetric(lower, upper Expr) Condition {
	return between(e, "NOT BETWEEN SYMMETRIC", lower, upper)
}
func Between[T any](column string, lower, upper T) Condition {
	return Col(column).Between(lower, upper)
}

func inExpressions(left Expr, operator string, values []Expr, width int) Condition {
	return inOwnedExpressions(left, operator, cloneSlice(values), width)
}

// inOwnedExpressions takes a private expression list. Public slice inputs must
// be copied before crossing this boundary.
func inOwnedExpressions(left Expr, operator string, values []Expr, width int) Condition {
	if len(values) == 0 {
		if operator == "NOT IN" {
			return True()
		}
		return False()
	}
	return AsCondition(Expr{kind: exprMembership, text: operator, value: &membershipExpression{left: left, values: values, width: width}})
}
func inValues[T any](left Expr, operator string, values []T) Condition {
	exprs := make([]Expr, len(values))
	for i := range values {
		exprs[i] = Param(values[i])
	}
	return inOwnedExpressions(left, operator, exprs, 1)
}

// In explicitly expands a homogeneous list. An empty list is FALSE. []byte is
// a list only when deliberately passed here; Eq binds []byte as one value.
func In[T any](column string, values ...T) Condition { return inValues(Col(column), "IN", values) }
func NotIn[T any](column string, values ...T) Condition {
	return inValues(Col(column), "NOT IN", values)
}
func (e Expr) In(values ...any) Condition         { return inValues(e, "IN", values) }
func (e Expr) NotIn(values ...any) Condition      { return inValues(e, "NOT IN", values) }
func (e Expr) InExpr(values ...Expr) Condition    { return inExpressions(e, "IN", values, 1) }
func (e Expr) NotInExpr(values ...Expr) Condition { return inExpressions(e, "NOT IN", values, 1) }
func (e Expr) InQuery(query Rowset) Condition     { return inQuery(e, "IN", query, 1) }
func (e Expr) NotInQuery(query Rowset) Condition  { return inQuery(e, "NOT IN", query, 1) }
func inQuery(e Expr, operator string, query Rowset, width int) Condition {
	return AsCondition(Expr{kind: exprMembership, text: operator, value: &membershipExpression{left: e, query: query, width: width}})
}

func (w *renderer) membership(e Expr) {
	n := e.value.(*membershipExpression)
	w.byte('(')
	w.expr(n.left)
	w.byte(' ')
	w.text(e.text)
	w.text(" (")
	if n.query != nil {
		width := statementWidth(n.query, w.options.MaxDepth)
		if width >= 0 && n.width != width {
			w.fail(ErrInvalid, "IN", "subquery projection width differs from the left operand")
			return
		}
		w.statement(n.query)
	} else {
		if !w.require(len(n.values) != 0, "IN", "nil subquery") {
			return
		}
		for _, value := range n.values {
			if n.width > 1 && (value.kind != exprList || len(value.value.([]Expr)) != n.width) {
				w.fail(ErrInvalid, "IN", "row widths differ")
				return
			}
		}
		w.exprs(n.values, ", ")
	}
	w.text("))")
}

func Exists(query Rowset) Condition {
	return AsCondition(Expr{kind: exprExists, text: "EXISTS ", value: &subqueryExpression{query: query}})
}
func NotExists(query Rowset) Condition { return Not(Exists(query)) }

// AnyArray / AllArray build SQL quantified operands. The corresponding query
// forms avoid an extra scalar-subquery wrapper and validate one-column output.
func AnyArray(array Expr) Expr {
	return Expr{kind: exprQuantified, text: "ANY", node: &expression{left: array}}
}
func AllArray(array Expr) Expr {
	return Expr{kind: exprQuantified, text: "ALL", node: &expression{left: array}}
}
func AnyQuery(query Rowset) Expr {
	return Expr{kind: exprExists, text: "ANY ", value: &subqueryExpression{query: query, expectedWidth: 1}}
}
func AllQuery(query Rowset) Expr {
	return Expr{kind: exprExists, text: "ALL ", value: &subqueryExpression{query: query, expectedWidth: 1}}
}
func EqAny(left, array Expr) Condition { return left.EqExpr(AnyArray(array)) }
func EqAll(left, array Expr) Condition { return left.EqExpr(AllArray(array)) }

func (w *renderer) conditions(conditions []Condition) {
	for i, c := range conditions {
		if i != 0 {
			w.text(" AND ")
		}
		w.expr(c.expr)
		if w.err != nil {
			return
		}
	}
}
