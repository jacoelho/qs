package qs

import "slices"

type selectQuantifier uint8

const (
	selectAll selectQuantifier = iota
	selectDistinct
	selectDistinctOn
)

// SelectBuilder is a mutable SELECT. Repeated list methods append; From and
// FromExpr replace the FROM list. Limit/Offset replace their previous values.
type SelectBuilder struct {
	base          statementBase
	columns       []Expr
	noColumns     bool
	from          []Relation
	where         []Condition
	group         []Expr
	groupDistinct bool
	having        []Condition
	windows       []namedWindow
	distinct      selectQuantifier
	distinctOn    []Expr
	tail          queryTail
}

// Select starts a query with expressions in projection order. Use Col for
// identifiers, Param for bound values, and UnsafeSQL for an existing trusted
// SQL projection list. SelectCols and SelectSQL are convenience wrappers.
func Select(columns ...Expr) *SelectBuilder { return &SelectBuilder{columns: cloneSlice(columns)} }

// SelectNoColumns intentionally returns rows with no columns. Select() remains
// an incomplete projection until columns are added.
func SelectNoColumns() *SelectBuilder { return &SelectBuilder{noColumns: true} }

// SelectCols is Select with each string interpreted as an identifier path,
// not SQL syntax. For casts and aliases, use Select with expression helpers.
func SelectCols(columns ...string) *SelectBuilder { return Select().ColumnNames(columns...) }

// SelectSQL is a convenience wrapper for Select with each argument wrapped in
// UnsafeSQL. It accepts trusted projection lists, not identifiers or user data.
// Column counts for these fragments are unknown; qs does not pretend that
// counting commas parses SQL. Prefer Select with expressions for new queries.
func SelectSQL(projections ...string) *SelectBuilder { return Select().ColumnsSQL(projections...) }

// Into terminates this SELECT as a standalone SELECT INTO statement. The
// source remains reusable and does not retain the destination.
func (b *SelectBuilder) Into(name string) *SelectIntoBuilder {
	return &SelectIntoBuilder{
		source:      b,
		destination: selectIntoTarget{name: name},
	}
}

// Columns appends expressions in projection order.
func (b *SelectBuilder) Columns(columns ...Expr) *SelectBuilder {
	b.columns = append(b.columns, columns...)
	return b
}

// ColumnNames appends identifier paths in projection order.
func (b *SelectBuilder) ColumnNames(columns ...string) *SelectBuilder {
	b.columns = slices.Grow(b.columns, len(columns))
	for _, c := range columns {
		b.columns = append(b.columns, Col(c))
	}
	return b
}

// ColumnsSQL appends trusted SQL projection fragments.
func (b *SelectBuilder) ColumnsSQL(projections ...string) *SelectBuilder {
	b.columns = slices.Grow(b.columns, len(projections))
	for _, c := range projections {
		b.columns = append(b.columns, UnsafeSQL(c))
	}
	return b
}
func (b *SelectBuilder) RemoveColumns() *SelectBuilder {
	clear(b.columns)
	b.columns = b.columns[:0]
	return b
}

// From replaces the table list. An empty call removes FROM.
func (b *SelectBuilder) From(tables ...string) *SelectBuilder {
	clear(b.from)
	b.from = b.from[:0]
	b.from = slices.Grow(b.from, len(tables))
	for _, table := range tables {
		b.from = append(b.from, Table(table))
	}
	return b
}

// FromExpr replaces the relation list. An empty call removes FROM.
func (b *SelectBuilder) FromExpr(relations ...Relation) *SelectBuilder {
	clear(b.from)
	b.from = append(b.from[:0], relations...)
	return b
}
func (b *SelectBuilder) Where(conditions ...Condition) *SelectBuilder {
	b.where = append(b.where, conditions...)
	return b
}
func (b *SelectBuilder) WhereIf(include bool, conditions ...Condition) *SelectBuilder {
	if include {
		b.Where(conditions...)
	}
	return b
}
func (b *SelectBuilder) RemoveWhere() *SelectBuilder { clear(b.where); b.where = b.where[:0]; return b }

// Distinct removes duplicate result rows and replaces any DISTINCT ON expressions.
func (b *SelectBuilder) Distinct() *SelectBuilder {
	b.distinct = selectDistinct
	b.distinctOn = nil
	return b
}

// All preserves duplicate rows and clears both DISTINCT and DISTINCT ON.
func (b *SelectBuilder) All() *SelectBuilder {
	b.distinct = selectAll
	b.distinctOn = nil
	return b
}
func (b *SelectBuilder) DistinctOn(expressions ...Expr) *SelectBuilder {
	b.distinct = selectDistinctOn
	b.distinctOn = cloneSlice(expressions)
	return b
}

// GroupBy appends identifier paths to the grouping list.
func (b *SelectBuilder) GroupBy(columns ...string) *SelectBuilder {
	b.group = slices.Grow(b.group, len(columns))
	for _, c := range columns {
		b.group = append(b.group, Col(c))
	}
	return b
}
func (b *SelectBuilder) GroupByExpr(expressions ...Expr) *SelectBuilder {
	b.group = append(b.group, expressions...)
	return b
}
func (b *SelectBuilder) GroupByDistinct() *SelectBuilder { b.groupDistinct = true; return b }
func (b *SelectBuilder) Having(conditions ...Condition) *SelectBuilder {
	b.having = append(b.having, conditions...)
	return b
}
func (b *SelectBuilder) Window(name string, spec WindowSpec) *SelectBuilder {
	b.windows = append(b.windows, namedWindow{name, spec})
	return b
}

// Join helpers attach to the last FROM item, matching PostgreSQL's binding of
// explicit JOIN more tightly than comma-separated FROM items. For nested joins,
// build a Relation with InnerJoin/LeftJoin/etc. and pass it to FromExpr.
func (b *SelectBuilder) appendJoin(right Relation, kind joinKind, on []Condition) *SelectBuilder {
	if len(b.from) == 0 {
		b.from = append(b.from, RelationSQL(invalidExpr("JOIN", "requires a FROM item")))
		return b
	}
	n := len(b.from) - 1
	b.from[n] = join(b.from[n], right, kind, false).On(on...)
	return b
}
func (b *SelectBuilder) Join(table string, on ...Condition) *SelectBuilder {
	return b.appendJoin(Table(table), joinInner, on)
}
func (b *SelectBuilder) LeftJoin(table string, on ...Condition) *SelectBuilder {
	return b.appendJoin(Table(table), joinLeft, on)
}
func (b *SelectBuilder) RightJoin(table string, on ...Condition) *SelectBuilder {
	return b.appendJoin(Table(table), joinRight, on)
}
func (b *SelectBuilder) FullJoin(table string, on ...Condition) *SelectBuilder {
	return b.appendJoin(Table(table), joinFull, on)
}
func (b *SelectBuilder) CrossJoin(table string) *SelectBuilder {
	return b.appendJoin(Table(table), joinCross, nil)
}
func (b *SelectBuilder) JoinExpr(relation Relation, on ...Condition) *SelectBuilder {
	return b.appendJoin(relation, joinInner, on)
}
func (b *SelectBuilder) LeftJoinExpr(relation Relation, on ...Condition) *SelectBuilder {
	return b.appendJoin(relation, joinLeft, on)
}
func (b *SelectBuilder) CrossJoinExpr(relation Relation) *SelectBuilder {
	return b.appendJoin(relation, joinCross, nil)
}

func (b *SelectBuilder) append(w *renderer) {
	b.appendInto(w, nil)
}

// appendInto renders the SELECT source for SELECT ... INTO. The destination
// is supplied by SelectIntoBuilder so SELECT remains a composable Rowset and
// never retains destination state itself.
func (b *SelectBuilder) appendInto(w *renderer, into *selectIntoDestination) {
	if !w.require(len(b.columns) > 0 || b.noColumns, "SELECT", "requires a projection or SelectNoColumns") {
		return
	}
	if !w.require(len(b.columns) > 0 || b.distinct == selectAll, "SELECT DISTINCT", "requires at least one projection") {
		return
	}
	w.head(b.base)
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
		w.text(") ")
	default:
		w.fail(ErrInvalid, "SELECT", "unknown quantifier")
		return
	}
	w.exprs(b.columns, ", ")
	if into != nil {
		into.append(w)
	}
	if len(b.from) > 0 {
		w.text(" FROM ")
		w.relations(b.from)
	}
	if len(b.where) > 0 {
		w.text(" WHERE ")
		w.conditions(b.where)
	}
	if b.groupDistinct && !w.require(len(b.group) > 0, "GROUP BY", "DISTINCT requires grouping expressions") {
		return
	}
	if len(b.group) > 0 {
		w.text(" GROUP BY ")
		if b.groupDistinct {
			w.feature(PostgreSQL14, "GROUP BY DISTINCT")
			w.text("DISTINCT ")
		}
		w.exprs(b.group, ", ")
	}
	if len(b.having) > 0 {
		w.text(" HAVING ")
		w.conditions(b.having)
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
			w.byte(')')
		}
	}
	w.tail(b.tail, b.distinct == selectAll && len(b.group) == 0 && len(b.having) == 0 && len(b.windows) == 0)
	w.foot(b.base)
}

// Reset releases retained parameter/expression references while retaining the
// capacities of the main clause lists. It invalidates any dependent subqueries.
func (b *SelectBuilder) Reset() {
	columns, from, where, group, having, windows, order := b.columns, b.from, b.where, b.group, b.having, b.windows, b.tail.order
	clear(columns)
	clear(from)
	clear(where)
	clear(group)
	clear(having)
	clear(windows)
	clear(order)
	*b = SelectBuilder{columns: columns[:0], from: from[:0], where: where[:0], group: group[:0], having: having[:0], windows: windows[:0], tail: queryTail{order: order[:0]}}
}
