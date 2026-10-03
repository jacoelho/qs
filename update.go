package qs

// UpdateBuilder constructs an UPDATE statement. Clause-list methods append;
// Table replaces the target relation and Reset clears the builder.
type UpdateBuilder struct {
	aliases      ReturningAliases
	cursor       string
	set          []Assignment
	from         []Relation
	where        []Condition
	returning    []Expr
	table        Relation
	base         statementBase
	hasCursor    bool
	requireWhere bool
}

// Update starts an UPDATE for a named table.
func Update(table string) *UpdateBuilder { return &UpdateBuilder{table: Table(table)} }

// UpdateTable starts an UPDATE for a relation expression.
func UpdateTable(table Relation) *UpdateBuilder { return &UpdateBuilder{table: table} }

// Table replaces the UPDATE target with a named table.
func (b *UpdateBuilder) Table(table string) *UpdateBuilder { b.table = Table(table); return b }

// Set appends assignments to the UPDATE SET clause.
func (b *UpdateBuilder) Set(assignments ...Assignment) *UpdateBuilder {
	b.set = append(b.set, assignments...)
	return b
}

// From appends named tables to the UPDATE FROM clause.
func (b *UpdateBuilder) From(tables ...string) *UpdateBuilder {
	for _, t := range tables {
		b.from = append(b.from, Table(t))
	}
	return b
}

// FromExpr appends relation expressions to the UPDATE FROM clause.
func (b *UpdateBuilder) FromExpr(relations ...Relation) *UpdateBuilder {
	b.from = append(b.from, relations...)
	return b
}

// Where appends predicates to the UPDATE WHERE clause.
func (b *UpdateBuilder) Where(conditions ...Condition) *UpdateBuilder {
	b.where = append(b.where, conditions...)
	return b
}

// WhereIf appends predicates only when include is true.
func (b *UpdateBuilder) WhereIf(include bool, conditions ...Condition) *UpdateBuilder {
	if include {
		b.Where(conditions...)
	}
	return b
}

// WhereCurrentOf selects PostgreSQL's cursor-position UPDATE predicate.
func (b *UpdateBuilder) WhereCurrentOf(cursor string) *UpdateBuilder {
	b.cursor = cursor
	b.hasCursor = true
	return b
}

// RequireWhere checks for a syntactic WHERE, not authorisation or selectivity.
func (b *UpdateBuilder) RequireWhere() *UpdateBuilder { b.requireWhere = true; return b }

// Returning appends expressions to the UPDATE RETURNING projection.
func (b *UpdateBuilder) Returning(expressions ...Expr) *UpdateBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}

// ReturningCols appends named columns to the UPDATE RETURNING projection.
func (b *UpdateBuilder) ReturningCols(columns ...string) *UpdateBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}

// ReturningRows replaces the OLD and NEW row aliases for UPDATE RETURNING.
func (b *UpdateBuilder) ReturningRows(aliases ReturningAliases) *UpdateBuilder {
	b.aliases = aliases
	return b
}
func (b *UpdateBuilder) append(w *renderer) {
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
	w.mutationWhere(b.where, b.cursor, b.hasCursor, b.requireWhere)
	if w.stopped("WHERE", 0) {
		return
	}
	w.returning(b.returning, b.aliases)
	if w.stopped("RETURNING", 0) {
		return
	}
	w.foot(b.base)
}
func (w *renderer) mutationWhere(conditions []Condition, cursor string, hasCursor, required bool) {
	if !w.require(!hasCursor || len(conditions) == 0, "WHERE", "CURRENT OF cannot be combined with a predicate") {
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

// Reset clears the UPDATE builder, including its target and accumulated clauses.
func (b *UpdateBuilder) Reset() { *b = UpdateBuilder{} }
