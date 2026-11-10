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

// compare builds a row comparison. Ordering and equality reject a bound
// untyped nil field as scalar comparisons do; the NULL-safe DISTINCT forms
// accept it.
func (r RowExpr) compare(operator string, other RowExpr) Condition {
	if r.width == 0 || other.width == 0 || r.width >= 0 && other.width >= 0 && r.width != other.width {
		return AsCondition(invalidExpr("row", "row arities differ or are zero"))
	}
	if operator == "IS DISTINCT FROM" || operator == "IS NOT DISTINCT FROM" {
		return compare(r.expr, operator, other.expr)
	}
	return comparison(r.expr, operator, other.expr)
}

// compareValues compares r with a row of bound values. It serves the typed
// tuples, whose signatures fix the number and Go types of the values.
func (r RowExpr) compareValues(operator string, values ...any) Condition {
	return r.compare(operator, rowValues(values))
}

func rowValues(values []any) RowExpr {
	if len(values) == 0 {
		return Row()
	}
	exprs := make([]Expr, len(values))
	for i, v := range values {
		exprs[i] = parameter(v)
	}
	return RowExpr{expr: nodeExpr(&listExpression{prefix: sqlRow, items: exprs}), width: len(exprs)}
}

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
	return membership(r.expr, operator, exprs)
}

// Field carries an application's non-NULL Go payload type. It does not prove
// SQL nullability, existence, driver codec support or SQL operator availability.
type Field[T any] struct{ expr Expr }

// Typed states that comparisons and assignments of expr bind Go values of T:
// Typed[int64](users.Col("id")).Eq(42). It adds no cast and proves nothing
// about the SQL type.
func Typed[T any](expr Expr) Field[T] { return Field[T]{expr: expr} }

// TypedCol is Typed over the column Col names: TypedCol[int64]("users", "id").
// Declare a column once and reuse the field wherever its values are bound.
func TypedCol[T any](first string, rest ...string) Field[T] {
	return Field[T]{expr: Col(first, rest...)}
}

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
func (f Field[T]) Eq(value T) Condition { return comparison(f.expr, "=", parameter(value)) }

// Ne compares f with a bound value of T using inequality.
func (f Field[T]) Ne(value T) Condition { return comparison(f.expr, "<>", parameter(value)) }

// Lt compares f with a bound value of T using less-than.
func (f Field[T]) Lt(value T) Condition { return comparison(f.expr, "<", parameter(value)) }

// Lte compares f with a bound value of T using less-than-or-equal.
func (f Field[T]) Lte(value T) Condition { return comparison(f.expr, "<=", parameter(value)) }

// Gt compares f with a bound value of T using greater-than.
func (f Field[T]) Gt(value T) Condition { return comparison(f.expr, ">", parameter(value)) }

// Gte compares f with a bound value of T using greater-than-or-equal.
func (f Field[T]) Gte(value T) Condition { return comparison(f.expr, ">=", parameter(value)) }

// EqField compares f with another field of the same Go type.
func (f Field[T]) EqField(other Field[T]) Condition { return f.expr.Eq(other.expr) }

// NeField compares f with another field of the same Go type using inequality.
func (f Field[T]) NeField(other Field[T]) Condition { return f.expr.Ne(other.expr) }

// LtField compares f with another field of the same Go type using less-than.
func (f Field[T]) LtField(other Field[T]) Condition { return f.expr.Lt(other.expr) }

// GtField compares f with another field of the same Go type using greater-than.
func (f Field[T]) GtField(other Field[T]) Condition { return f.expr.Gt(other.expr) }

// IsNull tests whether f is SQL NULL.
func (f Field[T]) IsNull() Condition { return f.expr.IsNull() }

// IsNotNull tests whether f is not SQL NULL.
func (f Field[T]) IsNotNull() Condition { return f.expr.IsNotNull() }

// IsDistinctFrom compares f with a nullable bound value.
func (f Field[T]) IsDistinctFrom(value Null[T]) Condition {
	return f.expr.IsDistinctFrom(ParamNull(value))
}

// IsNotDistinctFrom compares f with a nullable bound value using NULL-safe
// equality.
func (f Field[T]) IsNotDistinctFrom(value Null[T]) Condition {
	return f.expr.IsNotDistinctFrom(ParamNull(value))
}

// In compares f with explicitly expanded values. An empty list is FALSE.
func (f Field[T]) In(values ...T) Condition { return inValues(f.expr, "IN", values) }

// NotIn compares f with explicitly expanded values. An empty list is TRUE.
func (f Field[T]) NotIn(values ...T) Condition { return inValues(f.expr, "NOT IN", values) }

// Between checks whether f lies inclusively between two bound values.
func (f Field[T]) Between(lower, upper T) Condition {
	return f.expr.Between(parameter(lower), parameter(upper))
}

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
func (f Field[T]) LteField(other Field[T]) Condition { return f.expr.Lte(other.expr) }

// GteField compares f with another field of the same Go type using greater-than-or-equal.
func (f Field[T]) GteField(other Field[T]) Condition { return f.expr.Gte(other.expr) }

// NotBetween checks whether f lies outside two inclusive bound values.
func (f Field[T]) NotBetween(lower, upper T) Condition {
	return f.expr.NotBetween(parameter(lower), parameter(upper))
}

// NotInQuery compares f with the one-column result of query using NOT IN.
func (f Field[T]) NotInQuery(query Rowset) Condition { return f.expr.NotInQuery(query) }

// IsDistinctFromField compares f with another field using NULL-safe distinctness.
func (f Field[T]) IsDistinctFromField(other Field[T]) Condition {
	return f.expr.IsDistinctFrom(other.expr)
}

// IsNotDistinctFromField compares f with another field using NULL-safe equality.
func (f Field[T]) IsNotDistinctFromField(other Field[T]) Condition {
	return f.expr.IsNotDistinctFrom(other.expr)
}

// IsDistinctFrom compares two rows using PostgreSQL's NULL-safe distinctness.
func (r RowExpr) IsDistinctFrom(other RowExpr) Condition { return r.compare("IS DISTINCT FROM", other) }
