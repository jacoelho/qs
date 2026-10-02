package qs

// DeleteBuilder constructs a DELETE statement. Clause-list methods append;
// From replaces the target table and Reset clears the builder.
type DeleteBuilder struct {
	aliases      ReturningAliases
	cursor       string
	using        []Relation
	where        []Condition
	returning    []Expr
	table        Relation
	base         statementBase
	hasCursor    bool
	requireWhere bool
}

// DeleteFrom starts a DELETE for a named table.
func DeleteFrom(table string) *DeleteBuilder { return &DeleteBuilder{table: Table(table)} }

// DeleteFromTable starts a DELETE for a relation expression.
func DeleteFromTable(table Relation) *DeleteBuilder { return &DeleteBuilder{table: table} }

// From replaces the DELETE target with a named table.
func (b *DeleteBuilder) From(table string) *DeleteBuilder { b.table = Table(table); return b }

// Using appends named tables to the DELETE USING list.
func (b *DeleteBuilder) Using(tables ...string) *DeleteBuilder {
	for _, t := range tables {
		b.using = append(b.using, Table(t))
	}
	return b
}

// UsingExpr appends relation expressions to the DELETE USING list.
func (b *DeleteBuilder) UsingExpr(relations ...Relation) *DeleteBuilder {
	b.using = append(b.using, relations...)
	return b
}

// Where appends predicates to the DELETE WHERE clause.
func (b *DeleteBuilder) Where(conditions ...Condition) *DeleteBuilder {
	b.where = append(b.where, conditions...)
	return b
}

// WhereIf appends predicates only when include is true.
func (b *DeleteBuilder) WhereIf(include bool, conditions ...Condition) *DeleteBuilder {
	if include {
		b.Where(conditions...)
	}
	return b
}

// WhereCurrentOf selects PostgreSQL's cursor-position DELETE predicate.
func (b *DeleteBuilder) WhereCurrentOf(cursor string) *DeleteBuilder {
	b.cursor = cursor
	b.hasCursor = true
	return b
}

// RequireWhere requires a predicate or CURRENT OF clause when rendering.
func (b *DeleteBuilder) RequireWhere() *DeleteBuilder { b.requireWhere = true; return b }

// Returning appends expressions to the DELETE RETURNING projection.
func (b *DeleteBuilder) Returning(expressions ...Expr) *DeleteBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}

// ReturningCols appends named columns to the DELETE RETURNING projection.
func (b *DeleteBuilder) ReturningCols(columns ...string) *DeleteBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}

// ReturningRows replaces the OLD and NEW row aliases for DELETE RETURNING.
func (b *DeleteBuilder) ReturningRows(aliases ReturningAliases) *DeleteBuilder {
	b.aliases = aliases
	return b
}
func (b *DeleteBuilder) append(w *renderer) {
	w.head(b.base)
	w.text("DELETE FROM ")
	w.target(b.table, false)
	if len(b.using) > 0 {
		w.text(" USING ")
		w.relations(b.using)
	}
	w.mutationWhere(b.where, b.cursor, b.hasCursor, b.requireWhere)
	w.returning(b.returning, b.aliases)
	w.foot(b.base)
}

// Reset clears the DELETE builder, including its target and accumulated clauses.
func (b *DeleteBuilder) Reset() { *b = DeleteBuilder{} }
