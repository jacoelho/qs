package qs

// Clone copies the complete builder graph, preserving shared subqueries and
// cycles. Bound application values are intentionally not deep-copied. The
// original graph must not be mutated concurrently with Clone.
func Clone[T Statement](statement T) T {
	if any(statement) == nil {
		return statement
	}
	c := cloneContext{seen: make(map[Statement]Statement)}
	cloned, ok := c.statement(statement).(T)
	if !ok {
		panic("qs: Clone internal result is not the requested concrete statement type")
	}
	return cloned
}

type cloneContext struct{ seen map[Statement]Statement }

func (c *cloneContext) statement(s Statement) Statement {
	// Check known pointer types before using a value as a map key. A foreign
	// implementation embedded in a struct may otherwise be non-comparable.
	if !cloneableStatement(s) {
		return s
	}
	if v, ok := c.seen[s]; ok {
		return v
	}
	return c.cloneStatement(s)
}

func cloneableStatement(s Statement) bool {
	switch b := s.(type) {
	case *SelectBuilder:
		return b != nil
	case *InsertBuilder:
		return b != nil
	case *UpdateBuilder:
		return b != nil
	case *DeleteBuilder:
		return b != nil
	case *MergeBuilder:
		return b != nil
	case *SetBuilder:
		return b != nil
	case *ValuesBuilder:
		return b != nil
	case *TableBuilder:
		return b != nil
	case *ExplainBuilder:
		return b != nil
	case *TruncateBuilder:
		return b != nil
	case *ExecuteBuilder:
		return b != nil
	case *CreateTableAsBuilder:
		return b != nil
	case *MaterializedViewBuilder:
		return b != nil
	case *DeclareCursorBuilder:
		return b != nil
	case *SelectIntoBuilder:
		return b != nil
	case *SQLStatement:
		return b != nil
	default:
		return false
	}
}

func (c *cloneContext) cloneStatement(s Statement) Statement {
	switch b := s.(type) {
	case *SelectBuilder:
		return c.cloneSelect(b)
	case *InsertBuilder:
		return c.cloneInsert(b)
	case *UpdateBuilder:
		return c.cloneUpdate(b)
	case *DeleteBuilder:
		return c.cloneDelete(b)
	case *MergeBuilder:
		return c.cloneMerge(b)
	case *SetBuilder:
		return c.cloneSet(b)
	case *ValuesBuilder:
		return c.cloneValues(b)
	case *TableBuilder:
		return c.cloneTable(b)
	case *ExplainBuilder:
		return c.cloneExplain(b)
	case *TruncateBuilder:
		return c.cloneTruncate(b)
	case *ExecuteBuilder:
		return c.cloneExecute(b)
	case *CreateTableAsBuilder:
		return c.cloneCreateTableAs(b)
	case *MaterializedViewBuilder:
		return c.cloneMaterializedView(b)
	case *DeclareCursorBuilder:
		return c.cloneDeclareCursor(b)
	case *SelectIntoBuilder:
		return c.cloneSelectInto(b)
	case *SQLStatement:
		return c.cloneSQLStatement(b)
	default:
		return s
	}
}

func (c *cloneContext) remember(original, cloned Statement) {
	c.seen[original] = cloned
}

func (c *cloneContext) cloneSelect(b *SelectBuilder) *SelectBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.columns = c.exprs(b.columns)
	n.from = c.relations(b.from)
	n.where = c.conditions(b.where)
	n.group = c.exprs(b.group)
	n.having = c.conditions(b.having)
	n.distinctOn = c.exprs(b.distinctOn)
	n.tail = c.tail(b.tail)
	n.windows = cloneSlice(b.windows)
	for i := range n.windows {
		n.windows[i].spec = c.window(n.windows[i].spec)
	}
	return &n
}

func (c *cloneContext) cloneInsert(b *InsertBuilder) *InsertBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.table = c.relation(b.table)
	n.targets = c.exprs(b.targets)
	n.rows = c.rows(b.rows)
	n.set = c.assignments(b.set)
	n.source = c.rowset(b.source)
	n.returning = c.exprs(b.returning)
	if b.conflict != nil {
		v := c.conflict(*b.conflict)
		n.conflict = &v
	}
	return &n
}

func (c *cloneContext) cloneUpdate(b *UpdateBuilder) *UpdateBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.table = c.relation(b.table)
	n.set = c.assignments(b.set)
	n.from = c.relations(b.from)
	n.where = c.conditions(b.where)
	n.returning = c.exprs(b.returning)
	return &n
}

func (c *cloneContext) cloneDelete(b *DeleteBuilder) *DeleteBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.table = c.relation(b.table)
	n.using = c.relations(b.using)
	n.where = c.conditions(b.where)
	n.returning = c.exprs(b.returning)
	return &n
}

func (c *cloneContext) cloneMerge(b *MergeBuilder) *MergeBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.target = c.relation(b.target)
	n.source = c.relation(b.source)
	n.on = c.conditions(b.on)
	n.returning = c.exprs(b.returning)
	n.branches = cloneSlice(b.branches)
	for i := range n.branches {
		v := &n.branches[i]
		v.conditions = c.conditions(v.conditions)
		v.set = c.assignments(v.set)
		v.columns = cloneSlice(v.columns)
		v.values = c.exprs(v.values)
	}
	return &n
}

func (c *cloneContext) cloneSet(b *SetBuilder) *SetBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.left = c.rowset(b.left)
	n.right = c.rowset(b.right)
	n.tail = c.tail(b.tail)
	return &n
}

func (c *cloneContext) cloneValues(b *ValuesBuilder) *ValuesBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.rows = c.rows(b.rows)
	n.tail = c.tail(b.tail)
	return &n
}

func (c *cloneContext) cloneTable(b *TableBuilder) *TableBuilder {
	n := *b
	c.remember(b, &n)
	n.base = c.base(b.base)
	n.table = c.relation(b.table)
	n.tail = c.tail(b.tail)
	return &n
}

func (c *cloneContext) cloneExplain(b *ExplainBuilder) *ExplainBuilder {
	n := *b
	c.remember(b, &n)
	n.query = c.statement(b.query)
	n.options = cloneSlice(b.options)
	return &n
}

func (c *cloneContext) cloneTruncate(b *TruncateBuilder) *TruncateBuilder {
	n := *b
	c.remember(b, &n)
	n.tables = c.relations(b.tables)
	return &n
}

func (c *cloneContext) cloneExecute(b *ExecuteBuilder) *ExecuteBuilder {
	n := *b
	c.remember(b, &n)
	n.args = c.exprs(b.args)
	return &n
}

func (c *cloneContext) cloneCreateTableAs(b *CreateTableAsBuilder) *CreateTableAsBuilder {
	n := *b
	c.remember(b, &n)
	n.destination.columns = cloneSlice(b.destination.columns)
	n.source = c.utilitySource(b.source)
	return &n
}

func (c *cloneContext) cloneMaterializedView(b *MaterializedViewBuilder) *MaterializedViewBuilder {
	n := *b
	c.remember(b, &n)
	n.destination.columns = cloneSlice(b.destination.columns)
	n.query = c.rowset(b.query)
	return &n
}

func (c *cloneContext) cloneDeclareCursor(b *DeclareCursorBuilder) *DeclareCursorBuilder {
	n := *b
	c.remember(b, &n)
	n.query = c.rowset(b.query)
	return &n
}

func (c *cloneContext) cloneSelectInto(b *SelectIntoBuilder) *SelectIntoBuilder {
	n := *b
	c.remember(b, &n)
	if b.source != nil {
		n.source = ownedPayload[*SelectBuilder](c.statement(b.source))
	}
	return &n
}

func (c *cloneContext) cloneSQLStatement(b *SQLStatement) *SQLStatement {
	n := *b
	c.remember(b, &n)
	n.expr = c.expr(b.expr)
	return &n
}
func (c *cloneContext) rowset(s Rowset) Rowset {
	if s == nil {
		return nil
	}
	return ownedPayload[Rowset](c.statement(s))
}

func (c *cloneContext) utilitySource(s queryUtilitySource) queryUtilitySource {
	if s.executeSource {
		if s.execute != nil {
			s.execute = ownedPayload[*ExecuteBuilder](c.statement(s.execute))
		}
		return s
	}
	s.query = c.rowset(s.query)
	return s
}
func (c *cloneContext) exprs(v []Expr) []Expr {
	n := cloneSlice(v)
	for i := range n {
		n[i] = c.expr(n[i])
	}
	return n
}
func (c *cloneContext) rows(v [][]Expr) [][]Expr {
	n := cloneSlice(v)
	for i := range n {
		n[i] = c.exprs(n[i])
	}
	return n
}
func (c *cloneContext) conditions(v []Condition) []Condition {
	n := cloneSlice(v)
	for i := range n {
		n[i].expr = c.expr(n[i].expr)
	}
	return n
}
func (c *cloneContext) orders(v []Order) []Order {
	n := cloneSlice(v)
	for i := range n {
		n[i].expr = c.expr(n[i].expr)
	}
	return n
}
func (c *cloneContext) base(b statementBase) statementBase {
	b.prefix = c.exprs(b.prefix)
	b.suffix = c.exprs(b.suffix)
	b.with = cloneSlice(b.with)
	for i := range b.with {
		v := &b.with[i]
		v.body = c.statement(v.body)
		v.columns = cloneSlice(v.columns)
		if v.search != nil {
			n := *v.search
			n.columns = cloneSlice(n.columns)
			v.search = &n
		}
		if v.cycle != nil {
			n := *v.cycle
			n.columns = cloneSlice(n.columns)
			n.to = c.expr(n.to)
			n.otherwise = c.expr(n.otherwise)
			v.cycle = &n
		}
	}
	return b
}
func (c *cloneContext) tail(t queryTail) queryTail {
	t.order = c.orders(t.order)
	t.limit = c.expr(t.limit)
	t.offset = c.expr(t.offset)
	t.fetch = c.expr(t.fetch)
	t.locks = cloneSlice(t.locks)
	for i := range t.locks {
		t.locks[i].of = cloneSlice(t.locks[i].of)
	}
	return t
}
func (c *cloneContext) window(w WindowSpec) WindowSpec {
	w.partition = c.exprs(w.partition)
	w.order = c.orders(w.order)
	w.start.offset = c.expr(w.start.offset)
	w.end.offset = c.expr(w.end.offset)
	return w
}
func (c *cloneContext) expr(e Expr) Expr {
	e.node = c.expressionNode(e.node)
	return c.expressionValue(e)
}

func (c *cloneContext) expressionNode(node *expression) *expression {
	if node == nil {
		return nil
	}
	n := *node
	n.left = c.expr(n.left)
	n.right = c.expr(n.right)
	return &n
}

func (c *cloneContext) expressionValue(e Expr) Expr {
	switch e.kind {
	case exprInvalid, exprIdentifier, exprParameter, exprRaw, exprLiteral,
		exprKeyword, exprStringLiteral, exprBinary, exprPrefix, exprPostfix,
		exprAlias, exprSpecialCall, exprQuantified, exprField, exprFields,
		exprSubscript, exprCollate:
		// Scalar fields and the expression node were copied above. Bound values,
		// raw text and invalid-expression details are intentionally shallow.
		return e
	case exprIdentifierParts:
		e.value = identifierParts(cloneSlice(ownedPayload[identifierParts](e.value)))
	case exprGroup, exprFragment, exprList:
		e.value = c.exprs(ownedPayload[[]Expr](e.value))
	case exprCast:
		e = c.cloneCast(e)
	case exprCall:
		e = c.cloneCall(e)
	case exprSubquery, exprExists:
		e = c.cloneSubquery(e)
	case exprMembership:
		e = c.cloneMembership(e)
	case exprBetween:
		e = c.cloneBetween(e)
	case exprCase:
		e = c.cloneCase(e)
	case exprSlice:
		e = c.cloneSliceExpression(e)
	case exprXML:
		e = c.xml(e)
	case exprJSONConstructor:
		e = c.jsonConstructor(e)
	case exprSQLSyntax:
		e = c.cloneSQLSyntax(e)
	case exprSQLJSON:
		e = c.cloneSQLJSON(e)
	case exprVersioned:
		e = c.cloneVersioned(e)
	}
	return e
}

func (c *cloneContext) cloneCast(e Expr) Expr {
	n := *ownedPayload[*castExpression](e.value)
	n.expr = c.expr(n.expr)
	e.value = &n
	return e
}

func (c *cloneContext) cloneCall(e Expr) Expr {
	n := *ownedPayload[*callExpression](e.value)
	n.args = c.exprs(n.args)
	if n.mods != nil {
		m := *n.mods
		m.aggregateTail = c.aggregateTail(m.aggregateTail)
		m.order = c.orders(m.order)
		m.within = c.orders(m.within)
		n.mods = &m
	}
	e.value = &n
	return e
}

func (c *cloneContext) cloneSubquery(e Expr) Expr {
	n := *ownedPayload[*subqueryExpression](e.value)
	n.query = c.rowset(n.query)
	e.value = &n
	return e
}

func (c *cloneContext) cloneMembership(e Expr) Expr {
	n := *ownedPayload[*membershipExpression](e.value)
	n.left = c.expr(n.left)
	n.values = c.exprs(n.values)
	n.query = c.rowset(n.query)
	e.value = &n
	return e
}

func (c *cloneContext) cloneBetween(e Expr) Expr {
	n := *ownedPayload[*betweenExpression](e.value)
	n.value = c.expr(n.value)
	n.lower = c.expr(n.lower)
	n.upper = c.expr(n.upper)
	e.value = &n
	return e
}

func (c *cloneContext) cloneCase(e Expr) Expr {
	n := *ownedPayload[*caseExpression](e.value)
	n.operand = c.expr(n.operand)
	n.otherwise = c.expr(n.otherwise)
	n.branches = cloneSlice(n.branches)
	for i := range n.branches {
		n.branches[i].when = c.expr(n.branches[i].when)
		n.branches[i].then = c.expr(n.branches[i].then)
	}
	e.value = &n
	return e
}

func (c *cloneContext) cloneSliceExpression(e Expr) Expr {
	n := *ownedPayload[*sliceExpression](e.value)
	n.value = c.expr(n.value)
	n.lower = c.expr(n.lower)
	n.upper = c.expr(n.upper)
	e.value = &n
	return e
}

func (c *cloneContext) cloneSQLSyntax(e Expr) Expr {
	n := *ownedPayload[*sqlSyntaxExpression](e.value)
	n.args = c.exprs(n.args)
	e.value = &n
	return e
}

func (c *cloneContext) cloneSQLJSON(e Expr) Expr {
	n := *ownedPayload[*JSONQueryBuilder](e.value)
	n.document = c.expr(n.document)
	n.path = c.expr(n.path)
	n.passing = c.passing(n.passing)
	n.options = c.jsonOptions(n.options)
	e.value = &n
	return e
}

func (c *cloneContext) cloneVersioned(e Expr) Expr {
	n := *ownedPayload[*versionedExpression](e.value)
	n.expr = c.expr(n.expr)
	e.value = &n
	return e
}
func (c *cloneContext) relations(v []Relation) []Relation {
	n := cloneSlice(v)
	for i := range n {
		n[i] = c.relation(n[i])
	}
	return n
}
func (c *cloneContext) record(f RecordFunction) RecordFunction {
	f.expr = c.expr(f.expr)
	f.columns = cloneSlice(f.columns)
	return f
}
func (c *cloneContext) relation(r Relation) Relation {
	r.columns = cloneSlice(r.columns)
	if r.sample != nil {
		n := *r.sample
		n.arguments = c.exprs(n.arguments)
		n.seed = c.expr(n.seed)
		r.sample = &n
	}
	switch r.kind {
	case relationTable:
		// The table name and relation flags are scalar fields copied above.
	case relationTableIdent:
		r.value = identifierParts(cloneSlice(ownedPayload[identifierParts](r.value)))
	case relationSubquery:
		if r.value != nil {
			r.value = c.rowset(ownedPayload[Rowset](r.value))
		}
	case relationJoin:
		n := *ownedPayload[*joinExpression](r.value)
		n.left = c.relation(n.left)
		n.right = c.relation(n.right)
		n.on = c.conditions(n.on)
		n.using = cloneSlice(n.using)
		r.value = &n
	case relationFunction:
		r.value = c.record(ownedPayload[RecordFunction](r.value))
	case relationRowsFrom:
		n := cloneSlice(ownedPayload[[]RecordFunction](r.value))
		for i := range n {
			n[i] = c.record(n[i])
		}
		r.value = n
	case relationSQL:
		r.value = c.expr(ownedPayload[Expr](r.value))
	case relationJSONTable:
		n := *ownedPayload[*JSONTableBuilder](r.value)
		n.document = c.expr(n.document)
		n.passing = c.passing(n.passing)
		n.columns = c.jsonColumns(n.columns)
		n.onError.value = c.expr(n.onError.value)
		r.value = &n
	case relationXMLTable:
		r.value = c.xmlTable(*ownedPayload[*XMLTableBuilder](r.value))
	}
	return r
}
func (c *cloneContext) assignments(v []Assignment) []Assignment {
	n := cloneSlice(v)
	for i := range n {
		a := &n[i]
		a.target = c.expr(a.target)
		a.value = c.expr(a.value)
		a.columns = c.exprs(a.columns)
		a.query = c.rowset(a.query)
	}
	return n
}
func (c *cloneContext) conflict(v ConflictClause) ConflictClause {
	v.columns = cloneSlice(v.columns)
	v.index = cloneSlice(v.index)
	for i := range v.index {
		v.index[i].expr = c.expr(v.index[i].expr)
	}
	v.targetWhere = c.conditions(v.targetWhere)
	v.updates = c.assignments(v.updates)
	v.where = c.conditions(v.where)
	return v
}
func (c *cloneContext) passing(v []jsonPassing) []jsonPassing {
	n := cloneSlice(v)
	for i := range n {
		n[i].value = c.expr(n[i].value)
	}
	return n
}
func (c *cloneContext) jsonOptions(v jsonOptions) jsonOptions {
	v.empty.value = c.expr(v.empty.value)
	v.onError.value = c.expr(v.onError.value)
	return v
}
func (c *cloneContext) jsonColumns(v []JSONTableColumn) []JSONTableColumn {
	n := cloneSlice(v)
	for i := range n {
		n[i].options = c.jsonOptions(n[i].options)
		n[i].columns = c.jsonColumns(n[i].columns)
	}
	return n
}
