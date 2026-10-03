package qs

import (
	"fmt"
	"slices"
)

// SetBuilder combines two Rowsets and always preserves branch parentheses.
// Limit and OrderBy on a branch apply to that branch; those on this builder
// apply to the combined result.
type SetBuilder struct {
	left     Rowset
	right    Rowset
	tail     queryTail
	base     statementBase
	operator setOperator
}

type setOperator uint8

const (
	setUnion setOperator = iota + 1
	setUnionAll
	setIntersect
	setIntersectAll
	setExcept
	setExceptAll
)

func setOperation(operator setOperator, left, right Rowset, rest []Rowset) *SetBuilder {
	b := &SetBuilder{operator: operator, left: left, right: right}
	for _, next := range rest {
		b = &SetBuilder{operator: operator, left: b, right: next}
	}
	return b
}

// Union combines two or more rowsets with UNION, preserving each branch's
// parentheses and appending any additional rowsets in order.
func Union(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setUnion, left, right, rest)
}

// UnionAll combines two or more rowsets with UNION ALL.
func UnionAll(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setUnionAll, left, right, rest)
}

// Intersect combines two or more rowsets with INTERSECT.
func Intersect(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setIntersect, left, right, rest)
}

// IntersectAll combines two or more rowsets with INTERSECT ALL.
func IntersectAll(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setIntersectAll, left, right, rest)
}

// Except combines two or more rowsets with EXCEPT.
func Except(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setExcept, left, right, rest)
}

// ExceptAll combines two or more rowsets with EXCEPT ALL.
func ExceptAll(left, right Rowset, rest ...Rowset) *SetBuilder {
	return setOperation(setExceptAll, left, right, rest)
}
func (b *SetBuilder) append(w *renderer) {
	left, right := statementWidth(b.left, w.options.MaxDepth), statementWidth(b.right, w.options.MaxDepth)
	if left >= 0 && right >= 0 && !w.require(left == right, "set operation", "projection widths differ") {
		return
	}
	var operator string
	switch b.operator {
	case setUnion:
		operator = "UNION"
	case setUnionAll:
		operator = "UNION ALL"
	case setIntersect:
		operator = "INTERSECT"
	case setIntersectAll:
		operator = "INTERSECT ALL"
	case setExcept:
		operator = "EXCEPT"
	case setExceptAll:
		operator = "EXCEPT ALL"
	default:
		w.fail(ErrInvalid, "set operation", "unknown operator")
		return
	}
	w.head(b.base)
	w.byte('(')
	w.statement(b.left)
	w.text(") ")
	w.text(operator)
	w.text(" (")
	w.statement(b.right)
	w.byte(')')
	w.tail(b.tail, false)
	w.foot(b.base)
}

// ValuesBuilder is a standalone VALUES rowset. Row binds values and RowExpr
// takes explicit expressions. Every row must have the same non-zero width.
type ValuesBuilder struct {
	base statementBase
	rows [][]Expr
	tail queryTail
}

// Values starts a VALUES rowset with one bound-parameter row.
func Values(values ...any) *ValuesBuilder {
	b := &ValuesBuilder{}
	if len(values) > 0 {
		b.Row(values...)
	}
	return b
}

// ValuesExpr starts a VALUES rowset with one explicit-expression row.
func ValuesExpr(expressions ...Expr) *ValuesBuilder {
	b := &ValuesBuilder{}
	if len(expressions) > 0 {
		b.RowExpr(expressions...)
	}
	return b
}

// Row appends a row whose values become bound parameters.
func (b *ValuesBuilder) Row(values ...any) *ValuesBuilder {
	exprs := make([]Expr, len(values))
	for i, v := range values {
		exprs[i] = parameter(v)
	}
	b.rows = append(b.rows, exprs)
	return b
}

// RowExpr appends a row of explicit expressions.
func (b *ValuesBuilder) RowExpr(expressions ...Expr) *ValuesBuilder {
	b.rows = append(b.rows, cloneSlice(expressions))
	return b
}
func (b *ValuesBuilder) append(w *renderer) {
	w.head(b.base)
	w.values(b.rows, false)
	w.tail(b.tail, false)
	w.foot(b.base)
}
func (w *renderer) values(rows [][]Expr, allowDefault bool) {
	if !w.require(len(rows) > 0, "VALUES", "requires at least one row") {
		return
	}
	width := -1
	w.text("VALUES ")
	for i, row := range rows {
		rowWidth := projectionWidth(row)
		if !w.rowWidth("VALUES", i+1, rowWidth, width) {
			return
		}
		if rowWidth >= 0 {
			width = rowWidth
		}
		if !allowDefault {
			for _, e := range row {
				if e.kind == exprKeyword && e.text == sqlDefault {
					w.fail(ErrInvalid, "VALUES", "DEFAULT is only allowed directly in INSERT")
					return
				}
			}
		}
		if i != 0 {
			w.text(", ")
		}
		w.byte('(')
		w.exprs(row, ", ")
		w.byte(')')
	}
}

// rowWidth retains unknown projections while reporting only structural counts.
func (w *renderer) rowWidth(clause string, row, actual, expected int) bool {
	if actual == 0 {
		w.fail(ErrInvalid, clause, fmt.Sprintf("row %d: expected at least 1 value, got 0", row))
		return false
	}
	if actual >= 0 && expected >= 0 && actual != expected {
		noun := func(n int) string {
			if n == 1 {
				return "value"
			}
			return "values"
		}
		w.fail(ErrInvalid, clause, fmt.Sprintf("row %d: expected %d %s, got %d %s", row, expected, noun(expected), actual, noun(actual)))
		return false
	}
	return true
}

func writeProjectionWidth(values []WriteValue) int {
	if len(values) == 0 {
		return 0
	}
	for _, value := range values {
		if unknownProjection(value.expr()) {
			return -1
		}
	}
	return len(values)
}

func (w *renderer) writeValues(rows [][]WriteValue) {
	if !w.require(len(rows) > 0, "INSERT", "requires at least one row") {
		return
	}
	width := -1
	w.text("VALUES ")
	for i, row := range rows {
		rowWidth := writeProjectionWidth(row)
		if !w.rowWidth("INSERT", i+1, rowWidth, width) {
			return
		}
		if rowWidth >= 0 {
			width = rowWidth
		}
		if i != 0 {
			w.text(", ")
		}
		w.byte('(')
		for j, value := range row {
			if j != 0 {
				w.text(", ")
			}
			w.writeExpr(value.expr())
		}
		w.byte(')')
	}
}

// TableBuilder renders PostgreSQL's TABLE shorthand as a composable Rowset.
type TableBuilder struct {
	base  statementBase
	table Relation
	tail  queryTail
}

// TableRows starts a TABLE rowset for a named table.
func TableRows(name string) *TableBuilder { return &TableBuilder{table: Table(name)} }

// Only restricts TABLE to the named table, excluding inherited tables.
func (b *TableBuilder) Only() *TableBuilder { b.table = b.table.Only(); return b }
func (b *TableBuilder) append(w *renderer) {
	w.head(b.base)
	w.text("TABLE ")
	w.target(b.table, false)
	w.tail(b.tail, true)
	w.foot(b.base)
}

func projectionWidth(exprs []Expr) int {
	if len(exprs) == 0 {
		return 0
	}
	if slices.ContainsFunc(exprs, unknownProjection) {
		return -1
	}
	return len(exprs)
}
func unknownProjection(e Expr) bool {
	for {
		if e.kind == exprAlias {
			e = e.node.left
			continue
		}
		if e.kind == exprGroup {
			items := ownedPayload[[]Expr](e.value)
			if len(items) == 1 {
				e = items[0]
				continue
			}
		}
		break
	}
	if e.kind == exprRaw || e.kind == exprFragment || e.kind == exprFields {
		return true
	}
	if e.kind == exprIdentifier && (e.text == "*" || len(e.text) > 2 && e.text[len(e.text)-2:] == ".*") {
		return true
	}
	return false
}

// statementWidth returns a known output-column count or -1 when the statement
// is opaque or its graph exceeds the caller's rendering depth. Set operations
// inherit the left branch's width; their renderer validates both branches.
// Walking the set spine iteratively keeps width inspection bounded even when a
// builder graph contains a cycle.
func statementWidth(s Statement, maxDepth int) int {
	for range maxDepth {
		switch b := s.(type) {
		case *SelectBuilder:
			if b != nil {
				return projectionWidth(b.columns)
			}
		case *SetBuilder:
			if b != nil {
				s = b.left
				continue
			}
		case *ValuesBuilder:
			if b != nil && len(b.rows) > 0 {
				return projectionWidth(b.rows[0])
			}
		case *InsertRows:
			if b != nil {
				return projectionWidth(b.base.returning)
			}
		case *InsertSelect:
			if b != nil {
				return projectionWidth(b.base.returning)
			}
		case *InsertAssignments:
			if b != nil {
				return projectionWidth(b.base.returning)
			}
		case *InsertDefaults:
			if b != nil {
				return projectionWidth(b.base.returning)
			}
		case *UpdateBuilder:
			if b != nil {
				return projectionWidth(b.returning)
			}
		case *DeleteBuilder:
			if b != nil {
				return projectionWidth(b.returning)
			}
		case *MergeBuilder:
			if b != nil {
				return projectionWidth(b.returning)
			}
		}
		return -1
	}
	return -1
}
