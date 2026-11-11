package parsetree

import (
	"strings"

	qs "github.com/jacoelho/qs"
)

func (b *builder) exprs(items []node) []qs.Expr {
	expressions := make([]qs.Expr, len(items))
	for index, item := range items {
		expressions[index] = b.expr(item)
	}
	return expressions
}

// condition builds an expression in a boolean clause position.
func (b *builder) condition(n node) qs.Condition { return b.expr(n).Condition() }

func (b *builder) conditions(items []node) []qs.Condition {
	conditions := make([]qs.Condition, len(items))
	for index, item := range items {
		conditions[index] = b.condition(item)
	}
	return conditions
}

func (b *builder) expr(n node) qs.Expr {
	if b.stopped() {
		return qs.Expr{}
	}
	if !n.present() {
		b.gapf("missing expression")
		return qs.Expr{}
	}
	switch n.tag {
	case "ColumnRef":
		return b.columnRef(n)
	case tagConstant:
		return b.constant(n)
	case "ParamRef":
		return qs.Param(b.param(int(n.integer("number"))))
	case "SetToDefault":
		b.gapf("DEFAULT is only allowed directly in a write position")
	case "TypeCast":
		return b.expr(n.node("arg")).Cast(b.dataType(n.record("typeName")))
	case "A_Expr":
		return b.operatorExpr(n)
	case "BoolExpr":
		return b.boolExpr(n)
	case "NullTest":
		return b.nullTest(n)
	case "BooleanTest":
		return b.booleanTest(n)
	case tagFuncCall:
		return b.function(n)
	case "SubLink":
		return b.subLink(n)
	case tagRow:
		return b.row(n).Expr()
	case "A_ArrayExpr":
		return qs.Array(b.exprs(n.nodes("elements"))...)
	case "CaseExpr":
		return b.caseExpr(n)
	case "CoalesceExpr":
		return variadic(b, "COALESCE", qs.Coalesce, b.exprs(n.nodes("args")))
	case "MinMaxExpr":
		if n.text("op") == "IS_GREATEST" {
			return variadic(b, "GREATEST", qs.Greatest, b.exprs(n.nodes("args")))
		}
		return variadic(b, "LEAST", qs.Least, b.exprs(n.nodes("args")))
	case "GroupingFunc":
		return qs.Grouping(b.exprs(n.nodes("args"))...)
	case "CollateClause":
		value := b.expr(n.node("arg"))
		return qualified(b, "collation", value.Collate, n.strings("collname"))
	case "SQLValueFunction":
		return b.sqlValueFunction(n)
	case "MergeSupportFunc":
		return qs.MergeAction()
	case tagStar:
		return qs.Star()
	case "A_Indirection":
		return b.indirection(n)
	case "GroupingSet":
		return b.groupExpr(n)
	default:
		return b.jsonExpr(n)
	}
	return qs.Expr{}
}

// variadic calls a qs constructor that requires at least one argument.
func variadic[T, R any](b *builder, what string, build func(first T, rest ...T) R, values []T) R {
	if len(values) == 0 {
		b.gapf("%s has no arguments", what)
		var zero R
		return zero
	}
	return build(values[0], values[1:]...)
}

// qualified calls a qs constructor taking one identifier per name part.
func qualified[R any](b *builder, what string, build func(first string, rest ...string) R, parts []string) R {
	return variadic(b, what, build, parts)
}

func (b *builder) columnRef(n node) qs.Expr {
	fields := n.nodes("fields")
	if len(fields) == 0 {
		b.gapf("ColumnRef has no fields")
		return qs.Expr{}
	}
	parts := make([]string, 0, len(fields))
	for index, field := range fields {
		switch field.tag {
		case tagString:
			parts = append(parts, field.text("sval"))
		case tagStar:
			if index != len(fields)-1 {
				b.gapf("ColumnRef star is not last")
				return qs.Expr{}
			}
			return qs.Star(parts...)
		default:
			b.gapf("ColumnRef field %s", field.tag)
			return qs.Expr{}
		}
	}
	return qs.Col(parts[0], parts[1:]...)
}

// constant reads an A_Const, whose value is an inlined Integer, Float,
// Boolean, String or BitString. Protobuf JSON omits zero values, so an empty
// value object is 0, false or "".
func (b *builder) constant(n node) qs.Expr {
	switch {
	case n.flag("isnull"):
		return qs.NullLiteral()
	case n.has("ival"):
		return qs.LiteralInt(n.record("ival").integer("ival"))
	case n.has("fval"):
		// Keep PostgreSQL's token spelling: a float64 round trip changes
		// large decimal and exponent literals.
		return qs.LiteralNumeric(n.record("fval").text("fval"))
	case n.has("sval"):
		return qs.LiteralString(n.record("sval").text("sval"))
	case n.has("boolval"):
		return qs.LiteralBool(n.record("boolval").flag("boolval"))
	case n.has("bsval"):
		value := n.record("bsval").text("bsval")
		switch {
		case strings.HasPrefix(value, "b"), strings.HasPrefix(value, "B"):
			return qs.LiteralBit(value[1:])
		case strings.HasPrefix(value, "x"), strings.HasPrefix(value, "X"):
			return qs.LiteralHex(value[1:])
		}
		b.gapf("unknown bit string literal prefix")
	default:
		b.gapf("constant has no value")
	}
	return qs.Expr{}
}

func (b *builder) boolExpr(n node) qs.Expr {
	arguments := b.conditions(n.nodes("args"))
	switch operation := n.text("boolop"); {
	case operation == "AND_EXPR":
		return qs.And(arguments...).Expr()
	case operation == "OR_EXPR":
		return qs.Or(arguments...).Expr()
	case operation == "NOT_EXPR" && len(arguments) == 1:
		return qs.Not(arguments[0]).Expr()
	default:
		b.gapf("unsupported BoolExpr shape %s", operation)
		return qs.Expr{}
	}
}

func (b *builder) nullTest(n node) qs.Expr {
	value := b.expr(n.node("arg"))
	switch kind := n.text("nulltesttype"); kind {
	case "IS_NULL":
		return value.IsNull().Expr()
	case "IS_NOT_NULL":
		return value.IsNotNull().Expr()
	default:
		b.gapf("unsupported NullTest %s", kind)
		return qs.Expr{}
	}
}

func (b *builder) booleanTest(n node) qs.Expr {
	value := b.expr(n.node("arg"))
	switch kind := n.text("booltesttype"); kind {
	case "IS_TRUE":
		return value.IsTrue().Expr()
	case "IS_NOT_TRUE":
		return value.IsNotTrue().Expr()
	case "IS_FALSE":
		return value.IsFalse().Expr()
	case "IS_NOT_FALSE":
		return value.IsNotFalse().Expr()
	case "IS_UNKNOWN":
		return value.IsUnknown().Expr()
	case "IS_NOT_UNKNOWN":
		return value.IsNotUnknown().Expr()
	default:
		b.gapf("unsupported BooleanTest %s", kind)
		return qs.Expr{}
	}
}

// row keeps ROW(...) and bare (...) apart: the parser records the spelling.
func (b *builder) row(n node) qs.RowExpr {
	arguments := n.nodes("args")
	if len(arguments) == 0 {
		b.gapf("ROW() has no constructor")
		return qs.RowExpr{}
	}
	if n.text("row_format") == "COERCE_IMPLICIT_CAST" {
		return qs.Tuple(b.exprs(arguments)...)
	}
	return qs.Row(b.exprs(arguments)...)
}

func (b *builder) subLink(n node) qs.Expr {
	kind := n.text("subLinkType")
	switch kind {
	case "EXISTS_SUBLINK":
		return qs.Exists(b.rowset(n.node("subselect"))).Expr()
	case "EXPR_SUBLINK":
		return qs.Scalar(b.rowset(n.node("subselect")))
	case "ARRAY_SUBLINK":
		return qs.ArrayFrom(b.rowset(n.node("subselect")))
	case "ANY_SUBLINK", sublinkAll:
	default:
		b.gapf("unsupported sublink %s", kind)
		return qs.Expr{}
	}
	test := n.node("testexpr")
	operator := n.strings("operName")
	if len(operator) == 0 {
		if kind == sublinkAll {
			b.gapf("ALL subquery has no comparison operator")
			return qs.Expr{}
		}
		// A row operand keeps its width in qs.RowExpr.
		if test.tag == tagRow {
			left := b.row(test)
			return left.InQuery(b.rowset(n.node("subselect"))).Expr()
		}
		left := b.expr(test)
		return left.InQuery(b.rowset(n.node("subselect"))).Expr()
	}
	left := b.expr(test)
	query := b.rowset(n.node("subselect"))
	if kind == sublinkAll {
		return b.binaryOperator(left, operator, qs.AllQuery(query))
	}
	return b.binaryOperator(left, operator, qs.AnyQuery(query))
}

// binaryOperator applies a possibly schema-qualified operator name.
func (b *builder) binaryOperator(left qs.Expr, name []string, right qs.Expr) qs.Expr {
	switch len(name) {
	case 1:
		return qs.Operator(left, name[0], right)
	case 2:
		return qs.QualifiedOperator(left, name[0], name[1], right)
	default:
		b.gapf("operator name has %d parts", len(name))
		return qs.Expr{}
	}
}

func (b *builder) operatorExpr(n node) qs.Expr {
	name := n.strings("name")
	if len(name) == 0 {
		b.gapf("operator expression has no name")
		return qs.Expr{}
	}
	operator := name[0]
	kind := n.text("kind")
	switch kind {
	case "AEXPR_LIKE", "AEXPR_ILIKE", "AEXPR_SIMILAR":
		return b.pattern(n, kind, operator)
	}
	var left qs.Expr
	if n.has("lexpr") {
		left = b.expr(n.node("lexpr"))
	}
	right := n.node("rexpr")
	switch kind {
	case "AEXPR_IN":
		values := b.exprs(right.members())
		switch operator {
		case "=":
			return left.In(values...).Expr()
		case "<>":
			return left.NotIn(values...).Expr()
		}
		b.gapf("IN operator %q", operator)
		return qs.Expr{}
	case "AEXPR_BETWEEN", "AEXPR_NOT_BETWEEN", "AEXPR_BETWEEN_SYM", "AEXPR_NOT_BETWEEN_SYM":
		bounds := b.exprs(right.members())
		if len(bounds) != 2 {
			b.gapf("BETWEEN requires two bounds")
			return qs.Expr{}
		}
		switch kind {
		case "AEXPR_BETWEEN":
			return left.Between(bounds[0], bounds[1]).Expr()
		case "AEXPR_NOT_BETWEEN":
			return left.NotBetween(bounds[0], bounds[1]).Expr()
		case "AEXPR_BETWEEN_SYM":
			return left.BetweenSymmetric(bounds[0], bounds[1]).Expr()
		default:
			return left.NotBetweenSymmetric(bounds[0], bounds[1]).Expr()
		}
	case "AEXPR_NULLIF":
		return qs.NullIf(left, b.expr(right))
	case "AEXPR_DISTINCT":
		return left.IsDistinctFrom(b.expr(right)).Expr()
	case "AEXPR_NOT_DISTINCT":
		return left.IsNotDistinctFrom(b.expr(right)).Expr()
	case "AEXPR_OP_ANY":
		return b.binaryOperator(left, name, qs.AnyArray(b.expr(right)))
	case "AEXPR_OP_ALL":
		return b.binaryOperator(left, name, qs.AllArray(b.expr(right)))
	case "AEXPR_OP":
	default:
		b.gapf("unsupported operator expression %s", kind)
		return qs.Expr{}
	}
	if !n.has("lexpr") {
		if len(name) != 1 {
			b.gapf("qualified prefix operator")
			return qs.Expr{}
		}
		return qs.PrefixOperator(operator, b.expr(right))
	}
	return b.binaryOperator(left, name, b.expr(right))
}

// pattern builds LIKE, ILIKE and SIMILAR TO. The parser moves an ESCAPE
// clause, and every SIMILAR TO pattern, into a pg_catalog helper call.
func (b *builder) pattern(n node, kind, operator string) qs.Expr {
	left := b.expr(n.node("lexpr"))
	right := n.node("rexpr")
	helper := "like_escape"
	if kind == "AEXPR_SIMILAR" {
		helper = "similar_to_escape"
	}
	var escape node
	if right.tag == tagFuncCall {
		if name := right.strings("funcname"); len(name) == 2 && name[0] == "pg_catalog" && name[1] == helper {
			arguments := right.nodes("args")
			switch len(arguments) {
			case 1:
				right = arguments[0]
			case 2:
				right, escape = arguments[0], arguments[1]
			default:
				b.gapf("pattern escape helper has unsupported arity")
				return qs.Expr{}
			}
		}
	}
	pattern := b.expr(right)
	var condition qs.Condition
	switch kind + " " + operator {
	case "AEXPR_LIKE ~~":
		condition = left.LikeExpr(pattern)
	case "AEXPR_LIKE !~~":
		condition = left.NotLikeExpr(pattern)
	case "AEXPR_ILIKE ~~*":
		condition = left.ILikeExpr(pattern)
	case "AEXPR_ILIKE !~~*":
		condition = left.NotILikeExpr(pattern)
	case "AEXPR_SIMILAR ~":
		condition = left.SimilarTo(pattern)
	case "AEXPR_SIMILAR !~":
		condition = left.NotSimilarTo(pattern)
	default:
		b.gapf("pattern operator %q", operator)
		return qs.Expr{}
	}
	if escape.present() {
		condition = condition.EscapeExpr(b.expr(escape))
	}
	return condition.Expr()
}

func (b *builder) caseExpr(n node) qs.Expr {
	branches := n.nodes("args")
	if len(branches) == 0 {
		b.gapf("CASE has no WHEN")
		return qs.Expr{}
	}
	first, branches := branches[0], branches[1:]
	if n.has("arg") {
		simple := qs.CaseOf(b.expr(n.node("arg"))).
			WhenValue(b.expr(first.node("expr")), b.expr(first.node("result")))
		for _, branch := range branches {
			simple = simple.WhenValue(b.expr(branch.node("expr")), b.expr(branch.node("result")))
		}
		if n.has("defresult") {
			simple = simple.Else(b.expr(n.node("defresult")))
		}
		return simple.End()
	}
	searched := qs.Case().When(b.condition(first.node("expr")), b.expr(first.node("result")))
	for _, branch := range branches {
		searched = searched.When(b.condition(branch.node("expr")), b.expr(branch.node("result")))
	}
	if n.has("defresult") {
		searched = searched.Else(b.expr(n.node("defresult")))
	}
	return searched.End()
}

func (b *builder) sqlValueFunction(n node) qs.Expr {
	precision := int(n.integer("typmod"))
	switch operation := n.text("op"); operation {
	case "SVFOP_CURRENT_DATE":
		return qs.CurrentDate()
	case "SVFOP_CURRENT_TIME":
		return qs.CurrentTime()
	case "SVFOP_CURRENT_TIME_N":
		return qs.CurrentTime(precision)
	case "SVFOP_CURRENT_TIMESTAMP":
		return qs.CurrentTimestamp()
	case "SVFOP_CURRENT_TIMESTAMP_N":
		return qs.CurrentTimestamp(precision)
	case "SVFOP_LOCALTIME":
		return qs.LocalTime()
	case "SVFOP_LOCALTIME_N":
		return qs.LocalTime(precision)
	case "SVFOP_LOCALTIMESTAMP":
		return qs.LocalTimestamp()
	case "SVFOP_LOCALTIMESTAMP_N":
		return qs.LocalTimestamp(precision)
	case "SVFOP_CURRENT_USER":
		return qs.CurrentUser()
	case "SVFOP_SESSION_USER":
		return qs.SessionUser()
	case "SVFOP_CURRENT_CATALOG":
		return qs.CurrentCatalog()
	case "SVFOP_CURRENT_SCHEMA":
		return qs.CurrentSchema()
	default:
		b.gapf("%s has no constructor", strings.TrimPrefix(operation, "SVFOP_"))
		return qs.Expr{}
	}
}

func (b *builder) indirection(n node) qs.Expr {
	argument := n.node("arg")
	value := b.expr(argument)
	// The parser keeps a parenthesised indirection as a nested node; applying
	// the next step directly would flatten (a[1])[1] into a[1][1].
	if argument.tag == "A_Indirection" {
		value = value.Parenthesized()
	}
	for _, step := range n.nodes("indirection") {
		switch step.tag {
		case "A_Indices":
			value = b.subscript(value, step)
		case tagString:
			value = value.Field(step.text("sval"))
		case tagStar:
			value = value.Fields()
		default:
			b.gapf("unknown indirection node %s", step.tag)
			return qs.Expr{}
		}
	}
	return value
}

func (b *builder) subscript(value qs.Expr, step node) qs.Expr {
	lower, upper := step.has("lidx"), step.has("uidx")
	switch {
	case step.flag("is_slice") && lower && upper:
		return value.Slice(b.expr(step.node("lidx")), b.expr(step.node("uidx")))
	case step.flag("is_slice") && lower:
		return value.SliceFrom(b.expr(step.node("lidx")))
	case step.flag("is_slice") && upper:
		return value.SliceTo(b.expr(step.node("uidx")))
	case step.flag("is_slice"):
		return value.SliceAll()
	case upper:
		return value.Index(b.expr(step.node("uidx")))
	default:
		b.gapf("array subscript has no index")
		return qs.Expr{}
	}
}
