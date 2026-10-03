package qs

type cteMaterialization uint8

const (
	cteMaterializationDefault cteMaterialization = iota
	cteMaterialized
	cteNotMaterialized
)

// WithQuery is an immutable CTE definition. A recursive self-reference uses
// CTERef(name), not a builder pointer cycle.
type WithQuery struct {
	body            Statement
	search          *SearchSpec
	cycle           *CycleSpec
	name            string
	columns         []string
	materialization cteMaterialization
}

// CTE defines a common table expression with the supplied name and body.
func CTE(name string, body Statement) WithQuery { return WithQuery{name: name, body: body} }

// Columns replaces the output-column aliases of a common table expression.
func (c WithQuery) Columns(names ...string) WithQuery { c.columns = cloneSlice(names); return c }

// Materialized requests separate CTE evaluation, replacing any inlining preference.
func (c WithQuery) Materialized() WithQuery { c.materialization = cteMaterialized; return c }

// NotMaterialized requests inlining where PostgreSQL permits it.
func (c WithQuery) NotMaterialized() WithQuery { c.materialization = cteNotMaterialized; return c }

// Ref refers to this CTE by name without embedding its body.
func (c WithQuery) Ref() Relation { return TableIdent(c.name) }

// CTERef names a CTE, including recursive self-references.
func CTERef(name string) Relation { return TableIdent(name) }

// SearchSpec configures recursive CTE SEARCH ordering and its generated column.
type SearchSpec struct {
	set     string
	columns []string
	breadth bool
}

// SearchBreadthFirst requests breadth-first recursive CTE search ordering.
func SearchBreadthFirst(columns ...string) SearchSpec {
	return SearchSpec{breadth: true, columns: cloneSlice(columns)}
}

// SearchDepthFirst requests depth-first recursive CTE search ordering.
func SearchDepthFirst(columns ...string) SearchSpec { return SearchSpec{columns: cloneSlice(columns)} }

// Set names the generated SEARCH order column.
func (s SearchSpec) Set(column string) SearchSpec { s.set = column; return s }

// Search adds a recursive CTE SEARCH clause.
func (c WithQuery) Search(spec SearchSpec) WithQuery { c.search = &spec; return c }

// CycleSpec configures recursive CTE cycle detection and its generated columns.
type CycleSpec struct {
	to        Expr
	otherwise Expr
	mark      string
	path      string
	columns   []string
	custom    bool
}

// Cycle starts a recursive CTE CYCLE clause for the supplied columns.
func Cycle(columns ...string) CycleSpec { return CycleSpec{columns: cloneSlice(columns)} }

// Set names the generated CYCLE mark column.
func (c CycleSpec) Set(mark string) CycleSpec { c.mark = mark; return c }

// Using names the generated CYCLE path column.
func (c CycleSpec) Using(path string) CycleSpec { c.path = path; return c }

// Values requires SQL literals because the CYCLE mark grammar is not a normal
// parameter expression position. LiteralString escapes its input safely.
func (c CycleSpec) Values(to, otherwise Expr) CycleSpec {
	c.to = to
	c.otherwise = otherwise
	c.custom = true
	return c
}

// Cycle adds a recursive CTE CYCLE clause.
func (c WithQuery) Cycle(spec CycleSpec) WithQuery { c.cycle = &spec; return c }

type statementBase struct {
	with      []WithQuery
	prefix    []Expr
	suffix    []Expr
	recursive bool
}

func (w *renderer) head(base statementBase) {
	for _, p := range base.prefix {
		w.expr(p)
		w.byte(' ')
	}
	if len(base.with) == 0 {
		return
	}
	w.withClause(base)
	w.byte(' ')
}

func (w *renderer) withClause(base statementBase) {
	w.text("WITH ")
	if base.recursive {
		w.text("RECURSIVE ")
	}
	for i, c := range base.with {
		if !w.withQuery(base, i, c) {
			return
		}
	}
}

func (w *renderer) withQuery(base statementBase, i int, c WithQuery) bool {
	for j := range i {
		if base.with[j].name == c.name {
			w.fail(ErrInvalid, "WITH", "duplicate CTE name")
			return false
		}
	}
	if i != 0 {
		w.text(", ")
	}
	w.identifierPart(c.name)
	if len(c.columns) > 0 {
		w.text(" (")
		w.names(c.columns)
		w.byte(')')
	}
	if !cteBody(c.body) {
		w.fail(ErrInvalid, "WITH", "CTE body must be SELECT, VALUES, TABLE, a set operation or data-modifying statement")
		return false
	}
	if isDML(c.body) && w.statementDepth > 1 {
		w.fail(ErrInvalid, "WITH", "data-modifying CTE must be attached to a top-level statement")
		return false
	}
	width := statementWidth(c.body, w.options.MaxDepth)
	if width >= 0 && !w.require(len(c.columns) <= width, "WITH", "too many output column aliases") {
		return false
	}
	w.text(" AS ")
	switch c.materialization {
	case cteMaterializationDefault:
	case cteMaterialized:
		w.text("MATERIALIZED ")
	case cteNotMaterialized:
		w.text("NOT MATERIALIZED ")
	default:
		w.fail(ErrInvalid, "WITH", "unknown materialization policy")
		return false
	}
	w.byte('(')
	w.statement(c.body)
	w.byte(')')
	return w.cteSearchCycle(base.recursive, c.search, c.cycle)
}

func (w *renderer) cteSearchCycle(recursive bool, search *SearchSpec, cycle *CycleSpec) bool {
	if search == nil && cycle == nil {
		return true
	}
	w.feature(PostgreSQL14, "CTE SEARCH/CYCLE")
	if !w.require(recursive, "WITH", "SEARCH/CYCLE requires WITH RECURSIVE") {
		return false
	}
	if search != nil && !w.cteSearch(search) {
		return false
	}
	if cycle != nil && !w.cteCycle(cycle) {
		return false
	}
	return true
}

func (w *renderer) cteSearch(s *SearchSpec) bool {
	if !w.require(len(s.columns) > 0 && s.set != "", "SEARCH", "requires columns and a SET column") {
		return false
	}
	if s.breadth {
		w.text(" SEARCH BREADTH FIRST BY ")
	} else {
		w.text(" SEARCH DEPTH FIRST BY ")
	}
	w.names(s.columns)
	w.text(" SET ")
	w.identifierPart(s.set)
	return true
}

func (w *renderer) cteCycle(s *CycleSpec) bool {
	if !w.require(len(s.columns) > 0 && s.mark != "" && s.path != "", "CYCLE", "requires columns, a mark and a path column") {
		return false
	}
	w.text(" CYCLE ")
	w.names(s.columns)
	w.text(" SET ")
	w.identifierPart(s.mark)
	if s.custom {
		if !w.require(w.cycleLiteral(s.to) && w.cycleLiteral(s.otherwise), "CYCLE", "mark values must be unsigned SQL constants") {
			return false
		}
		w.text(" TO ")
		w.expr(s.to)
		w.text(" DEFAULT ")
		w.expr(s.otherwise)
	}
	w.text(" USING ")
	w.identifierPart(s.path)
	return true
}

func (w *renderer) cycleLiteral(e Expr) bool {
	// CYCLE uses AexprConst, excluding unary signs and keyword expressions.
	// Inspect through version gates, but render the original node so its
	// feature and depth checks remain active.
	for depth := w.depth; depth < w.options.MaxDepth; depth++ {
		switch e.kind {
		case exprVersioned:
			gate, ok := e.value.(*versionedExpression)
			if !ok || gate == nil {
				return false
			}
			e = gate.expr
		case exprStringLiteral:
			return true
		case exprLiteral:
			return e.text != "" && e.text[0] != '+' && e.text[0] != '-'
		default:
			return false
		}
	}
	w.fail(ErrDepth, "CYCLE", "mark literal exceeds the nesting limit")
	return false
}

func (w *renderer) foot(base statementBase) {
	for _, s := range base.suffix {
		w.byte(' ')
		w.expr(s)
	}
}
func isDML(s Statement) bool {
	switch s.(type) {
	case *InsertRows, *InsertSelect, *InsertAssignments, *InsertDefaults,
		*UpdateBuilder, *DeleteBuilder, *MergeBuilder:
		return true
	default:
		return false
	}
}
func cteBody(s Statement) bool {
	if isDML(s) {
		return true
	}
	_, ok := s.(Rowset)
	return ok
}
