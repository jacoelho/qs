package qs

const (
	sqlDefault = "DEFAULT"
	sqlRow     = "ROW"
)

// WriteValue is a value that can occur directly in a destination write.
// Its expression cannot be used as a general expression or bound as a driver
// value. Use Value to bind application data, Write for an ordinary expression,
// and Default for SQL DEFAULT.
type WriteValue struct{ write Expr }

// WriteRowValue is a row-valued destination write. It preserves whether the
// row was written with ROW(...) or parenthesized tuple syntax.
type WriteRowValue struct{ writeRow RowExpr }

// Value binds an application value in a destination write. It accepts nil,
// retains application values shallowly, and does not encode them.
// Expressions, statements and write descriptors are rejected as parameters;
// use Write for SQL expressions and Default for SQL DEFAULT.
func Value(value any) WriteValue { return Write(parameter(value)) }

// Write wraps an ordinary expression for a direct destination position.
func Write(expr Expr) WriteValue { return WriteValue{write: expr} }

// Default requests the destination column's SQL DEFAULT.
func Default() WriteValue {
	return WriteValue{write: Expr{kind: exprKeyword, text: sqlDefault}}
}

// WriteRow constructs a ROW(...) destination value. At least one value is
// required by PostgreSQL's row grammar.
func WriteRow(first WriteValue, rest ...WriteValue) WriteRowValue {
	exprs := make([]Expr, 1+len(rest))
	exprs[0] = first.write
	for i, value := range rest {
		exprs[i+1] = value.write
	}
	return writeRowValue(sqlRow, exprs)
}

// WriteTuple constructs a parenthesized tuple destination value. A tuple
// requires at least two values because one parenthesized expression is scalar.
func WriteTuple(first, second WriteValue, rest ...WriteValue) WriteRowValue {
	exprs := make([]Expr, 2+len(rest))
	exprs[0], exprs[1] = first.write, second.write
	for i, value := range rest {
		exprs[i+2] = value.write
	}
	return writeRowValue("", exprs)
}

// WriteRowExpr wraps an existing ordinary row expression for a direct row
// destination position. Row expressions are immutable values owned by their
// constructors, so the existing element buffer can be retained.
func WriteRowExpr(row RowExpr) WriteRowValue {
	return WriteRowValue{writeRow: row}
}

func writeRowValue(format string, exprs []Expr) WriteRowValue {
	return WriteRowValue{writeRow: RowExpr{
		expr:  Expr{kind: exprList, text: format, value: exprs},
		width: projectionWidth(exprs),
	}}
}

func (v WriteValue) expr() Expr          { return v.write }
func (v WriteRowValue) rowExpr() RowExpr { return v.writeRow }

func ownedWriteValues(first WriteValue, rest []WriteValue) []WriteValue {
	values := make([]WriteValue, 1+len(rest))
	values[0] = first
	copy(values[1:], rest)
	return values
}

// writeExpr renders an expression at a direct destination-write position.
// DEFAULT is represented as a write descriptor rather than a general
// expression, so it is emitted here while nested expressions continue through
// the ordinary expression renderer.
func (w *renderer) writeExpr(e Expr) {
	if e.kind == exprKeyword && e.text == sqlDefault {
		w.text(sqlDefault)
	} else {
		w.expr(e)
	}
}

func (w *renderer) writeRow(e Expr) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	if !w.require(e.kind == exprList && (e.text == sqlRow || e.text == ""), "SET ROW", "requires a row value or subquery") {
		return
	}
	if e.text == sqlRow {
		w.text(sqlRow + "(")
	} else {
		w.byte('(')
	}
	for i, value := range ownedPayload[[]Expr](e.value) {
		if i != 0 {
			w.text(", ")
		}
		w.writeExpr(value)
	}
	w.byte(')')
}
