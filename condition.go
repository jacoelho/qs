package qs

import "slices"

// Condition is a SQL boolean expression. Unknown (SQL NULL) retains PostgreSQL
// three-valued semantics; qs never rewrites equality according to a bound value.
type Condition struct{ expr Expr }

// Expr returns the underlying boolean expression.
func (c Condition) Expr() Expr { return c.expr }

// Condition treats e as a SQL condition, for a boolean column, a boolean
// parameter or trusted syntax: Col("active").Condition(). It is the inverse of
// Condition.Expr. When qs combines a condition from UnsafeSQL or Fragment with
// another condition, in a WHERE, HAVING or ON list, with a SeekAfter cursor,
// or through And, Or or Not, it parenthesizes that condition. A lone condition
// renders verbatim.
func (e Expr) Condition() Condition { return Condition{e} }

// As names the condition's boolean value as an output column.
func (c Condition) As(alias string) Expr { return c.expr.As(alias) }

// Asc orders by the condition's boolean value, FALSE first.
func (c Condition) Asc() Order { return c.expr.Asc() }

// Desc orders by the condition's boolean value, TRUE first.
func (c Condition) Desc() Order { return c.expr.Desc() }

// True returns a condition that is always TRUE.
func True() Condition { return LiteralBool(true).Condition() }

// False returns a condition that is always FALSE.
func False() Condition { return LiteralBool(false).Condition() }

// And combines conditions with SQL AND. With no conditions, it returns TRUE.
func And(conditions ...Condition) Condition { return junction(sqlAnd, true, conditions) }

// Or combines conditions with SQL OR. With no conditions, it returns FALSE.
func Or(conditions ...Condition) Condition { return junction(sqlOr, false, conditions) }

// Not negates a condition while preserving SQL three-valued logic.
func Not(condition Condition) Condition { return prefix(sqlNot, condition.bounded()).Condition() }

// And combines c with each condition in other. With no other conditions, it
// returns c unchanged.
func (c Condition) And(other ...Condition) Condition {
	return c.junction(sqlAnd, other)
}

// Or combines c with each condition in other. With no other conditions, it
// returns c unchanged.
func (c Condition) Or(other ...Condition) Condition {
	return c.junction(sqlOr, other)
}

// Not negates c while preserving SQL three-valued logic.
func (c Condition) Not() Condition { return Not(c) }

// The spellings And, Or and Not store, which rowIndependent reads back.
const (
	sqlAnd = " AND "
	sqlOr  = " OR "
	sqlNot = "NOT"
)

func junction(operator string, identity bool, conditions []Condition) Condition {
	if len(conditions) == 0 {
		return LiteralBool(identity).Condition()
	}
	return conditions[0].junction(operator, conditions[1:])
}

func (c Condition) junction(operator string, other []Condition) Condition {
	if len(other) == 0 {
		return c
	}
	values := make([]Expr, 1+len(other))
	values[0] = c.bounded()
	for i := range other {
		values[i+1] = other[i].bounded()
	}
	return nodeExpr(&groupExpression{separator: operator, items: values}).Condition()
}

// bounded returns the condition as an operand that qs combines with another.
// Structural conditions render their own parentheses. Trusted SQL does not, so
// an OR inside it would otherwise bind to the neighbouring operand.
func (c Condition) bounded() Expr {
	if c.expr.opaque() {
		return c.expr.Parenthesized()
	}
	return c.expr
}

func compare(left Expr, operator string, right Expr) Condition {
	return binary(left, operator, right).Condition()
}

const nilComparison = "a bound NULL never compares true; use IsNull or a DISTINCT form"

// nilParameter reports whether e binds untyped nil.
func nilParameter(e Expr) bool {
	value, ok := e.bound()
	return ok && value == nil
}

// nilOperand reports whether e binds untyped nil directly, as a field of a row
// constructor at any depth, or as the array of ANY or ALL: none of those
// compares true either.
func nilOperand(e Expr) bool {
	switch n := e.node().(type) {
	case *listExpression:
		return (n.prefix == sqlRow || n.prefix == "") && slices.ContainsFunc(n.items, nilOperand)
	case *quantifiedExpression:
		return nilParameter(n.array)
	default:
		return nilParameter(e)
	}
}

// comparison builds an ordering or equality comparison. A bound untyped nil on
// either side is SQL NULL, which never compares true and is almost always a
// bug, so it fails rendering instead of silently removing rows. Typed nil
// pointers and driver values encoding NULL are indistinguishable from data
// here and still bind.
func comparison(left Expr, operator string, right Expr) Condition {
	if nilOperand(left) || nilOperand(right) {
		return invalidExpr("comparison", nilComparison).Condition()
	}
	return compare(left, operator, right)
}

// Eq compares e with another expression using SQL equality. Bind a Go value
// with Param, or use Typed for comparisons that take values directly. A bound
// untyped nil fails rendering with ErrInvalid; use IsNull for a NULL test.
func (e Expr) Eq(other Expr) Condition { return comparison(e, "=", other) }

// Ne compares e with another expression using SQL inequality.
func (e Expr) Ne(other Expr) Condition { return comparison(e, "<>", other) }

// Lt compares e with another expression using SQL less-than.
func (e Expr) Lt(other Expr) Condition { return comparison(e, "<", other) }

// Lte compares e with another expression using SQL less-than-or-equal.
func (e Expr) Lte(other Expr) Condition { return comparison(e, "<=", other) }

// Gt compares e with another expression using SQL greater-than.
func (e Expr) Gt(other Expr) Condition { return comparison(e, ">", other) }

// Gte compares e with another expression using SQL greater-than-or-equal.
func (e Expr) Gte(other Expr) Condition { return comparison(e, ">=", other) }

// IsDistinctFrom compares e with another expression using PostgreSQL's
// NULL-safe distinctness operator.
func (e Expr) IsDistinctFrom(other Expr) Condition { return compare(e, "IS DISTINCT FROM", other) }

// IsNotDistinctFrom compares e with another expression using PostgreSQL's
// NULL-safe equality operator.
func (e Expr) IsNotDistinctFrom(other Expr) Condition {
	return compare(e, "IS NOT DISTINCT FROM", other)
}

// IsNull tests whether e is SQL NULL.
func (e Expr) IsNull() Condition { return postfix(e, "IS NULL").Condition() }

// IsNotNull tests whether e is not SQL NULL.
func (e Expr) IsNotNull() Condition { return postfix(e, "IS NOT NULL").Condition() }

// IsTrue tests whether e is TRUE, excluding FALSE and NULL.
func (e Expr) IsTrue() Condition { return postfix(e, "IS TRUE").Condition() }

// IsNotTrue tests whether e is not TRUE, including FALSE and NULL.
func (e Expr) IsNotTrue() Condition { return postfix(e, "IS NOT TRUE").Condition() }

// IsFalse tests whether e is FALSE, excluding TRUE and NULL.
func (e Expr) IsFalse() Condition { return postfix(e, "IS FALSE").Condition() }

// IsNotFalse tests whether e is not FALSE, including TRUE and NULL.
func (e Expr) IsNotFalse() Condition { return postfix(e, "IS NOT FALSE").Condition() }

// IsUnknown tests whether e is SQL NULL.
func (e Expr) IsUnknown() Condition { return postfix(e, "IS UNKNOWN").Condition() }

// IsNotUnknown tests whether e is not SQL NULL.
func (e Expr) IsNotUnknown() Condition { return postfix(e, "IS NOT UNKNOWN").Condition() }

func between(value Expr, operator string, lower, upper Expr) Condition {
	if nilOperand(value) || nilOperand(lower) || nilOperand(upper) {
		return invalidExpr("comparison", nilComparison).Condition()
	}
	return nodeExpr(&betweenExpression{value, operator, lower, upper}).Condition()
}

// Between checks e against two expressions using BETWEEN, inclusively.
func (e Expr) Between(lower, upper Expr) Condition { return between(e, "BETWEEN", lower, upper) }

// NotBetween checks e against two expressions using NOT BETWEEN.
func (e Expr) NotBetween(lower, upper Expr) Condition {
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

// membership builds IN or NOT IN over an expression list the caller no longer
// writes. An empty list is FALSE for IN and TRUE for NOT IN.
func membership(left Expr, operator string, values []Expr) Condition {
	if len(values) == 0 {
		if operator == "NOT IN" {
			return True()
		}
		return False()
	}
	if nilOperand(left) || slices.ContainsFunc(values, nilOperand) {
		return invalidExpr("comparison", nilComparison).Condition()
	}
	return nodeExpr(&membershipExpression{left: left, operator: operator, values: values}).Condition()
}

func inValues[T any](left Expr, operator string, values []T) Condition {
	exprs := make([]Expr, len(values))
	for i := range values {
		exprs[i] = parameter(values[i])
	}
	return membership(left, operator, exprs)
}

// In compares e with a list of expressions. An empty list is FALSE. A bound
// untyped nil fails rendering, as it does for Eq.
func (e Expr) In(values ...Expr) Condition { return membership(e, "IN", cloneSlice(values)) }

// NotIn compares e with a list of expressions. An empty list is TRUE.
func (e Expr) NotIn(values ...Expr) Condition { return membership(e, "NOT IN", cloneSlice(values)) }

// InQuery compares e with the one-column result of query.
func (e Expr) InQuery(query Rowset) Condition { return inQuery(e, "IN", query, 1) }

// NotInQuery compares e with the one-column result of query using NOT IN.
func (e Expr) NotInQuery(query Rowset) Condition { return inQuery(e, "NOT IN", query, 1) }
func inQuery(e Expr, operator string, query Rowset, width int) Condition {
	if nilOperand(e) {
		return invalidExpr("comparison", nilComparison).Condition()
	}
	return nodeExpr(&membershipExpression{left: e, operator: operator, query: ownedRowset(query), width: width}).Condition()
}

func (w *renderer) membership(n *membershipExpression) {
	w.byte('(')
	w.expr(n.left)
	if w.stopped("membership left", 0) {
		return
	}
	w.byte(' ')
	w.text(n.operator)
	w.text(" (")
	if n.query != nil {
		width := statementWidth(n.query, w.options.MaxDepth)
		if n.width >= 0 && width >= 0 && n.width != width {
			w.fail(ErrInvalid, "IN", "subquery projection width differs from the left operand")
			return
		}
		w.statement(n.query)
		if w.stopped("membership subquery", 0) {
			return
		}
	} else {
		if !w.require(len(n.values) != 0, "IN", "nil subquery") {
			return
		}
		w.exprs(n.values, ", ")
		if w.stopped("membership values", 0) {
			return
		}
	}
	w.text("))")
}

// Exists tests whether query returns at least one row.
func Exists(query Rowset) Condition {
	return nodeExpr(&subqueryExpression{query: ownedRowset(query), keyword: "EXISTS "}).Condition()
}

// NotExists tests whether query returns no rows.
func NotExists(query Rowset) Condition { return Not(Exists(query)) }

// AnyArray / AllArray build SQL quantified operands. The corresponding query
// forms avoid an extra scalar-subquery wrapper and validate one-column output.
// AnyArray returns a quantified operand for comparison with any element of an
// SQL array expression.
func AnyArray(array Expr) Expr {
	return nodeExpr(&quantifiedExpression{"ANY", array})
}

// AllArray returns a quantified operand for comparison with every element of
// an SQL array expression.
func AllArray(array Expr) Expr {
	return nodeExpr(&quantifiedExpression{"ALL", array})
}

// AnyQuery returns a quantified operand for comparison with any row from a
// one-column query.
func AnyQuery(query Rowset) Expr {
	return nodeExpr(&subqueryExpression{query: ownedRowset(query), keyword: "ANY ", expectedWidth: 1})
}

// AllQuery returns a quantified operand for comparison with every row from a
// one-column query.
func AllQuery(query Rowset) Expr {
	return nodeExpr(&subqueryExpression{query: ownedRowset(query), keyword: "ALL ", expectedWidth: 1})
}

// EqAny compares left with any value from array.
func EqAny(left, array Expr) Condition { return left.Eq(AnyArray(array)) }

// EqAll compares left with every value from array.
func EqAll(left, array Expr) Condition { return left.Eq(AllArray(array)) }

func (w *renderer) conditions(conditions []Condition) {
	w.conjunction(conditions, len(conditions) > 1)
}

// bounded renders e as one operand of an operator that qs adds. Trusted SQL
// carries no precedence boundary of its own, so it is parenthesized.
func (w *renderer) bounded(e Expr) {
	if !e.opaque() {
		w.expr(e)
		return
	}
	w.byte('(')
	w.expr(e)
	w.byte(')')
}

// conjunction renders conditions joined by AND. A combined conjunction, which
// has more than one conjunct including any that the caller appends, renders
// each opaque condition as a bounded operand. A lone opaque condition stays
// verbatim, so trusted syntax such as CURRENT OF remains valid.
func (w *renderer) conjunction(conditions []Condition, combined bool) {
	if w.err != nil {
		return
	}
	for i, c := range conditions {
		if i != 0 {
			w.text(" AND ")
		}
		if combined {
			w.bounded(c.expr)
		} else {
			w.expr(c.expr)
		}
		if w.stopped("condition", i+1) {
			return
		}
	}
}
