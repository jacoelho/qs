package qx

type DeleteBuilder struct {
	base         statementBase
	table        Relation
	using        []Relation
	where        []Condition
	cursor       string
	hasCursor    bool
	requireWhere bool
	returning    []Expr
	aliases      ReturningAliases
}

func DeleteFrom(table string) *DeleteBuilder              { return &DeleteBuilder{table: Table(table)} }
func DeleteFromTable(table Relation) *DeleteBuilder       { return &DeleteBuilder{table: table} }
func (b *DeleteBuilder) From(table string) *DeleteBuilder { b.table = Table(table); return b }
func (b *DeleteBuilder) Using(tables ...string) *DeleteBuilder {
	for _, t := range tables {
		b.using = append(b.using, Table(t))
	}
	return b
}
func (b *DeleteBuilder) UsingExpr(relations ...Relation) *DeleteBuilder {
	b.using = append(b.using, relations...)
	return b
}
func (b *DeleteBuilder) Where(conditions ...Condition) *DeleteBuilder {
	b.where = append(b.where, conditions...)
	return b
}
func (b *DeleteBuilder) WhereIf(include bool, conditions ...Condition) *DeleteBuilder {
	if include {
		b.Where(conditions...)
	}
	return b
}
func (b *DeleteBuilder) WhereCurrentOf(cursor string) *DeleteBuilder {
	b.cursor = cursor
	b.hasCursor = true
	return b
}
func (b *DeleteBuilder) RequireWhere() *DeleteBuilder { b.requireWhere = true; return b }
func (b *DeleteBuilder) Returning(expressions ...Expr) *DeleteBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}
func (b *DeleteBuilder) ReturningCols(columns ...string) *DeleteBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}
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
func (b *DeleteBuilder) Reset() { *b = DeleteBuilder{} }
