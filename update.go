package qs

// PendingUpdate is an UPDATE that has not yet said which rows it changes. It
// is not a statement: Where, WhereCurrentOf or AllRows completes it. It is an
// immutable value whose clause methods return a copy with their arguments
// appended.
type PendingUpdate struct {
	statement UpdateBuilder
}

// UpdateBuilder is a complete UPDATE statement: its rows were selected by
// PendingUpdate.Where, WhereCurrentOf or AllRows. It is an immutable value.
type UpdateBuilder struct {
	aliases   ReturningAliases
	set       []Assignment
	from      []Relation
	returning []Expr
	rows      rowSelection
	table     Relation
	base      statementBase
}

// Update starts an UPDATE for a named table.
func Update(table string) PendingUpdate { return UpdateTable(Table(table)) }

// UpdateTable starts an UPDATE for a relation expression.
func UpdateTable(table Relation) PendingUpdate {
	return PendingUpdate{UpdateBuilder{table: table}}
}

// With adds CTEs before UPDATE, in order. Duplicate CTE names are reported
// when the statement is rendered.
func (p PendingUpdate) With(ctes ...WithQuery) PendingUpdate {
	p.statement = p.statement.With(ctes...)
	return p
}

// WithRecursive is With for CTEs that may refer to themselves.
func (p PendingUpdate) WithRecursive(ctes ...WithQuery) PendingUpdate {
	p.statement = p.statement.WithRecursive(ctes...)
	return p
}

// Set appends assignments to the UPDATE SET clause. A statement with none
// fails rendering.
func (p PendingUpdate) Set(assignments ...Assignment) PendingUpdate {
	p.statement.set = extend(p.statement.set, assignments...)
	return p
}

// From appends named tables to the UPDATE FROM clause.
func (p PendingUpdate) From(tables ...string) PendingUpdate {
	p.statement.from = extendWith(p.statement.from, tables, table)
	return p
}

// FromExpr appends relation expressions to the UPDATE FROM clause.
func (p PendingUpdate) FromExpr(relations ...Relation) PendingUpdate {
	p.statement.from = extend(p.statement.from, relations...)
	return p
}

// Where completes the UPDATE with the rows that satisfy every condition.
// Rendering fails when the conditions are TRUE for every row by construction:
// And of an empty list and NotIn of an empty list are TRUE, and a filter with
// no entries must not reach every row. Use AllRows to update every row. The
// check reads TRUE and FALSE literals through And, Or, Not and parentheses;
// bound values and trusted SQL are not inspected.
func (p PendingUpdate) Where(first Condition, rest ...Condition) UpdateBuilder {
	p.statement.rows = rowsWhere(first, rest)
	return p.statement
}

// WhereCurrentOf completes the UPDATE with the row a cursor is positioned on.
func (p PendingUpdate) WhereCurrentOf(cursor string) UpdateBuilder {
	p.statement.rows = rowSelection{kind: rowsAtCursor, cursor: cursor}
	return p.statement
}

// AllRows completes the UPDATE with every row of the table.
func (p PendingUpdate) AllRows() UpdateBuilder {
	p.statement.rows = rowSelection{kind: rowsAll}
	return p.statement
}

// Returning appends expressions to the UPDATE RETURNING projection.
func (b UpdateBuilder) Returning(expressions ...Expr) UpdateBuilder {
	b.returning = extend(b.returning, expressions...)
	return b
}

// ReturningCols appends named columns to the UPDATE RETURNING projection.
func (b UpdateBuilder) ReturningCols(columns ...string) UpdateBuilder {
	b.returning = extendWith(b.returning, columns, unqualifiedColumn)
	return b
}

// ReturningRows replaces the OLD and NEW row aliases for UPDATE RETURNING.
func (b UpdateBuilder) ReturningRows(aliases ReturningAliases) UpdateBuilder {
	b.aliases = aliases
	return b
}

func (w *renderer) updateStatement(b *UpdateBuilder) {
	if !w.require(len(b.set) > 0, "UPDATE", "requires assignments") {
		return
	}
	w.head(b.base)
	if w.err != nil {
		return
	}
	w.text("UPDATE ")
	w.target(b.table, false)
	if w.stopped("target", 0) {
		return
	}
	w.text(" SET ")
	w.assignments(b.set)
	if w.stopped("SET", 0) {
		return
	}
	if len(b.from) > 0 {
		w.text(" FROM ")
		w.relations(b.from)
		if w.stopped("FROM", 0) {
			return
		}
	}
	w.mutationWhere(&b.rows)
	if w.stopped("WHERE", 0) {
		return
	}
	w.returning(b.returning, b.aliases)
	if w.stopped("RETURNING", 0) {
		return
	}
	w.foot(b.base)
}

// rowSelection is which rows an UPDATE or DELETE changes. PendingUpdate and
// PendingDelete are its only writers and set exactly one kind, so a complete
// statement cannot hold a predicate together with a cursor or with all rows.
type rowSelection struct {
	cursor     string
	conditions []Condition
	kind       rowSelectionKind
}

type rowSelectionKind uint8

const (
	rowsUnselected rowSelectionKind = iota // the zero statement, which is invalid
	rowsMatching                           // conditions
	rowsAtCursor                           // WHERE CURRENT OF cursor
	rowsAll                                // no WHERE
)

func rowsWhere(first Condition, rest []Condition) rowSelection {
	return rowSelection{kind: rowsMatching, conditions: ownedList(first, rest)}
}

// mutationWhere renders the row selection of UPDATE and DELETE.
func (w *renderer) mutationWhere(rows *rowSelection) {
	switch rows.kind {
	case rowsMatching:
		// A predicate that is TRUE whatever the row is not a selection: And
		// and NotIn return TRUE for an empty list, so accepting it would let
		// a dynamic filter with no entries reach every row.
		selective := false
		for _, c := range rows.conditions {
			if rowIndependent(c.expr, w.options.MaxDepth) != alwaysTrue {
				selective = true
				break
			}
		}
		if !w.require(selective, "WHERE", "mutation requires a condition that is not always TRUE; use AllRows to change every row") {
			return
		}
		w.text(" WHERE ")
		w.conditions(rows.conditions)
	case rowsAtCursor:
		w.text(" WHERE CURRENT OF ")
		w.identifierPart(rows.cursor)
	case rowsAll:
	default:
		w.fail(ErrInvalid, "WHERE", "zero statement selects no rows; build it from Update or DeleteFrom")
	}
}

// constantTruth is the value of a condition that is the same for every row.
type constantTruth uint8

const (
	variesByRow constantTruth = iota
	alwaysTrue
	alwaysFalse
)

// rowIndependent folds TRUE and FALSE literals through AND, OR, NOT and
// parentheses. Every other expression varies by row as far as qs can tell:
// bound values and trusted SQL are not inspected. A condition nested deeper
// than depth also reports variesByRow; rendering then rejects its depth.
func rowIndependent(e Expr, depth int) constantTruth {
	if e.kind == exprLiteral {
		switch e.text {
		case sqlTrue:
			return alwaysTrue
		case sqlFalse:
			return alwaysFalse
		}
		return variesByRow
	}
	if depth <= 0 {
		return variesByRow
	}
	switch n := e.node().(type) {
	case *prefixExpression:
		if n.operator != sqlNot {
			return variesByRow
		}
		switch rowIndependent(n.operand, depth-1) {
		case alwaysTrue:
			return alwaysFalse
		case alwaysFalse:
			return alwaysTrue
		case variesByRow:
		}
	case *groupExpression:
		switch {
		case len(n.items) == 1:
			return rowIndependent(n.items[0], depth-1)
		case n.separator == sqlAnd:
			return junctionTruth(n.items, alwaysFalse, alwaysTrue, depth-1)
		case n.separator == sqlOr:
			return junctionTruth(n.items, alwaysTrue, alwaysFalse, depth-1)
		}
	}
	return variesByRow
}

// junctionTruth is absorbing when any operand is, identity when every operand
// is, and row-dependent otherwise. FALSE absorbs AND; TRUE absorbs OR.
func junctionTruth(items []Expr, absorbing, identity constantTruth, depth int) constantTruth {
	result := identity
	for _, item := range items {
		truth := rowIndependent(item, depth)
		if truth == absorbing {
			return absorbing
		}
		if truth == variesByRow {
			result = variesByRow
		}
	}
	return result
}
