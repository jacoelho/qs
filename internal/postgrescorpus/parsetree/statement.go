package parsetree

import (
	qs "github.com/jacoelho/qs"
)

// placeholder stands in for a query that could not be built. It is never
// rendered: Build discards the statement once a gap is recorded.
var placeholder = qs.SelectNoColumns()

func (b *builder) statement(n node) qs.Statement {
	switch n.tag {
	case tagSelect:
		return b.selectStmt(n)
	case "InsertStmt":
		return b.insert(n)
	case "UpdateStmt":
		return b.update(n)
	case "DeleteStmt":
		return b.delete(n)
	case "MergeStmt":
		return b.merge(n)
	}
	b.gapf("unsupported statement node %s", n.tag)
	return placeholder
}

// rowset builds a query in subquery position.
func (b *builder) rowset(n node) qs.Rowset {
	if n.tag != tagSelect {
		b.gapf("unsupported rowset node %s", n.tag)
		return placeholder
	}
	return b.selectStmt(n)
}

// selectStmt accepts both tagged SelectStmt nodes and the untagged operands
// of a set operation.
func (b *builder) selectStmt(n node) qs.Rowset {
	if b.stopped() {
		return placeholder
	}
	if op := n.text("op"); op != "SETOP_NONE" {
		return b.setOperation(n, op)
	}
	if n.has("valuesLists") {
		return b.values(n)
	}
	return b.plainSelect(n)
}

func (b *builder) setOperation(n node, op string) qs.Rowset {
	var combine func(left, right qs.Rowset, rest ...qs.Rowset) qs.SetBuilder
	all := n.flag("all")
	switch {
	case op == "SETOP_UNION" && all:
		combine = qs.UnionAll
	case op == "SETOP_UNION":
		combine = qs.Union
	case op == "SETOP_INTERSECT" && all:
		combine = qs.IntersectAll
	case op == "SETOP_INTERSECT":
		combine = qs.Intersect
	case op == "SETOP_EXCEPT" && all:
		combine = qs.ExceptAll
	case op == "SETOP_EXCEPT":
		combine = qs.Except
	default:
		b.gapf("set operation %s", op)
		return placeholder
	}
	left := b.selectStmt(n.record("larg"))
	right := b.selectStmt(n.record("rarg"))
	query := paginate(b, withQueries(b, combine(left, right), n), n)
	b.rejectLocks(n)
	return query
}

func (b *builder) values(n node) qs.Rowset {
	rows := n.nodes("valuesLists")
	if len(rows) == 0 {
		b.gapf("empty VALUES")
		return placeholder
	}
	query := qs.ValuesExpr(b.exprs(rows[0].members())...)
	for _, row := range rows[1:] {
		query = query.RowExpr(b.exprs(row.members())...)
	}
	query = paginate(b, withQueries(b, query, n), n)
	b.rejectLocks(n)
	return query
}

func (b *builder) rejectLocks(n node) {
	if n.has("lockingClause") {
		b.gapf("locking clause on a non-SELECT rowset")
	}
}

func (b *builder) plainSelect(n node) qs.Rowset {
	query := qs.SelectNoColumns()
	if targets := n.nodes("targetList"); len(targets) != 0 {
		query = qs.SelectSlice(b.projection(targets, "projection"))
	}
	if distinct := n.nodes("distinctClause"); len(distinct) != 0 {
		// Plain DISTINCT is a list holding one absent member.
		if len(distinct) == 1 && distinct[0].tag == "" {
			query = query.Distinct()
		} else {
			query = query.DistinctOn(b.exprs(distinct)...)
		}
	}
	if from := n.nodes("fromClause"); len(from) != 0 {
		query = query.FromExpr(b.relations(from)...)
	}
	if where := n.node("whereClause"); where.present() {
		query = query.Where(b.condition(where))
	}
	if group := n.nodes("groupClause"); len(group) != 0 {
		expressions := make([]qs.Expr, len(group))
		for index, item := range group {
			expressions[index] = b.groupExpr(item)
		}
		query = query.GroupByExpr(expressions...)
	}
	if n.flag("groupDistinct") {
		query = query.GroupByDistinct()
	}
	if having := n.node("havingClause"); having.present() {
		query = query.Having(b.condition(having))
	}
	for _, window := range n.nodes("windowClause") {
		name := window.text("name")
		if name == "" {
			b.gapf("unnamed WINDOW definition")
		}
		query = query.Window(name, b.window(window))
	}
	query = paginate(b, withQueries(b, query, n), n)
	for _, lock := range n.nodes("lockingClause") {
		query = query.Lock(b.lock(lock))
	}
	return query
}

// projection builds SELECT and RETURNING output expressions.
func (b *builder) projection(targets []node, clause string) []qs.Expr {
	expressions := make([]qs.Expr, len(targets))
	for index, target := range targets {
		if target.has("indirection") {
			b.gapf("%s target indirection", clause)
		}
		expression := b.expr(target.node("val"))
		if name := target.text("name"); name != "" {
			expression = expression.As(name)
		}
		expressions[index] = expression
	}
	return expressions
}

func (b *builder) lock(n node) qs.LockClause {
	var clause qs.LockClause
	switch strength := n.text("strength"); strength {
	case "LCS_FORUPDATE":
		clause = qs.ForUpdate()
	case "LCS_FORNOKEYUPDATE":
		clause = qs.ForNoKeyUpdate()
	case "LCS_FORSHARE":
		clause = qs.ForShare()
	case "LCS_FORKEYSHARE":
		clause = qs.ForKeyShare()
	default:
		b.gapf("locking strength %s", strength)
	}
	if relations := n.nodes("lockedRels"); len(relations) != 0 {
		names := make([]string, len(relations))
		for index, relation := range relations {
			if relation.has("schemaname") || relation.has("catalogname") {
				b.gapf("qualified lock target")
			}
			names[index] = relation.text("relname")
		}
		clause = clause.Of(names...)
	}
	switch n.text("waitPolicy") {
	case "LockWaitError":
		clause = clause.NoWait()
	case "LockWaitSkip":
		clause = clause.SkipLocked()
	}
	return clause
}

// commonTables is the WITH method set shared by statements that accept
// recursive CTEs.
type commonTables[T any] interface {
	With(ctes ...qs.WithQuery) T
	WithRecursive(ctes ...qs.WithQuery) T
}

func withQueries[T commonTables[T]](b *builder, query T, n node) T {
	clause := n.record("withClause")
	if !clause.present() {
		return query
	}
	if clause.flag("recursive") {
		return query.WithRecursive(b.ctes(clause)...)
	}
	return query.With(b.ctes(clause)...)
}

func (b *builder) ctes(clause node) []qs.WithQuery {
	items := clause.nodes("ctes")
	queries := make([]qs.WithQuery, len(items))
	for index, item := range items {
		query := qs.CTE(item.text("ctename"), b.statement(item.node("ctequery")))
		if columns := item.strings("aliascolnames"); len(columns) != 0 {
			query = query.Columns(columns...)
		}
		switch item.text("ctematerialized") {
		case "CTEMaterializeAlways":
			query = query.Materialized()
		case "CTEMaterializeNever":
			query = query.NotMaterialized()
		}
		if search := item.record("search_clause"); search.present() {
			columns := search.strings("search_col_list")
			spec := qs.SearchDepthFirst(columns...)
			if search.flag("search_breadth_first") {
				spec = qs.SearchBreadthFirst(columns...)
			}
			query = query.Search(spec.Set(search.text("search_seq_column")))
		}
		if cycle := item.record("cycle_clause"); cycle.present() {
			spec := qs.Cycle(cycle.strings("cycle_col_list")...).
				Set(cycle.text("cycle_mark_column")).
				Using(cycle.text("cycle_path_column"))
			// The parser fills in TRUE and FALSE when the source omits them.
			if cycle.has("cycle_mark_value") || cycle.has("cycle_mark_default") {
				spec = spec.Values(b.expr(cycle.node("cycle_mark_value")), b.expr(cycle.node("cycle_mark_default")))
			}
			query = query.Cycle(spec)
		}
		queries[index] = query
	}
	return queries
}

// pagination is the ORDER BY, OFFSET and LIMIT method set of every rowset.
type pagination[T any] interface {
	OrderBy(terms ...qs.Order) T
	OffsetExpr(count qs.Expr) T
	LimitExpr(count qs.Expr) T
	LimitWithTiesExpr(count qs.Expr) T
}

func paginate[T pagination[T]](b *builder, query T, n node) T {
	if sort := n.nodes("sortClause"); len(sort) != 0 {
		query = query.OrderBy(b.orders(sort)...)
	}
	if offset := n.node("limitOffset"); offset.present() {
		query = query.OffsetExpr(b.expr(offset))
	}
	if count := n.node("limitCount"); count.present() {
		// LIMIT ALL reaches here as a NULL constant and renders as LIMIT NULL,
		// which PostgreSQL parses to the same tree.
		if n.text("limitOption") == "LIMIT_OPTION_WITH_TIES" {
			query = query.LimitWithTiesExpr(b.expr(count))
		} else {
			query = query.LimitExpr(b.expr(count))
		}
	}
	return query
}

func (b *builder) orders(items []node) []qs.Order {
	terms := make([]qs.Order, len(items))
	for index, item := range items {
		terms[index] = b.order(item)
	}
	return terms
}

func (b *builder) order(n node) qs.Order {
	value := b.expr(n.node("node"))
	var term qs.Order
	switch n.text("sortby_dir") {
	case "SORTBY_DESC":
		term = value.Desc()
	case "SORTBY_USING":
		operator := n.strings("useOp")
		if len(operator) != 1 {
			b.gapf("ORDER BY USING has no single operator")
			return term
		}
		term = value.Using(operator[0])
	default:
		// qs has no implicit direction; ASC is PostgreSQL's default.
		term = value.Asc()
	}
	switch n.text("sortby_nulls") {
	case "SORTBY_NULLS_FIRST":
		term = term.NullsFirst()
	case "SORTBY_NULLS_LAST":
		term = term.NullsLast()
	}
	return term
}

func (b *builder) groupExpr(n node) qs.Expr {
	if n.tag != "GroupingSet" {
		return b.expr(n)
	}
	content := n.nodes("content")
	switch kind := n.text("kind"); kind {
	case "GROUPING_SET_EMPTY":
		return qs.GroupingSet()
	case "GROUPING_SET_SIMPLE":
		return qs.GroupingSet(b.exprs(content)...)
	case "GROUPING_SET_ROLLUP":
		return qs.Rollup(b.exprs(content)...)
	case "GROUPING_SET_CUBE":
		return qs.Cube(b.exprs(content)...)
	case "GROUPING_SET_SETS":
		sets := make([]qs.Expr, len(content))
		for index, item := range content {
			sets[index] = b.groupExpr(item)
		}
		return qs.GroupingSets(sets...)
	default:
		b.gapf("unknown grouping set kind %s", kind)
		return qs.Expr{}
	}
}

func (b *builder) window(n node) qs.WindowSpec {
	spec := qs.Window()
	if base := n.text("refname"); base != "" {
		spec = spec.Base(base)
	}
	if partition := n.nodes("partitionClause"); len(partition) != 0 {
		spec = spec.PartitionBy(b.exprs(partition)...)
	}
	if order := n.nodes("orderClause"); len(order) != 0 {
		spec = spec.OrderBy(b.orders(order)...)
	}
	return b.frame(spec, n)
}

// PostgreSQL FRAMEOPTION_* bits; a stable part of the parse-tree format.
const (
	frameRange                   = 0x00002
	frameRows                    = 0x00004
	frameGroups                  = 0x00008
	frameBetween                 = 0x00010
	frameStartUnboundedPreceding = 0x00020
	frameEndUnboundedPreceding   = 0x00040
	frameStartUnboundedFollowing = 0x00080
	frameEndUnboundedFollowing   = 0x00100
	frameStartCurrentRow         = 0x00200
	frameEndCurrentRow           = 0x00400
	frameStartOffsetPreceding    = 0x00800
	frameEndOffsetPreceding      = 0x01000
	frameStartOffsetFollowing    = 0x02000
	frameEndOffsetFollowing      = 0x04000
	frameExcludeCurrentRow       = 0x08000
	frameExcludeGroup            = 0x10000
	frameExcludeTies             = 0x20000
)

// frame spells the frame explicitly even when the source relied on the
// default: the parser records the default frame in the same bits.
func (b *builder) frame(spec qs.WindowSpec, n node) qs.WindowSpec {
	options := n.integer("frameOptions")
	if options == 0 {
		return spec
	}
	start := b.frameStart(options, n)
	between := options&frameBetween != 0
	var end qs.Bound
	if between {
		end = b.frameEnd(options, n)
	}
	switch {
	case options&frameRows != 0 && between:
		spec = spec.RowsBetween(start, end)
	case options&frameRows != 0:
		spec = spec.Rows(start)
	case options&frameGroups != 0 && between:
		spec = spec.GroupsBetween(start, end)
	case options&frameGroups != 0:
		spec = spec.Groups(start)
	case options&frameRange != 0 && between:
		spec = spec.RangeBetween(start, end)
	case options&frameRange != 0:
		spec = spec.Range(start)
	default:
		b.gapf("unknown window frame kind %#x", options)
		return spec
	}
	var exclusions []qs.FrameExclusion
	if options&frameExcludeCurrentRow != 0 {
		exclusions = append(exclusions, qs.ExcludeCurrentRow)
	}
	if options&frameExcludeGroup != 0 {
		exclusions = append(exclusions, qs.ExcludeGroup)
	}
	if options&frameExcludeTies != 0 {
		exclusions = append(exclusions, qs.ExcludeTies)
	}
	switch len(exclusions) {
	case 0:
	case 1:
		spec = spec.Exclude(exclusions[0])
	default:
		b.gapf("multiple window frame exclusions")
	}
	return spec
}

func (b *builder) frameStart(options int64, n node) qs.Bound {
	switch {
	case options&frameStartUnboundedPreceding != 0:
		return qs.UnboundedPreceding()
	case options&frameStartUnboundedFollowing != 0:
		return qs.UnboundedFollowing()
	case options&frameStartCurrentRow != 0:
		return qs.CurrentRow()
	case options&frameStartOffsetPreceding != 0:
		return qs.PrecedingExpr(b.frameOffset(n, "startOffset"))
	case options&frameStartOffsetFollowing != 0:
		return qs.FollowingExpr(b.frameOffset(n, "startOffset"))
	}
	return qs.UnboundedPreceding()
}

func (b *builder) frameEnd(options int64, n node) qs.Bound {
	switch {
	case options&frameEndUnboundedPreceding != 0:
		return qs.UnboundedPreceding()
	case options&frameEndUnboundedFollowing != 0:
		return qs.UnboundedFollowing()
	case options&frameEndCurrentRow != 0:
		return qs.CurrentRow()
	case options&frameEndOffsetPreceding != 0:
		return qs.PrecedingExpr(b.frameOffset(n, "endOffset"))
	case options&frameEndOffsetFollowing != 0:
		return qs.FollowingExpr(b.frameOffset(n, "endOffset"))
	}
	return qs.CurrentRow()
}

func (b *builder) frameOffset(n node, field string) qs.Expr {
	offset := n.node(field)
	if !offset.present() {
		b.gapf("window %s is missing", field)
		return qs.Expr{}
	}
	return b.expr(offset)
}
