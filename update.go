package qs

type UpdateBuilder struct {
	base         statementBase
	table        Relation
	set          []Assignment
	from         []Relation
	where        []Condition
	cursor       string
	hasCursor    bool
	requireWhere bool
	returning    []Expr
	aliases      ReturningAliases
}

func Update(table string) *UpdateBuilder                   { return &UpdateBuilder{table: Table(table)} }
func UpdateTable(table Relation) *UpdateBuilder            { return &UpdateBuilder{table: table} }
func (b *UpdateBuilder) Table(table string) *UpdateBuilder { b.table = Table(table); return b }
func (b *UpdateBuilder) Set(assignments ...Assignment) *UpdateBuilder {
	b.set = append(b.set, assignments...)
	return b
}
func (b *UpdateBuilder) From(tables ...string) *UpdateBuilder {
	for _, t := range tables {
		b.from = append(b.from, Table(t))
	}
	return b
}
func (b *UpdateBuilder) FromExpr(relations ...Relation) *UpdateBuilder {
	b.from = append(b.from, relations...)
	return b
}
func (b *UpdateBuilder) Where(conditions ...Condition) *UpdateBuilder {
	b.where = append(b.where, conditions...)
	return b
}
func (b *UpdateBuilder) WhereIf(include bool, conditions ...Condition) *UpdateBuilder {
	if include {
		b.Where(conditions...)
	}
	return b
}
func (b *UpdateBuilder) WhereCurrentOf(cursor string) *UpdateBuilder {
	b.cursor = cursor
	b.hasCursor = true
	return b
}

// RequireWhere checks for a syntactic WHERE, not authorisation or selectivity.
func (b *UpdateBuilder) RequireWhere() *UpdateBuilder { b.requireWhere = true; return b }
func (b *UpdateBuilder) Returning(expressions ...Expr) *UpdateBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}
func (b *UpdateBuilder) ReturningCols(columns ...string) *UpdateBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}
func (b *UpdateBuilder) ReturningRows(aliases ReturningAliases) *UpdateBuilder {
	b.aliases = aliases
	return b
}
func (b *UpdateBuilder) append(w *renderer) {
	if !w.require(len(b.set) > 0, "UPDATE", "requires assignments") {
		return
	}
	w.head(b.base)
	w.text("UPDATE ")
	w.target(b.table, false)
	w.text(" SET ")
	w.assignments(b.set)
	if len(b.from) > 0 {
		w.text(" FROM ")
		w.relations(b.from)
	}
	w.mutationWhere(b.where, b.cursor, b.hasCursor, b.requireWhere)
	w.returning(b.returning, b.aliases)
	w.foot(b.base)
}
func (w *renderer) mutationWhere(conditions []Condition, cursor string, hasCursor, required bool) {
	if !w.require(!(hasCursor && len(conditions) > 0), "WHERE", "CURRENT OF cannot be combined with a predicate") {
		return
	}
	if !w.require(!required || hasCursor || len(conditions) > 0, "WHERE", "mutation requires an explicit WHERE") {
		return
	}
	if hasCursor {
		w.text(" WHERE CURRENT OF ")
		w.identifierPart(cursor)
	} else if len(conditions) > 0 {
		w.text(" WHERE ")
		w.conditions(conditions)
	}
}
func (b *UpdateBuilder) Reset() { *b = UpdateBuilder{} }
