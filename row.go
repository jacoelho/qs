package qx

// RowExpr is a heterogeneous SQL row. Tuple2 / Tuple3 add compile-time value
// typing without imposing a fixed arity on the main query builder.
type RowExpr struct {
	expr  Expr
	width int
}

func (RowExpr) qxExpression() {}
func Row(expressions ...Expr) RowExpr {
	if len(expressions) == 0 {
		return RowExpr{expr: invalidExpr("row", "row requires at least one expression")}
	}
	return RowExpr{expr: listExpr("ROW", expressions), width: projectionWidth(expressions)}
}

// Tuple uses PostgreSQL's parenthesized row syntax. A single parenthesized
// expression is a scalar, so tuples require at least two expressions.
func Tuple(expressions ...Expr) RowExpr {
	if len(expressions) < 2 {
		return RowExpr{expr: invalidExpr("tuple", "requires at least two expressions")}
	}
	return RowExpr{expr: listExpr("", expressions), width: projectionWidth(expressions)}
}
func (r RowExpr) Expr() Expr                  { return r.expr }
func (r RowExpr) As(alias string) Expr        { return r.expr.As(alias) }
func (r RowExpr) Eq(other RowExpr) Condition  { return r.compare("=", other) }
func (r RowExpr) Ne(other RowExpr) Condition  { return r.compare("<>", other) }
func (r RowExpr) Lt(other RowExpr) Condition  { return r.compare("<", other) }
func (r RowExpr) Lte(other RowExpr) Condition { return r.compare("<=", other) }
func (r RowExpr) Gt(other RowExpr) Condition  { return r.compare(">", other) }
func (r RowExpr) Gte(other RowExpr) Condition { return r.compare(">=", other) }
func (r RowExpr) IsNotDistinctFrom(other RowExpr) Condition {
	return r.compare("IS NOT DISTINCT FROM", other)
}
func (r RowExpr) compare(operator string, other RowExpr) Condition {
	if r.width == 0 || other.width == 0 || r.width >= 0 && other.width >= 0 && r.width != other.width {
		return AsCondition(invalidExpr("row", "row arities differ or are zero"))
	}
	return compare(r.expr, operator, other.expr)
}
func rowValues(values []any) RowExpr {
	if len(values) == 0 {
		return Row()
	}
	exprs := make([]Expr, len(values))
	for i, v := range values {
		exprs[i] = parameter(v)
	}
	return RowExpr{expr: Expr{kind: exprList, text: "ROW", value: exprs}, width: len(exprs)}
}
func (r RowExpr) EqValues(values ...any) Condition  { return r.Eq(rowValues(values)) }
func (r RowExpr) NeValues(values ...any) Condition  { return r.Ne(rowValues(values)) }
func (r RowExpr) LtValues(values ...any) Condition  { return r.Lt(rowValues(values)) }
func (r RowExpr) LteValues(values ...any) Condition { return r.Lte(rowValues(values)) }
func (r RowExpr) GtValues(values ...any) Condition  { return r.Gt(rowValues(values)) }
func (r RowExpr) GteValues(values ...any) Condition { return r.Gte(rowValues(values)) }
func (r RowExpr) InQuery(query Rowset) Condition    { return inQuery(r.expr, "IN", query, r.width) }
func (r RowExpr) NotInQuery(query Rowset) Condition { return inQuery(r.expr, "NOT IN", query, r.width) }
func (r RowExpr) In(rows ...RowExpr) Condition      { return r.in("IN", rows) }
func (r RowExpr) NotIn(rows ...RowExpr) Condition   { return r.in("NOT IN", rows) }
func (r RowExpr) in(operator string, rows []RowExpr) Condition {
	exprs := make([]Expr, len(rows))
	for i, row := range rows {
		if row.width == 0 || r.width == 0 || row.width >= 0 && r.width >= 0 && row.width != r.width {
			return AsCondition(invalidExpr("row IN", "row arities differ"))
		}
		exprs[i] = row.expr
	}
	return inOwnedExpressions(r.expr, operator, exprs, r.width)
}

// Field carries an application's non-NULL Go payload type. It does not prove
// SQL nullability, existence, driver codec support or SQL operator availability.
type Field[T any] struct{ expr Expr }

func Typed[T any](column string) Field[T]           { return Field[T]{expr: Col(column)} }
func TypedExpr[T any](expr Expr) Field[T]           { return Field[T]{expr: expr} }
func (Field[T]) qxExpression()                      {}
func (f Field[T]) Expr() Expr                       { return f.expr }
func (f Field[T]) As(alias string) Expr             { return f.expr.As(alias) }
func (f Field[T]) Asc() Order                       { return f.expr.Asc() }
func (f Field[T]) Desc() Order                      { return f.expr.Desc() }
func (f Field[T]) Eq(value T) Condition             { return f.expr.Eq(value) }
func (f Field[T]) Ne(value T) Condition             { return f.expr.Ne(value) }
func (f Field[T]) Lt(value T) Condition             { return f.expr.Lt(value) }
func (f Field[T]) Lte(value T) Condition            { return f.expr.Lte(value) }
func (f Field[T]) Gt(value T) Condition             { return f.expr.Gt(value) }
func (f Field[T]) Gte(value T) Condition            { return f.expr.Gte(value) }
func (f Field[T]) EqField(other Field[T]) Condition { return f.expr.EqExpr(other.expr) }
func (f Field[T]) NeField(other Field[T]) Condition { return f.expr.NeExpr(other.expr) }
func (f Field[T]) LtField(other Field[T]) Condition { return f.expr.LtExpr(other.expr) }
func (f Field[T]) GtField(other Field[T]) Condition { return f.expr.GtExpr(other.expr) }
func (f Field[T]) IsNull() Condition                { return f.expr.IsNull() }
func (f Field[T]) IsNotNull() Condition             { return f.expr.IsNotNull() }
func (f Field[T]) IsDistinctFrom(value Null[T]) Condition {
	return f.expr.IsDistinctFromExpr(ParamNull(value))
}
func (f Field[T]) IsNotDistinctFrom(value Null[T]) Condition {
	return f.expr.IsNotDistinctFromExpr(ParamNull(value))
}
func (f Field[T]) In(values ...T) Condition           { return inValues(f.expr, "IN", values) }
func (f Field[T]) NotIn(values ...T) Condition        { return inValues(f.expr, "NOT IN", values) }
func (f Field[T]) Between(lower, upper T) Condition   { return f.expr.Between(lower, upper) }
func (f Field[T]) InQuery(query Rowset) Condition     { return f.expr.InQuery(query) }
func (f Field[T]) Set(value T) Assignment             { return assignTarget(f.expr, Param(value)) }
func (f Field[T]) SetField(value Field[T]) Assignment { return assignTarget(f.expr, value.expr) }
func (f Field[T]) SetNullable(value Null[T]) Assignment {
	return assignTarget(f.expr, ParamNull(value))
}
func (f Field[T]) SetNull() Assignment    { return assignTarget(f.expr, NullLiteral()) }
func (f Field[T]) SetDefault() Assignment { return assignTarget(f.expr, Default()) }

func (f Field[T]) LteField(other Field[T]) Condition   { return f.expr.LteExpr(other.expr) }
func (f Field[T]) GteField(other Field[T]) Condition   { return f.expr.GteExpr(other.expr) }
func (f Field[T]) NotBetween(lower, upper T) Condition { return f.expr.NotBetween(lower, upper) }
func (f Field[T]) NotInQuery(query Rowset) Condition   { return f.expr.NotInQuery(query) }
func (f Field[T]) IsDistinctFromField(other Field[T]) Condition {
	return f.expr.IsDistinctFromExpr(other.expr)
}
func (f Field[T]) IsNotDistinctFromField(other Field[T]) Condition {
	return f.expr.IsNotDistinctFromExpr(other.expr)
}
func (r RowExpr) IsDistinctFrom(other RowExpr) Condition { return r.compare("IS DISTINCT FROM", other) }
