package qs

// RowExpr is a heterogeneous SQL row. Tuple2 / Tuple3 add compile-time value
// typing without imposing a fixed arity on the main query builder.
type RowExpr struct {
	expr  Expr
	width int
}

func (RowExpr) qxExpression() {}

// Row constructs a heterogeneous SQL ROW expression. An empty row is invalid.
func Row(expressions ...Expr) RowExpr {
	if len(expressions) == 0 {
		return RowExpr{expr: invalidExpr("row", "row requires at least one expression")}
	}
	return RowExpr{expr: listExpr(sqlRow, expressions), width: projectionWidth(expressions)}
}

// Tuple uses PostgreSQL's parenthesized row syntax. A single parenthesized
// expression is a scalar, so tuples require at least two expressions.
//
// Tuple returns an invalid row when fewer than two expressions are supplied.
func Tuple(expressions ...Expr) RowExpr {
	if len(expressions) < 2 {
		return RowExpr{expr: invalidExpr("tuple", "requires at least two expressions")}
	}
	return RowExpr{expr: listExpr("", expressions), width: projectionWidth(expressions)}
}

// Expr returns the SQL expression represented by r.
func (r RowExpr) Expr() Expr { return r.expr }

// As aliases r with a SQL identifier.
func (r RowExpr) As(alias string) Expr { return r.expr.As(alias) }

// Eq compares two rows for equality.
func (r RowExpr) Eq(other RowExpr) Condition { return r.compare("=", other) }

// Ne compares two rows for inequality.
func (r RowExpr) Ne(other RowExpr) Condition { return r.compare("<>", other) }

// Lt compares two rows using less-than.
func (r RowExpr) Lt(other RowExpr) Condition { return r.compare("<", other) }

// Lte compares two rows using less-than-or-equal.
func (r RowExpr) Lte(other RowExpr) Condition { return r.compare("<=", other) }

// Gt compares two rows using greater-than.
func (r RowExpr) Gt(other RowExpr) Condition { return r.compare(">", other) }

// Gte compares two rows using greater-than-or-equal.
func (r RowExpr) Gte(other RowExpr) Condition { return r.compare(">=", other) }

// IsNotDistinctFrom compares two rows using PostgreSQL's NULL-safe equality.
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
	return RowExpr{expr: Expr{kind: exprList, text: sqlRow, value: exprs}, width: len(exprs)}
}

// EqValues compares r with a row built from bound values.
func (r RowExpr) EqValues(values ...any) Condition { return r.Eq(rowValues(values)) }

// NeValues compares r for inequality with a row built from bound values.
func (r RowExpr) NeValues(values ...any) Condition { return r.Ne(rowValues(values)) }

// LtValues compares r with a row built from bound values using less-than.
func (r RowExpr) LtValues(values ...any) Condition { return r.Lt(rowValues(values)) }

// LteValues compares r with a row built from bound values using less-than-or-equal.
func (r RowExpr) LteValues(values ...any) Condition { return r.Lte(rowValues(values)) }

// GtValues compares r with a row built from bound values using greater-than.
func (r RowExpr) GtValues(values ...any) Condition { return r.Gt(rowValues(values)) }

// GteValues compares r with a row built from bound values using greater-than-or-equal.
func (r RowExpr) GteValues(values ...any) Condition { return r.Gte(rowValues(values)) }

// InQuery compares r with rows returned by query. The query width must match r.
func (r RowExpr) InQuery(query Rowset) Condition { return inQuery(r.expr, "IN", query, r.width) }

// NotInQuery compares r with rows returned by query using NOT IN.
func (r RowExpr) NotInQuery(query Rowset) Condition { return inQuery(r.expr, "NOT IN", query, r.width) }

// In compares r with explicitly supplied rows. An empty list is FALSE.
func (r RowExpr) In(rows ...RowExpr) Condition { return r.in("IN", rows) }

// NotIn compares r with explicitly supplied rows. An empty list is TRUE.
func (r RowExpr) NotIn(rows ...RowExpr) Condition { return r.in("NOT IN", rows) }
func (r RowExpr) in(operator string, rows []RowExpr) Condition {
	exprs := make([]Expr, len(rows))
	for i, row := range rows {
		if row.width == 0 || r.width == 0 || row.width >= 0 && r.width >= 0 && row.width != r.width {
			return AsCondition(invalidExpr("row IN", "row arities differ"))
		}
		exprs[i] = row.expr
	}
	return inOwnedExpressions(r.expr, operator, exprs)
}

// Field carries an application's non-NULL Go payload type. It does not prove
// SQL nullability, existence, driver codec support or SQL operator availability.
type Field[T any] struct{ expr Expr }

// Typed creates a field whose comparisons and assignments bind values of T.
func Typed[T any](column string) Field[T] { return Field[T]{expr: Col(column)} }

// TypedExpr creates a field backed by an existing expression and typed as T.
func TypedExpr[T any](expr Expr) Field[T] { return Field[T]{expr: expr} }

func (Field[T]) qxExpression() {}

// Expr returns the SQL expression represented by f.
func (f Field[T]) Expr() Expr { return f.expr }

// As aliases f with a SQL identifier.
func (f Field[T]) As(alias string) Expr { return f.expr.As(alias) }

// Asc orders by f in ascending order.
func (f Field[T]) Asc() Order { return f.expr.Asc() }

// Desc orders by f in descending order.
func (f Field[T]) Desc() Order { return f.expr.Desc() }

// Eq compares f with a bound value of T.
func (f Field[T]) Eq(value T) Condition { return f.expr.Eq(value) }

// Ne compares f with a bound value of T using inequality.
func (f Field[T]) Ne(value T) Condition { return f.expr.Ne(value) }

// Lt compares f with a bound value of T using less-than.
func (f Field[T]) Lt(value T) Condition { return f.expr.Lt(value) }

// Lte compares f with a bound value of T using less-than-or-equal.
func (f Field[T]) Lte(value T) Condition { return f.expr.Lte(value) }

// Gt compares f with a bound value of T using greater-than.
func (f Field[T]) Gt(value T) Condition { return f.expr.Gt(value) }

// Gte compares f with a bound value of T using greater-than-or-equal.
func (f Field[T]) Gte(value T) Condition { return f.expr.Gte(value) }

// EqField compares f with another field of the same Go type.
func (f Field[T]) EqField(other Field[T]) Condition { return f.expr.EqExpr(other.expr) }

// NeField compares f with another field of the same Go type using inequality.
func (f Field[T]) NeField(other Field[T]) Condition { return f.expr.NeExpr(other.expr) }

// LtField compares f with another field of the same Go type using less-than.
func (f Field[T]) LtField(other Field[T]) Condition { return f.expr.LtExpr(other.expr) }

// GtField compares f with another field of the same Go type using greater-than.
func (f Field[T]) GtField(other Field[T]) Condition { return f.expr.GtExpr(other.expr) }

// IsNull tests whether f is SQL NULL.
func (f Field[T]) IsNull() Condition { return f.expr.IsNull() }

// IsNotNull tests whether f is not SQL NULL.
func (f Field[T]) IsNotNull() Condition { return f.expr.IsNotNull() }

// IsDistinctFrom compares f with a nullable bound value.
func (f Field[T]) IsDistinctFrom(value Null[T]) Condition {
	return f.expr.IsDistinctFromExpr(ParamNull(value))
}

// IsNotDistinctFrom compares f with a nullable bound value using NULL-safe
// equality.
func (f Field[T]) IsNotDistinctFrom(value Null[T]) Condition {
	return f.expr.IsNotDistinctFromExpr(ParamNull(value))
}

// In compares f with explicitly expanded values. An empty list is FALSE.
func (f Field[T]) In(values ...T) Condition { return inValues(f.expr, "IN", values) }

// NotIn compares f with explicitly expanded values. An empty list is TRUE.
func (f Field[T]) NotIn(values ...T) Condition { return inValues(f.expr, "NOT IN", values) }

// Between checks whether f lies inclusively between two bound values.
func (f Field[T]) Between(lower, upper T) Condition { return f.expr.Between(lower, upper) }

// InQuery compares f with the one-column result of query.
func (f Field[T]) InQuery(query Rowset) Condition { return f.expr.InQuery(query) }

// Set assigns a bound value of T to f.
func (f Field[T]) Set(value T) Assignment { return assignTypedTarget(f.expr, Write(Param(value))) }

// SetField assigns another field of the same Go type to f.
func (f Field[T]) SetField(value Field[T]) Assignment {
	return assignTypedTarget(f.expr, Write(value.expr))
}

// SetNullable assigns a nullable value to f, binding NULL when value is invalid.
func (f Field[T]) SetNullable(value Null[T]) Assignment {
	return assignTypedTarget(f.expr, Write(ParamNull(value)))
}

// SetNull assigns the SQL NULL literal to f.
func (f Field[T]) SetNull() Assignment { return assignTypedTarget(f.expr, Write(NullLiteral())) }

// SetDefault assigns the destination column's DEFAULT to f.
func (f Field[T]) SetDefault() Assignment { return assignTypedTarget(f.expr, Default()) }

// LteField compares f with another field of the same Go type using less-than-or-equal.
func (f Field[T]) LteField(other Field[T]) Condition { return f.expr.LteExpr(other.expr) }

// GteField compares f with another field of the same Go type using greater-than-or-equal.
func (f Field[T]) GteField(other Field[T]) Condition { return f.expr.GteExpr(other.expr) }

// NotBetween checks whether f lies outside two inclusive bound values.
func (f Field[T]) NotBetween(lower, upper T) Condition { return f.expr.NotBetween(lower, upper) }

// NotInQuery compares f with the one-column result of query using NOT IN.
func (f Field[T]) NotInQuery(query Rowset) Condition { return f.expr.NotInQuery(query) }

// IsDistinctFromField compares f with another field using NULL-safe distinctness.
func (f Field[T]) IsDistinctFromField(other Field[T]) Condition {
	return f.expr.IsDistinctFromExpr(other.expr)
}

// IsNotDistinctFromField compares f with another field using NULL-safe equality.
func (f Field[T]) IsNotDistinctFromField(other Field[T]) Condition {
	return f.expr.IsNotDistinctFromExpr(other.expr)
}

// IsDistinctFrom compares two rows using PostgreSQL's NULL-safe distinctness.
func (r RowExpr) IsDistinctFrom(other RowExpr) Condition { return r.compare("IS DISTINCT FROM", other) }
