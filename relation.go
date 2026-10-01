package qx

import "slices"

type relationKind uint8

const (
	relationTable relationKind = iota + 1
	relationTableIdent
	relationSubquery
	relationJoin
	relationFunction
	relationRowsFrom
	relationSQL
	relationJSONTable
	relationXMLTable
)

type joinKind uint8

const (
	joinInner joinKind = iota + 1
	joinLeft
	joinRight
	joinFull
	joinCross
)

type joinExpression struct {
	left, right Relation
	kind        joinKind
	natural     bool
	on          []Condition
	using       []string
	usingAlias  string
}

type sampleClause struct {
	method    string
	arguments []Expr
	seed      Expr
	hasSeed   bool
}

// Relation is an immutable FROM item, including a nested join tree.
type Relation struct {
	kind       relationKind
	name       string
	value      any
	alias      string
	columns    []string
	only       bool
	lateral    bool
	ordinality bool
	noAlias    bool
	sample     *sampleClause
}

func Table(name string) Relation { return Relation{kind: relationTable, name: name} }
func TableIdent(parts ...string) Relation {
	return Relation{kind: relationTableIdent, value: identifierParts(cloneSlice(parts))}
}
func Subquery(query Rowset, alias string, columns ...string) Relation {
	return Relation{kind: relationSubquery, value: query, alias: alias, columns: cloneSlice(columns)}
}

// Derived creates an unaliased FROM subquery (PostgreSQL 16+). Use As to name
// it when its columns need relation qualification.
func Derived(query Rowset) Relation {
	return Relation{kind: relationSubquery, value: query, noAlias: true}
}
func RelationSQL(parts ...Expr) Relation {
	return Relation{kind: relationSQL, value: Fragment(parts...)}
}
func (r Relation) As(alias string, columns ...string) Relation {
	r.alias = alias
	r.columns = cloneSlice(columns)
	return r
}
func (r Relation) Only() Relation           { r.only = true; return r }
func Lateral(r Relation) Relation           { r.lateral = true; return r }
func (r Relation) WithOrdinality() Relation { r.ordinality = true; return r }

func (r Relation) TableSample(method string, arguments ...Expr) Relation {
	r.sample = &sampleClause{method: method, arguments: cloneSlice(arguments)}
	return r
}
func (r Relation) Repeatable(seed Expr) Relation {
	var sample sampleClause
	if r.sample != nil {
		sample = *r.sample
	}
	sample.seed = seed
	sample.hasSeed = true
	r.sample = &sample
	return r
}

// Col refers to a named column using the relation's alias (when present).
func (r Relation) Col(column string) Expr {
	if r.alias != "" {
		return Ident(r.alias, column)
	}
	if r.kind == relationTable {
		return Col(r.name + "." + column)
	}
	if r.kind == relationTableIdent {
		base, ok := r.value.(identifierParts)
		if !ok {
			return invalidExpr("column", "invalid table identifier")
		}
		parts := slices.Concat(base, identifierParts{column})
		return Expr{kind: exprIdentifierParts, value: parts}
	}
	return invalidExpr("column", "derived or joined relation requires an alias")
}
func (r Relation) Star() Expr {
	if r.alias != "" {
		return Fragment(Ident(r.alias), UnsafeSQL(".*"))
	}
	if r.kind == relationTable {
		return Star(r.name)
	}
	if r.kind == relationTableIdent {
		return Fragment(Ident(r.value.(identifierParts)...), UnsafeSQL(".*"))
	}
	return invalidExpr("star", "derived or joined relation requires an alias")
}

func join(left, right Relation, kind joinKind, natural bool) Relation {
	return Relation{kind: relationJoin, value: &joinExpression{left: left, right: right, kind: kind, natural: natural}}
}
func InnerJoin(left, right Relation) Relation        { return join(left, right, joinInner, false) }
func LeftJoin(left, right Relation) Relation         { return join(left, right, joinLeft, false) }
func RightJoin(left, right Relation) Relation        { return join(left, right, joinRight, false) }
func FullJoin(left, right Relation) Relation         { return join(left, right, joinFull, false) }
func CrossJoin(left, right Relation) Relation        { return join(left, right, joinCross, false) }
func NaturalJoin(left, right Relation) Relation      { return join(left, right, joinInner, true) }
func NaturalLeftJoin(left, right Relation) Relation  { return join(left, right, joinLeft, true) }
func NaturalRightJoin(left, right Relation) Relation { return join(left, right, joinRight, true) }
func NaturalFullJoin(left, right Relation) Relation  { return join(left, right, joinFull, true) }
func (r Relation) On(conditions ...Condition) Relation {
	if r.kind != relationJoin {
		return RelationSQL(invalidExpr("JOIN", "ON requires a join"))
	}
	j := *r.value.(*joinExpression)
	j.on = slices.Concat(j.on, conditions)
	r.value = &j
	return r
}
func (r Relation) Using(columns ...string) Relation {
	if r.kind != relationJoin {
		return RelationSQL(invalidExpr("JOIN", "USING requires a join"))
	}
	j := *r.value.(*joinExpression)
	j.using = slices.Concat(j.using, columns)
	r.value = &j
	return r
}
func (r Relation) UsingAs(alias string) Relation {
	if r.kind != relationJoin {
		return RelationSQL(invalidExpr("JOIN", "USING alias requires a join"))
	}
	j := *r.value.(*joinExpression)
	j.usingAlias = alias
	r.value = &j
	return r
}

// RecordColumn describes a column returned by a record-valued table function.
type RecordColumn struct {
	Name string
	Type DataType
}

func Def(name string, typ DataType) RecordColumn { return RecordColumn{Name: name, Type: typ} }

// RecordFunction is a function in ROWS FROM, with optional record-column types.
type RecordFunction struct {
	expr    Expr
	columns []RecordColumn
}

func Function(expr Expr) RecordFunction { return RecordFunction{expr: expr} }
func (f RecordFunction) DefineColumns(columns ...RecordColumn) RecordFunction {
	f.columns = cloneSlice(columns)
	return f
}
func TableFunc(expr Expr) Relation { return Relation{kind: relationFunction, value: Function(expr)} }
func (r Relation) DefineColumns(columns ...RecordColumn) Relation {
	if r.kind != relationFunction {
		return RelationSQL(invalidExpr("function", "column definitions require a table function"))
	}
	f := r.value.(RecordFunction)
	f.columns = cloneSlice(columns)
	r.value = f
	return r
}
func RowsFrom(functions ...RecordFunction) Relation {
	return Relation{kind: relationRowsFrom, value: cloneSlice(functions)}
}

// FROM accepts CAST's keyword form as a function expression, but not :: casts.
func (w *renderer) functionRelationExpr(e Expr) {
	if e.kind != exprCast {
		w.expr(e)
		return
	}
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	n := e.value.(*castExpression)
	w.text("CAST(")
	w.expr(n.expr)
	w.text(" AS ")
	w.dataType(n.typ)
	w.byte(')')
}

func (w *renderer) relations(relations []Relation) {
	for i, relation := range relations {
		if i != 0 {
			w.text(", ")
		}
		w.relation(relation)
	}
}
func (w *renderer) relation(r Relation) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	physical := r.kind == relationTable || r.kind == relationTableIdent
	if !w.require(!r.only || physical, "ONLY", "requires a physical table") {
		return
	}
	if !w.require(r.sample == nil || physical, "TABLESAMPLE", "requires a physical table") {
		return
	}
	if r.lateral {
		if !w.require(r.kind == relationSubquery || r.kind == relationFunction || r.kind == relationRowsFrom || r.kind == relationJSONTable || r.kind == relationXMLTable || r.kind == relationSQL, "LATERAL", "requires a subquery or table function") {
			return
		}
		w.text("LATERAL ")
	}
	if r.only {
		w.text("ONLY ")
	}
	aliasRendered := false
	switch r.kind {
	case relationTable:
		w.identifierPath(r.name, false)
	case relationTableIdent:
		w.identifierParts(Expr{value: r.value})
	case relationSubquery:
		if !w.require(r.alias != "" || r.noAlias, "subquery", "explicit alias required; use Derived for an unaliased subquery") {
			return
		}
		if r.alias == "" {
			w.feature(PostgreSQL16, "unaliased subquery")
		}
		query, _ := r.value.(Rowset)
		width := statementWidth(query, w.options.MaxDepth)
		if width >= 0 && !w.require(len(r.columns) <= width, "subquery", "too many column aliases") {
			return
		}
		w.byte('(')
		w.statement(query)
		w.byte(')')
	case relationJoin:
		w.join(r.value.(*joinExpression))
	case relationFunction:
		f := r.value.(RecordFunction)
		if !w.require(!(len(f.columns) > 0 && (r.ordinality || len(r.columns) > 0)), "function", "use ROWS FROM for typed records with ordinality; do not combine column aliases and definitions") {
			return
		}
		w.functionRelationExpr(f.expr)
		if len(f.columns) > 0 {
			w.text(" AS ")
			if r.alias != "" {
				w.identifierPart(r.alias)
				w.byte(' ')
			}
			w.recordColumns(f.columns)
			aliasRendered = true
		}
	case relationRowsFrom:
		functions := r.value.([]RecordFunction)
		if !w.require(len(functions) > 0, "ROWS FROM", "requires functions") {
			return
		}
		w.text("ROWS FROM (")
		for i, f := range functions {
			if i != 0 {
				w.text(", ")
			}
			w.functionRelationExpr(f.expr)
			if len(f.columns) > 0 {
				w.text(" AS ")
				w.recordColumns(f.columns)
			}
		}
		w.byte(')')
	case relationSQL:
		w.expr(r.value.(Expr))
	case relationJSONTable:
		w.jsonTable(r.value.(*JSONTableBuilder))
	case relationXMLTable:
		w.xmlTable(r.value.(*XMLTableBuilder))
	default:
		w.fail(ErrInvalid, "FROM", "zero relation")
		return
	}
	if r.ordinality {
		if !w.require(r.kind == relationFunction || r.kind == relationRowsFrom, "WITH ORDINALITY", "requires a table function") {
			return
		}
		w.text(" WITH ORDINALITY")
	}
	if !aliasRendered {
		if r.alias != "" {
			w.text(" AS ")
			w.identifierPart(r.alias)
			if len(r.columns) > 0 {
				w.text(" (")
				w.names(r.columns)
				w.byte(')')
			}
		} else if len(r.columns) > 0 {
			w.fail(ErrInvalid, "alias", "column aliases require a relation alias")
		}
	}
	if r.sample != nil {
		if !w.require(r.sample.method != "" && len(r.sample.arguments) > 0, "TABLESAMPLE", "requires a method and arguments") {
			return
		}
		w.text(" TABLESAMPLE ")
		w.identifierPath(r.sample.method, false)
		w.byte('(')
		w.exprs(r.sample.arguments, ", ")
		w.byte(')')
		if r.sample.hasSeed {
			w.text(" REPEATABLE (")
			w.expr(r.sample.seed)
			w.byte(')')
		}
	}
}
func (w *renderer) join(j *joinExpression) {
	if !w.require(!(len(j.on) > 0 && len(j.using) > 0), "JOIN", "ON and USING are mutually exclusive") {
		return
	}
	if j.natural || j.kind == joinCross {
		if !w.require(len(j.on) == 0 && len(j.using) == 0, "JOIN", "NATURAL and CROSS joins cannot have ON or USING") {
			return
		}
	} else if !w.require(len(j.on) > 0 || len(j.using) > 0, "JOIN", "requires ON or USING") {
		return
	}
	w.byte('(')
	w.relation(j.left)
	if j.natural {
		w.text(" NATURAL")
	}
	switch j.kind {
	case joinInner:
		w.text(" JOIN ")
	case joinLeft:
		w.text(" LEFT JOIN ")
	case joinRight:
		w.text(" RIGHT JOIN ")
	case joinFull:
		w.text(" FULL JOIN ")
	case joinCross:
		w.text(" CROSS JOIN ")
	default:
		w.fail(ErrInvalid, "JOIN", "unknown join type")
		return
	}
	w.relation(j.right)
	if len(j.on) > 0 {
		w.text(" ON ")
		w.conditions(j.on)
	}
	if len(j.using) > 0 {
		w.text(" USING (")
		w.names(j.using)
		w.byte(')')
		if j.usingAlias != "" {
			w.feature(PostgreSQL14, "JOIN USING alias")
			w.text(" AS ")
			w.identifierPart(j.usingAlias)
		}
	} else if j.usingAlias != "" {
		w.fail(ErrInvalid, "JOIN", "USING alias requires USING columns")
	}
	w.byte(')')
}
func (w *renderer) recordColumns(columns []RecordColumn) {
	w.byte('(')
	for i, column := range columns {
		if i != 0 {
			w.text(", ")
		}
		w.identifierPart(column.Name)
		w.byte(' ')
		w.dataType(column.Type)
	}
	w.byte(')')
}

func (w *renderer) target(r Relation, insert bool) {
	if !w.require((r.kind == relationTable || r.kind == relationTableIdent) && r.sample == nil && !r.lateral && !r.ordinality && len(r.columns) == 0 && (!insert || !r.only), "target", "requires a physical table without sampling or column aliases") {
		return
	}
	w.relation(r)
}
