package qs

// Condition is a SQL boolean expression. Unknown (SQL NULL) retains PostgreSQL
// three-valued semantics; qs never rewrites equality according to a bound value.
type Condition struct{ expr Expr }

// Expr returns the underlying boolean expression.
func (c Condition) Expr() Expr { return c.expr }

// AsCondition treats an expression as a SQL condition.
func AsCondition(expr Expr) Condition { return Condition{expr} }

// True returns a condition that is always TRUE.
func True() Condition { return AsCondition(LiteralBool(true)) }

// False returns a condition that is always FALSE.
func False() Condition { return AsCondition(LiteralBool(false)) }

// And combines conditions with SQL AND. With no conditions, it returns TRUE.
func And(conditions ...Condition) Condition { return junction(" AND ", true, conditions) }

// Or combines conditions with SQL OR. With no conditions, it returns FALSE.
func Or(conditions ...Condition) Condition { return junction(" OR ", false, conditions) }

// Not negates a condition while preserving SQL three-valued logic.
func Not(condition Condition) Condition { return AsCondition(prefix("NOT", condition.expr)) }

// And combines c with each condition in other. With no other conditions, it
// returns c unchanged.
func (c Condition) And(other ...Condition) Condition {
	return c.junction(" AND ", other)
}

// Or combines c with each condition in other. With no other conditions, it
// returns c unchanged.
func (c Condition) Or(other ...Condition) Condition {
	return c.junction(" OR ", other)
}

// Not negates c while preserving SQL three-valued logic.
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

// Eq compares e with a bound value using SQL equality.
func (e Expr) Eq(value any) Condition { return e.EqExpr(parameter(value)) }

// Ne compares e with a bound value using SQL inequality.
func (e Expr) Ne(value any) Condition { return e.NeExpr(parameter(value)) }

// Lt compares e with a bound value using SQL less-than.
func (e Expr) Lt(value any) Condition { return e.LtExpr(parameter(value)) }

// Lte compares e with a bound value using SQL less-than-or-equal.
func (e Expr) Lte(value any) Condition { return e.LteExpr(parameter(value)) }

// Gt compares e with a bound value using SQL greater-than.
func (e Expr) Gt(value any) Condition { return e.GtExpr(parameter(value)) }

// Gte compares e with a bound value using SQL greater-than-or-equal.
func (e Expr) Gte(value any) Condition { return e.GteExpr(parameter(value)) }

// EqExpr compares e with another SQL expression using equality.
func (e Expr) EqExpr(other Expr) Condition { return compare(e, "=", other) }

// NeExpr compares e with another SQL expression using inequality.
func (e Expr) NeExpr(other Expr) Condition { return compare(e, "<>", other) }

// LtExpr compares e with another SQL expression using less-than.
func (e Expr) LtExpr(other Expr) Condition { return compare(e, "<", other) }

// LteExpr compares e with another SQL expression using less-than-or-equal.
func (e Expr) LteExpr(other Expr) Condition { return compare(e, "<=", other) }

// GtExpr compares e with another SQL expression using greater-than.
func (e Expr) GtExpr(other Expr) Condition { return compare(e, ">", other) }

// GteExpr compares e with another SQL expression using greater-than-or-equal.
func (e Expr) GteExpr(other Expr) Condition { return compare(e, ">=", other) }

// IsDistinctFromExpr compares e with another expression using PostgreSQL's
// NULL-safe distinctness operator.
func (e Expr) IsDistinctFromExpr(other Expr) Condition { return compare(e, "IS DISTINCT FROM", other) }

// IsNotDistinctFromExpr compares e with another expression using PostgreSQL's
// NULL-safe equality operator.
func (e Expr) IsNotDistinctFromExpr(other Expr) Condition {
	return compare(e, "IS NOT DISTINCT FROM", other)
}

// IsNull tests whether e is SQL NULL.
func (e Expr) IsNull() Condition { return AsCondition(postfix(e, "IS NULL")) }

// IsNotNull tests whether e is not SQL NULL.
func (e Expr) IsNotNull() Condition { return AsCondition(postfix(e, "IS NOT NULL")) }

// IsTrue tests whether e is TRUE, excluding FALSE and NULL.
func (e Expr) IsTrue() Condition { return AsCondition(postfix(e, "IS TRUE")) }

// IsNotTrue tests whether e is not TRUE, including FALSE and NULL.
func (e Expr) IsNotTrue() Condition { return AsCondition(postfix(e, "IS NOT TRUE")) }

// IsFalse tests whether e is FALSE, excluding TRUE and NULL.
func (e Expr) IsFalse() Condition { return AsCondition(postfix(e, "IS FALSE")) }

// IsNotFalse tests whether e is not FALSE, including TRUE and NULL.
func (e Expr) IsNotFalse() Condition { return AsCondition(postfix(e, "IS NOT FALSE")) }

// IsUnknown tests whether e is SQL NULL.
func (e Expr) IsUnknown() Condition { return AsCondition(postfix(e, "IS UNKNOWN")) }

// IsNotUnknown tests whether e is not SQL NULL.
func (e Expr) IsNotUnknown() Condition { return AsCondition(postfix(e, "IS NOT UNKNOWN")) }

// IsNull tests whether a named column is SQL NULL.
func IsNull(column string) Condition { return Col(column).IsNull() }

// IsNotNull tests whether a named column is not SQL NULL.
func IsNotNull(column string) Condition { return Col(column).IsNotNull() }

// Eq compares a named column with a bound value.
func Eq(column string, value any) Condition { return Col(column).Eq(value) }

// Ne compares a named column with a bound value.
func Ne(column string, value any) Condition { return Col(column).Ne(value) }

// Lt compares a named column with a bound value.
func Lt(column string, value any) Condition { return Col(column).Lt(value) }

// Lte compares a named column with a bound value.
func Lte(column string, value any) Condition { return Col(column).Lte(value) }

// Gt compares a named column with a bound value.
func Gt(column string, value any) Condition { return Col(column).Gt(value) }

// Gte compares a named column with a bound value.
func Gte(column string, value any) Condition { return Col(column).Gte(value) }

// EqColumns compares two named columns with equality.
func EqColumns(left, right string) Condition { return Col(left).EqExpr(Col(right)) }

// NeColumns compares two named columns with inequality.
func NeColumns(left, right string) Condition { return Col(left).NeExpr(Col(right)) }

// IsDistinctFrom compares a named column with a nullable bound value.
func IsDistinctFrom[T any](column string, value Null[T]) Condition {
	return Col(column).IsDistinctFromExpr(ParamNull(value))
}

// IsNotDistinctFrom compares a named column with a nullable bound value.
func IsNotDistinctFrom[T any](column string, value Null[T]) Condition {
	return Col(column).IsNotDistinctFromExpr(ParamNull(value))
}

func between(value Expr, operator string, lower, upper Expr) Condition {
	return AsCondition(Expr{kind: exprBetween, text: operator, value: &betweenExpression{value, lower, upper}})
}

// Between checks whether e lies inclusively between two bound values.
func (e Expr) Between(lower, upper any) Condition {
	return e.BetweenExpr(parameter(lower), parameter(upper))
}

// NotBetween checks whether e lies outside two inclusive bound values.
func (e Expr) NotBetween(lower, upper any) Condition {
	return e.NotBetweenExpr(parameter(lower), parameter(upper))
}

// BetweenExpr checks e against two SQL expressions using BETWEEN.
func (e Expr) BetweenExpr(lower, upper Expr) Condition { return between(e, "BETWEEN", lower, upper) }

// NotBetweenExpr checks e against two SQL expressions using NOT BETWEEN.
func (e Expr) NotBetweenExpr(lower, upper Expr) Condition {
	return between(e, "NOT BETWEEN", lower, upper)
}

// BetweenSymmetric checks e using PostgreSQL's order-independent BETWEEN form.
func (e Expr) BetweenSymmetric(lower, upper Expr) Condition {
	return between(e, "BETWEEN SYMMETRIC", lower, upper)
}

// NotBetweenSymmetric checks e using PostgreSQL's order-independent NOT form.
func (e Expr) NotBetweenSymmetric(lower, upper Expr) Condition {
	return between(e, "NOT BETWEEN SYMMETRIC", lower, upper)
}

// Between checks a named column against two bound values inclusively.
func Between[T any](column string, lower, upper T) Condition {
	return Col(column).Between(lower, upper)
}

func inExpressions(left Expr, operator string, values []Expr) Condition {
	return inOwnedExpressions(left, operator, cloneSlice(values))
}

// inOwnedExpressions takes a private expression list. Public slice inputs must
// be copied before crossing this boundary.
func inOwnedExpressions(left Expr, operator string, values []Expr) Condition {
	if len(values) == 0 {
		if operator == "NOT IN" {
			return True()
		}
		return False()
	}
	return AsCondition(Expr{kind: exprMembership, text: operator, value: &membershipExpression{left: left, values: values}})
}
func inValues[T any](left Expr, operator string, values []T) Condition {
	exprs := make([]Expr, len(values))
	for i := range values {
		exprs[i] = Param(values[i])
	}
	return inOwnedExpressions(left, operator, exprs)
}

// In explicitly expands a homogeneous list for a named column. An empty list
// is FALSE. []byte is a list only when deliberately passed here; Eq binds
// []byte as one value.
func In[T any](column string, values ...T) Condition { return inValues(Col(column), "IN", values) }

// NotIn explicitly expands a homogeneous list for a named column. An empty
// list is TRUE.
func NotIn[T any](column string, values ...T) Condition {
	return inValues(Col(column), "NOT IN", values)
}

// In expands bound values for e. An empty list is FALSE.
func (e Expr) In(values ...any) Condition { return inValues(e, "IN", values) }

// NotIn expands bound values for e. An empty list is TRUE.
func (e Expr) NotIn(values ...any) Condition { return inValues(e, "NOT IN", values) }

// InExpr expands SQL expressions for e. An empty list is FALSE.
func (e Expr) InExpr(values ...Expr) Condition { return inExpressions(e, "IN", values) }

// NotInExpr expands SQL expressions for e. An empty list is TRUE.
func (e Expr) NotInExpr(values ...Expr) Condition { return inExpressions(e, "NOT IN", values) }

// InQuery compares e with the one-column result of query.
func (e Expr) InQuery(query Rowset) Condition { return inQuery(e, "IN", query, 1) }

// NotInQuery compares e with the one-column result of query using NOT IN.
func (e Expr) NotInQuery(query Rowset) Condition { return inQuery(e, "NOT IN", query, 1) }
func inQuery(e Expr, operator string, query Rowset, width int) Condition {
	return AsCondition(Expr{kind: exprMembership, text: operator, value: &membershipExpression{left: e, query: query, width: width}})
}

func (w *renderer) membership(e Expr) {
	n := ownedPayload[*membershipExpression](e.value)
	w.byte('(')
	w.expr(n.left)
	w.byte(' ')
	w.text(e.text)
	w.text(" (")
	if n.query != nil {
		width := statementWidth(n.query, w.options.MaxDepth)
		if n.width >= 0 && width >= 0 && n.width != width {
			w.fail(ErrInvalid, "IN", "subquery projection width differs from the left operand")
			return
		}
		w.statement(n.query)
	} else {
		if !w.require(len(n.values) != 0, "IN", "nil subquery") {
			return
		}
		w.exprs(n.values, ", ")
	}
	w.text("))")
}

// Exists tests whether query returns at least one row.
func Exists(query Rowset) Condition {
	return AsCondition(Expr{kind: exprExists, text: "EXISTS ", value: &subqueryExpression{query: query}})
}

// NotExists tests whether query returns no rows.
func NotExists(query Rowset) Condition { return Not(Exists(query)) }

// AnyArray / AllArray build SQL quantified operands. The corresponding query
// forms avoid an extra scalar-subquery wrapper and validate one-column output.
// AnyArray returns a quantified operand for comparison with any element of an
// SQL array expression.
func AnyArray(array Expr) Expr {
	return Expr{kind: exprQuantified, text: "ANY", node: &expression{left: array}}
}

// AllArray returns a quantified operand for comparison with every element of
// an SQL array expression.
func AllArray(array Expr) Expr {
	return Expr{kind: exprQuantified, text: "ALL", node: &expression{left: array}}
}

// AnyQuery returns a quantified operand for comparison with any row from a
// one-column query.
func AnyQuery(query Rowset) Expr {
	return Expr{kind: exprExists, text: "ANY ", value: &subqueryExpression{query: query, expectedWidth: 1}}
}

// AllQuery returns a quantified operand for comparison with every row from a
// one-column query.
func AllQuery(query Rowset) Expr {
	return Expr{kind: exprExists, text: "ALL ", value: &subqueryExpression{query: query, expectedWidth: 1}}
}

// EqAny compares left with any value from array.
func EqAny(left, array Expr) Condition { return left.EqExpr(AnyArray(array)) }

// EqAll compares left with every value from array.
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
