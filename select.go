package qs

type selectQuantifier uint8

const (
	selectAll selectQuantifier = iota
	selectDistinct
	selectDistinctOn
)

// SelectBuilder is an immutable SELECT. Every method returns a new value and
// leaves its receiver unchanged, so a query can be branched by assignment:
// keep the result, as in q = q.Where(...). List methods append; From and
// FromExpr replace the FROM list. Limit/Offset replace their previous values.
// The zero value has no projection and fails rendering until Columns is called.
type SelectBuilder struct {
	columns       []Expr
	from          []Relation
	where         []Condition
	group         []Expr
	having        []Condition
	windows       []namedWindow
	distinctOn    []Expr
	cursor        []Expr // SeekAfter key values; empty means no cursor.
	tail          queryTail
	base          statementBase
	noColumns     bool
	groupDistinct bool
	distinct      selectQuantifier
}

// Select starts a query with at least one expression, in projection order.
// Use Col for identifiers, Param for bound values, and UnsafeSQL for an
// existing trusted SQL projection list. SelectCols and SelectSQL are
// convenience wrappers; SelectSlice takes a projection held in a slice and
// SelectNoColumns states that none is wanted.
func Select(first Expr, rest ...Expr) SelectBuilder {
	return SelectBuilder{columns: ownedList(first, rest)}
}

// SelectSlice is Select for a projection held in a slice, which is copied. The
// length of a slice is not known until the program runs, so an empty one is
// an incomplete projection and fails rendering.
func SelectSlice(columns []Expr) SelectBuilder {
	return SelectBuilder{columns: cloneSlice(columns)}
}

// SelectNoColumns intentionally returns rows with no columns.
func SelectNoColumns() SelectBuilder { return SelectBuilder{noColumns: true} }

// SelectCols is Select with each string naming one column, quoted as written.
// For qualified names, casts and aliases, use Select with expressions.
func SelectCols(first string, rest ...string) SelectBuilder {
	return SelectBuilder{columns: convertedList(first, rest, unqualifiedColumn)}
}

// SelectSQL is a convenience wrapper for Select with each argument wrapped in
// UnsafeSQL. It accepts trusted projection lists, not identifiers or user data.
// Column counts for these fragments are unknown; qs does not pretend that
// counting commas parses SQL. Prefer Select with expressions for new queries.
func SelectSQL(first string, rest ...string) SelectBuilder {
	return SelectBuilder{columns: convertedList(first, rest, UnsafeSQL)}
}

// Columns appends expressions in projection order.
func (b SelectBuilder) Columns(columns ...Expr) SelectBuilder {
	b.columns = extend(b.columns, columns...)
	return b
}

// ColumnNames appends columns, each named by one identifier, in projection order.
func (b SelectBuilder) ColumnNames(columns ...string) SelectBuilder {
	b.columns = extendWith(b.columns, columns, unqualifiedColumn)
	return b
}

// ColumnsSQL appends trusted SQL projection fragments.
func (b SelectBuilder) ColumnsSQL(projections ...string) SelectBuilder {
	b.columns = extendWith(b.columns, projections, UnsafeSQL)
	return b
}

// From replaces the table list. An empty call removes FROM.
func (b SelectBuilder) From(tables ...string) SelectBuilder {
	b.from = extendWith(nil, tables, table)
	return b
}

// FromExpr replaces the relation list. An empty call removes FROM.
func (b SelectBuilder) FromExpr(relations ...Relation) SelectBuilder {
	b.from = cloneSlice(relations)
	return b
}

// Where appends predicates to the SELECT WHERE clause.
func (b SelectBuilder) Where(conditions ...Condition) SelectBuilder {
	b.where = extend(b.where, conditions...)
	return b
}

// WhereIf appends predicates only when include is true.
func (b SelectBuilder) WhereIf(include bool, conditions ...Condition) SelectBuilder {
	if include {
		return b.Where(conditions...)
	}
	return b
}

// Distinct removes duplicate result rows and replaces any DISTINCT ON expressions.
func (b SelectBuilder) Distinct() SelectBuilder {
	b.distinct = selectDistinct
	b.distinctOn = nil
	return b
}

// All preserves duplicate rows and clears both DISTINCT and DISTINCT ON.
func (b SelectBuilder) All() SelectBuilder {
	b.distinct = selectAll
	b.distinctOn = nil
	return b
}

// DistinctOn replaces the quantifier with DISTINCT ON and stores its expressions.
func (b SelectBuilder) DistinctOn(expressions ...Expr) SelectBuilder {
	b.distinct = selectDistinctOn
	b.distinctOn = cloneSlice(expressions)
	return b
}

// GroupBy appends columns, each named by one identifier, to the grouping list.
func (b SelectBuilder) GroupBy(columns ...string) SelectBuilder {
	b.group = extendWith(b.group, columns, unqualifiedColumn)
	return b
}

// GroupByExpr appends expressions to the grouping list.
func (b SelectBuilder) GroupByExpr(expressions ...Expr) SelectBuilder {
	b.group = extend(b.group, expressions...)
	return b
}

// GroupByDistinct requests PostgreSQL's GROUP BY DISTINCT form.
func (b SelectBuilder) GroupByDistinct() SelectBuilder { b.groupDistinct = true; return b }

// Having appends predicates to the SELECT HAVING clause.
func (b SelectBuilder) Having(conditions ...Condition) SelectBuilder {
	b.having = extend(b.having, conditions...)
	return b
}

// Window appends a named window definition.
func (b SelectBuilder) Window(name string, spec WindowSpec) SelectBuilder {
	b.windows = extend(b.windows, namedWindow{name, spec})
	return b
}

// joinLast replaces the last FROM item with a join onto it, matching
// PostgreSQL's binding of explicit JOIN more tightly than comma-separated FROM
// items. For nested joins, build a Relation with InnerJoin/LeftJoin/etc. and
// pass it to FromExpr. The FROM list is copied: the receiver's is shared.
func (b SelectBuilder) joinLast(join func(left Relation) Relation) SelectBuilder {
	if len(b.from) == 0 {
		b.from = []Relation{RelationSQL(invalidExpr("JOIN", "requires a FROM item"))}
		return b
	}
	from := cloneSlice(b.from)
	from[len(from)-1] = join(from[len(from)-1])
	b.from = from
	return b
}

func (b SelectBuilder) appendJoin(right Relation, kind joinKind, first Condition, rest ...Condition) SelectBuilder {
	return b.joinLast(func(left Relation) Relation { return pendingJoin(left, right, kind).On(first, rest...) })
}

func (b SelectBuilder) appendCrossJoin(right Relation) SelectBuilder {
	return b.joinLast(func(left Relation) Relation { return CrossJoin(left, right) })
}

// Join attaches to the last FROM item; missing FROM remains a rendering error.
func (b SelectBuilder) Join(table string, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(Table(table), joinInner, first, rest...)
}

// LeftJoin attaches to the last FROM item, preserving comma binding.
func (b SelectBuilder) LeftJoin(table string, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(Table(table), joinLeft, first, rest...)
}

// RightJoin attaches to the last FROM item, preserving comma binding.
func (b SelectBuilder) RightJoin(table string, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(Table(table), joinRight, first, rest...)
}

// FullJoin attaches to the last FROM item, preserving comma binding.
func (b SelectBuilder) FullJoin(table string, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(Table(table), joinFull, first, rest...)
}

// CrossJoin attaches a named table with a CROSS JOIN to the last FROM item.
func (b SelectBuilder) CrossJoin(table string) SelectBuilder {
	return b.appendCrossJoin(Table(table))
}

// JoinExpr accepts a completed relation and attaches to the last FROM item.
func (b SelectBuilder) JoinExpr(relation Relation, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(relation, joinInner, first, rest...)
}

// LeftJoinExpr accepts a completed relation and attaches to the last FROM item.
func (b SelectBuilder) LeftJoinExpr(relation Relation, first Condition, rest ...Condition) SelectBuilder {
	return b.appendJoin(relation, joinLeft, first, rest...)
}

// CrossJoinExpr attaches a completed relation with a CROSS JOIN.
func (b SelectBuilder) CrossJoinExpr(relation Relation) SelectBuilder {
	return b.appendCrossJoin(relation)
}

func (w *renderer) selectStatement(b *SelectBuilder) {
	w.selectRows(b, b.cursor, nil)
	if w.err != nil {
		return
	}
	// An unused WINDOW definition does not prevent row locking. The tail checks
	// structural OVER use after ORDER BY and pagination expressions are rendered.
	w.tail(b.tail, b.distinct == selectAll && len(b.group) == 0 && len(b.having) == 0)
	w.foot(b.base)
}

// appendRows renders everything before ORDER BY: the rows that ordering and
// pagination select from, or that CountRows counts when cursor is nil. The
// projection is followed by the order expressions that may change those rows;
// CountRows passes ORDER BY there because it omits the clause itself.
func (w *renderer) selectRows(b *SelectBuilder, cursor []Expr, order []Order) {
	if !w.require(len(b.columns) > 0 || b.noColumns, "SELECT", "requires a projection or SelectNoColumns") {
		return
	}
	if !w.require(len(b.columns) > 0 || b.distinct == selectAll, "SELECT DISTINCT", "requires at least one projection") {
		return
	}
	w.head(b.base)
	if w.err != nil {
		return
	}
	w.text("SELECT ")
	switch b.distinct {
	case selectAll:
	case selectDistinct:
		w.text("DISTINCT ")
	case selectDistinctOn:
		if !w.require(len(b.distinctOn) > 0, "DISTINCT ON", "requires expressions") {
			return
		}
		w.text("DISTINCT ON (")
		w.exprs(b.distinctOn, ", ")
		if w.stopped("DISTINCT ON", 0) {
			return
		}
		w.text(") ")
	default:
		w.fail(ErrInvalid, "SELECT", "unknown quantifier")
		return
	}
	w.exprs(b.columns, ", ")
	if w.stopped("projection", 0) {
		return
	}
	w.selectOrderExprs(b, order)
	if w.err != nil {
		return
	}
	if len(b.from) > 0 {
		w.text(" FROM ")
		w.relations(b.from)
		if w.stopped("FROM", 0) {
			return
		}
	}
	w.selectWhere(b, cursor)
	if w.err != nil {
		return
	}
	if b.groupDistinct && !w.require(len(b.group) > 0, "GROUP BY", "DISTINCT requires grouping expressions") {
		return
	}
	if len(b.group) > 0 {
		w.text(" GROUP BY ")
		if b.groupDistinct {
			w.text("DISTINCT ")
		}
		w.exprs(b.group, ", ")
		if w.stopped("GROUP BY", 0) {
			return
		}
	}
	if len(b.having) > 0 {
		w.text(" HAVING ")
		w.conditions(b.having)
		if w.stopped("HAVING", 0) {
			return
		}
	}
	if len(b.windows) > 0 {
		if !w.validateWindows(b.windows) {
			return
		}
		w.text(" WINDOW ")
		for i, n := range b.windows {
			if i != 0 {
				w.text(", ")
			}
			w.identifierPart(n.name)
			w.text(" AS (")
			w.window(n.spec)
			if w.stopped("window", i+1) {
				return
			}
			w.byte(')')
		}
	}
}

// appendWhere renders the WHERE conditions with an active cursor as the last
// conjunct.
func (w *renderer) selectWhere(b *SelectBuilder, cursor []Expr) {
	if len(b.where) == 0 && len(cursor) == 0 {
		return
	}
	w.text(" WHERE ")
	w.conjunction(b.where, len(b.where) > 1 || len(cursor) > 0)
	if len(cursor) > 0 && w.err == nil {
		if len(b.where) > 0 {
			w.text(" AND ")
		}
		w.selectKeyset(b, cursor)
		w.stopped("keyset", 0)
	}
	w.stopped("WHERE", 0)
}
