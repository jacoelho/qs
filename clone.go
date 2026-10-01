package qx

// Clone copies the complete builder graph, preserving shared subqueries and
// cycles. Bound application values are intentionally not deep-copied. The
// original graph must not be mutated concurrently with Clone.
func Clone[T Statement](statement T) T {
	if any(statement) == nil {
		return statement
	}
	c := cloneContext{seen: make(map[Statement]Statement)}
	return c.statement(statement).(T)
}

type cloneContext struct{ seen map[Statement]Statement }

func (c *cloneContext) statement(s Statement) Statement {
	// Check known pointer types before using a value as a map key. A foreign
	// implementation embedded in a struct may otherwise be non-comparable.
	switch b := s.(type) {
	case *SelectBuilder:
		if b == nil {
			return s
		}
	case *InsertBuilder:
		if b == nil {
			return s
		}
	case *UpdateBuilder:
		if b == nil {
			return s
		}
	case *DeleteBuilder:
		if b == nil {
			return s
		}
	case *MergeBuilder:
		if b == nil {
			return s
		}
	case *SetBuilder:
		if b == nil {
			return s
		}
	case *ValuesBuilder:
		if b == nil {
			return s
		}
	case *TableBuilder:
		if b == nil {
			return s
		}
	case *ExplainBuilder:
		if b == nil {
			return s
		}
	case *TruncateBuilder:
		if b == nil {
			return s
		}
	case *ExecuteBuilder:
		if b == nil {
			return s
		}
	case *CreateTableAsBuilder:
		if b == nil {
			return s
		}
	case *MaterializedViewBuilder:
		if b == nil {
			return s
		}
	case *DeclareCursorBuilder:
		if b == nil {
			return s
		}
	case *SelectIntoBuilder:
		if b == nil {
			return s
		}
	case *SQLStatement:
		if b == nil {
			return s
		}
	default:
		return s
	}
	if v, ok := c.seen[s]; ok {
		return v
	}
	switch b := s.(type) {
	case *SelectBuilder:
		n := *b
		c.seen[s] = &n
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
	case *InsertBuilder:
		n := *b
		c.seen[s] = &n
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
	case *UpdateBuilder:
		n := *b
		c.seen[s] = &n
		n.base = c.base(b.base)
		n.table = c.relation(b.table)
		n.set = c.assignments(b.set)
		n.from = c.relations(b.from)
		n.where = c.conditions(b.where)
		n.returning = c.exprs(b.returning)
		return &n
	case *DeleteBuilder:
		n := *b
		c.seen[s] = &n
		n.base = c.base(b.base)
		n.table = c.relation(b.table)
		n.using = c.relations(b.using)
		n.where = c.conditions(b.where)
		n.returning = c.exprs(b.returning)
		return &n
	case *MergeBuilder:
		n := *b
		c.seen[s] = &n
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
	case *SetBuilder:
		n := *b
		c.seen[s] = &n
		n.base = c.base(b.base)
		n.left = c.rowset(b.left)
		n.right = c.rowset(b.right)
		n.tail = c.tail(b.tail)
		return &n
	case *ValuesBuilder:
		n := *b
		c.seen[s] = &n
		n.base = c.base(b.base)
		n.rows = c.rows(b.rows)
		n.tail = c.tail(b.tail)
		return &n
	case *TableBuilder:
		n := *b
		c.seen[s] = &n
		n.base = c.base(b.base)
		n.table = c.relation(b.table)
		n.tail = c.tail(b.tail)
		return &n
	case *ExplainBuilder:
		n := *b
		c.seen[s] = &n
		n.query = c.statement(b.query)
		n.options = cloneSlice(b.options)
		return &n
	case *TruncateBuilder:
		n := *b
		c.seen[s] = &n
		n.tables = c.relations(b.tables)
		return &n
	case *ExecuteBuilder:
		n := *b
		c.seen[s] = &n
		n.args = c.exprs(b.args)
		return &n
	case *CreateTableAsBuilder:
		n := *b
		c.seen[s] = &n
		n.destination.columns = cloneSlice(b.destination.columns)
		n.source = c.utilitySource(b.source)
		return &n
	case *MaterializedViewBuilder:
		n := *b
		c.seen[s] = &n
		n.destination.columns = cloneSlice(b.destination.columns)
		n.query = c.rowset(b.query)
		return &n
	case *DeclareCursorBuilder:
		n := *b
		c.seen[s] = &n
		n.query = c.rowset(b.query)
		return &n
	case *SelectIntoBuilder:
		n := *b
		c.seen[s] = &n
		if b.source != nil {
			n.source = c.statement(b.source).(*SelectBuilder)
		}
		return &n
	case *SQLStatement:
		n := *b
		c.seen[s] = &n
		n.expr = c.expr(b.expr)
		return &n
	}
	return s
}
func (c *cloneContext) rowset(s Rowset) Rowset {
	if s == nil {
		return nil
	}
	return c.statement(s).(Rowset)
}

func (c *cloneContext) utilitySource(s queryUtilitySource) queryUtilitySource {
	if s.executeSource {
		if s.execute != nil {
			s.execute = c.statement(s.execute).(*ExecuteBuilder)
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
	if e.node != nil {
		n := *e.node
		n.left = c.expr(n.left)
		n.right = c.expr(n.right)
		e.node = &n
	}
	switch e.kind {
	case exprIdentifierParts:
		e.value = identifierParts(cloneSlice(e.value.(identifierParts)))
	case exprGroup, exprFragment, exprList:
		e.value = c.exprs(e.value.([]Expr))
	case exprCast:
		n := *e.value.(*castExpression)
		n.expr = c.expr(n.expr)
		e.value = &n
	case exprCall:
		n := *e.value.(*callExpression)
		n.args = c.exprs(n.args)
		if n.mods != nil {
			m := *n.mods
			m.aggregateTail = c.aggregateTail(m.aggregateTail)
			m.order = c.orders(m.order)
			m.within = c.orders(m.within)
			n.mods = &m
		}
		e.value = &n
	case exprSubquery, exprExists:
		n := *e.value.(*subqueryExpression)
		n.query = c.rowset(n.query)
		e.value = &n
	case exprMembership:
		n := *e.value.(*membershipExpression)
		n.left = c.expr(n.left)
		n.values = c.exprs(n.values)
		n.query = c.rowset(n.query)
		e.value = &n
	case exprBetween:
		n := *e.value.(*betweenExpression)
		n.value = c.expr(n.value)
		n.lower = c.expr(n.lower)
		n.upper = c.expr(n.upper)
		e.value = &n
	case exprCase:
		n := *e.value.(*CaseBuilder)
		n.operand = c.expr(n.operand)
		n.otherwise = c.expr(n.otherwise)
		n.branches = cloneSlice(n.branches)
		for i := range n.branches {
			n.branches[i].when = c.expr(n.branches[i].when)
			n.branches[i].then = c.expr(n.branches[i].then)
		}
		e.value = &n
	case exprSlice:
		n := *e.value.(*sliceExpression)
		n.value = c.expr(n.value)
		n.lower = c.expr(n.lower)
		n.upper = c.expr(n.upper)
		e.value = &n
	case exprXML:
		e = c.xml(e)
	case exprJSONConstructor:
		e = c.jsonConstructor(e)
	case exprSQLSyntax:
		n := *e.value.(*sqlSyntaxExpression)
		n.args = c.exprs(n.args)
		e.value = &n
	case exprSQLJSON:
		n := *e.value.(*JSONQueryBuilder)
		n.document = c.expr(n.document)
		n.path = c.expr(n.path)
		n.passing = c.passing(n.passing)
		n.options = c.jsonOptions(n.options)
		e.value = &n
	case exprVersioned:
		n := *e.value.(*versionedExpression)
		n.expr = c.expr(n.expr)
		e.value = &n
	}
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
	case relationTableIdent:
		r.value = identifierParts(cloneSlice(r.value.(identifierParts)))
	case relationSubquery:
		if r.value != nil {
			r.value = c.rowset(r.value.(Rowset))
		}
	case relationJoin:
		n := *r.value.(*joinExpression)
		n.left = c.relation(n.left)
		n.right = c.relation(n.right)
		n.on = c.conditions(n.on)
		n.using = cloneSlice(n.using)
		r.value = &n
	case relationFunction:
		r.value = c.record(r.value.(RecordFunction))
	case relationRowsFrom:
		n := cloneSlice(r.value.([]RecordFunction))
		for i := range n {
			n[i] = c.record(n[i])
		}
		r.value = n
	case relationSQL:
		r.value = c.expr(r.value.(Expr))
	case relationJSONTable:
		n := *r.value.(*JSONTableBuilder)
		n.document = c.expr(n.document)
		n.passing = c.passing(n.passing)
		n.columns = c.jsonColumns(n.columns)
		n.onError.value = c.expr(n.onError.value)
		r.value = &n
	case relationXMLTable:
		r.value = c.xmlTable(*r.value.(*XMLTableBuilder))
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
