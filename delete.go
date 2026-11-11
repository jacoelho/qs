package qs

// PendingDelete is a DELETE that has not yet said which rows it removes. It
// is not a statement: Where, WhereCurrentOf or AllRows completes it. It is an
// immutable value whose clause methods return a copy with their arguments
// appended.
type PendingDelete struct {
	statement DeleteBuilder
}

// DeleteBuilder is a complete DELETE statement: its rows were selected by
// PendingDelete.Where, WhereCurrentOf or AllRows. It is an immutable value.
type DeleteBuilder struct {
	aliases   ReturningAliases
	using     []Relation
	returning []Expr
	rows      rowSelection
	table     Relation
	base      statementBase
}

// DeleteFrom starts a DELETE for a named table.
func DeleteFrom(table string) PendingDelete { return DeleteFromTable(Table(table)) }

// DeleteFromTable starts a DELETE for a relation expression.
func DeleteFromTable(table Relation) PendingDelete {
	return PendingDelete{DeleteBuilder{table: table}}
}

// With adds CTEs before DELETE, in order. Duplicate CTE names are reported
// when the statement is rendered.
func (p PendingDelete) With(ctes ...WithQuery) PendingDelete {
	p.statement = p.statement.With(ctes...)
	return p
}

// WithRecursive is With for CTEs that may refer to themselves.
func (p PendingDelete) WithRecursive(ctes ...WithQuery) PendingDelete {
	p.statement = p.statement.WithRecursive(ctes...)
	return p
}

// Using appends named tables to the DELETE USING list.
func (p PendingDelete) Using(tables ...string) PendingDelete {
	p.statement.using = extendWith(p.statement.using, tables, table)
	return p
}

// UsingExpr appends relation expressions to the DELETE USING list.
func (p PendingDelete) UsingExpr(relations ...Relation) PendingDelete {
	p.statement.using = extend(p.statement.using, relations...)
	return p
}

// Where completes the DELETE with the rows that satisfy every condition.
// Rendering fails when the conditions are TRUE for every row by construction:
// And of an empty list and NotIn of an empty list are TRUE, and a filter with
// no entries must not reach every row. Use AllRows to delete every row. The
// check reads TRUE and FALSE literals through And, Or, Not and parentheses;
// bound values and trusted SQL are not inspected.
func (p PendingDelete) Where(first Condition, rest ...Condition) DeleteBuilder {
	p.statement.rows = rowsWhere(first, rest)
	return p.statement
}

// WhereSlice is Where for a run-time list, which is copied. An empty list
// selects no rows to delete and fails rendering, as conditions that are TRUE
// for every row do.
func (p PendingDelete) WhereSlice(conditions []Condition) DeleteBuilder {
	p.statement.rows = rowSelection{kind: rowsMatching, conditions: cloneSlice(conditions)}
	return p.statement
}

// WhereCurrentOf completes the DELETE with the row a cursor is positioned on.
func (p PendingDelete) WhereCurrentOf(cursor string) DeleteBuilder {
	p.statement.rows = rowSelection{kind: rowsAtCursor, cursor: cursor}
	return p.statement
}

// AllRows completes the DELETE with every row of the table.
func (p PendingDelete) AllRows() DeleteBuilder {
	p.statement.rows = rowSelection{kind: rowsAll}
	return p.statement
}

// Returning appends expressions to the DELETE RETURNING projection.
func (b DeleteBuilder) Returning(expressions ...Expr) DeleteBuilder {
	b.returning = extend(b.returning, expressions...)
	return b
}

// ReturningCols appends named columns to the DELETE RETURNING projection.
func (b DeleteBuilder) ReturningCols(columns ...string) DeleteBuilder {
	b.returning = extendWith(b.returning, columns, unqualifiedColumn)
	return b
}

// ReturningRows replaces the OLD and NEW row aliases for DELETE RETURNING.
func (b DeleteBuilder) ReturningRows(aliases ReturningAliases) DeleteBuilder {
	b.aliases = aliases
	return b
}

func (w *renderer) deleteStatement(b *DeleteBuilder) {
	w.head(b.base)
	if w.err != nil {
		return
	}
	w.text("DELETE FROM ")
	w.target(b.table, false)
	if w.stopped("target", 0) {
		return
	}
	if len(b.using) > 0 {
		w.text(" USING ")
		w.relations(b.using)
		if w.stopped("USING", 0) {
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
