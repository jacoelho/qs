package qs

import (
	"slices"
	"strings"
)

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
	usingAlias string
	on         []Condition
	using      []string
	left       Relation
	right      Relation
	kind       joinKind
	natural    bool
}

// PendingJoin is a join whose qualifier has not been selected yet. Complete
// it with On, Using, or UsingAs before passing it to a relation-consuming API.
// The operands live inline so constructing a pending join does not allocate a
// join node that may never be completed.
type PendingJoin struct{ joinExpression }

type sampleClause struct {
	seed      Expr
	method    string
	arguments []Expr
	hasSeed   bool
}

// Relation is an immutable FROM item, including a nested join tree.
type Relation struct {
	value      any
	sample     *sampleClause
	name       string
	alias      string
	columns    []string
	kind       relationKind
	only       bool
	lateral    bool
	ordinality bool
	noAlias    bool
}

// Table creates a relation for a named table. The name is parsed as an
// identifier path when rendered.
func Table(name string) Relation { return Relation{kind: relationTable, name: name} }

// TableIdent creates a relation from identifier components.
func TableIdent(parts ...string) Relation {
	return Relation{kind: relationTableIdent, value: identifierParts(cloneSlice(parts))}
}

// Subquery creates an aliased FROM subquery with optional column aliases.
func Subquery(query Rowset, alias string, columns ...string) Relation {
	return Relation{kind: relationSubquery, value: query, alias: alias, columns: cloneSlice(columns)}
}

// Derived creates an unaliased FROM subquery (PostgreSQL 16+). Use As to name
// it when its columns need relation qualification.
func Derived(query Rowset) Relation {
	return Relation{kind: relationSubquery, value: query, noAlias: true}
}

// RelationSQL creates a relation from trusted SQL expressions.
func RelationSQL(parts ...Expr) Relation {
	return Relation{kind: relationSQL, value: Fragment(parts...)}
}

// As replaces a relation alias and its optional column aliases.
func (r Relation) As(alias string, columns ...string) Relation {
	r.alias = alias
	r.columns = cloneSlice(columns)
	return r
}

// Only restricts a physical table relation to the named table itself.
func (r Relation) Only() Relation { r.only = true; return r }

// Lateral marks a subquery or table-function relation as LATERAL.
func Lateral(r Relation) Relation { r.lateral = true; return r }

// WithOrdinality requests WITH ORDINALITY for a table-function relation.
func (r Relation) WithOrdinality() Relation { r.ordinality = true; return r }

// TableSample replaces a relation's TABLESAMPLE method and arguments.
func (r Relation) TableSample(method string, arguments ...Expr) Relation {
	r.sample = &sampleClause{method: method, arguments: cloneSlice(arguments)}
	return r
}

// Repeatable sets or replaces the TABLESAMPLE REPEATABLE seed.
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

// Col refers to a literal column name using the relation's alias when present.
// Dots and * in column are quoted; use Star for a relation wildcard.
func (r Relation) Col(column string) Expr {
	if r.alias != "" {
		return Ident(r.alias, column)
	}
	if r.kind == relationTable {
		parts := make(identifierParts, strings.Count(r.name, ".")+2)
		name := r.name
		for i := range parts[:len(parts)-1] {
			part, rest, found := strings.Cut(name, ".")
			if part == "" || part == "*" {
				return invalidExpr("column", "invalid table path")
			}
			parts[i] = part
			if !found {
				parts[len(parts)-1] = column
				return Expr{kind: exprIdentifierParts, value: parts}
			}
			name = rest
		}
		return Expr{kind: exprIdentifierParts, value: parts}
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

// Star references all columns exposed by the relation.
func (r Relation) Star() Expr {
	if r.alias != "" {
		return Fragment(Ident(r.alias), UnsafeSQL(".*"))
	}
	if r.kind == relationTable {
		return Star(r.name)
	}
	if r.kind == relationTableIdent {
		return Fragment(Ident(ownedPayload[identifierParts](r.value)...), UnsafeSQL(".*"))
	}
	return invalidExpr("star", "derived or joined relation requires an alias")
}

func join(left, right Relation, kind joinKind, natural bool) Relation {
	return Relation{kind: relationJoin, value: &joinExpression{left: left, right: right, kind: kind, natural: natural}}
}
func pendingJoin(left, right Relation, kind joinKind) PendingJoin {
	return PendingJoin{joinExpression{left: left, right: right, kind: kind}}
}

// InnerJoin requires qualification before it can become a FROM item or operand.
func InnerJoin(left, right Relation) PendingJoin { return pendingJoin(left, right, joinInner) }

// LeftJoin requires qualification before it can become a FROM item or operand.
func LeftJoin(left, right Relation) PendingJoin { return pendingJoin(left, right, joinLeft) }

// RightJoin requires qualification before it can become a FROM item or operand.
func RightJoin(left, right Relation) PendingJoin { return pendingJoin(left, right, joinRight) }

// FullJoin requires qualification before it can become a FROM item or operand.
func FullJoin(left, right Relation) PendingJoin { return pendingJoin(left, right, joinFull) }

// CrossJoin creates an unconditional CROSS JOIN relation.
func CrossJoin(left, right Relation) Relation { return join(left, right, joinCross, false) }

// NaturalJoin creates an INNER NATURAL JOIN relation.
func NaturalJoin(left, right Relation) Relation { return join(left, right, joinInner, true) }

// NaturalLeftJoin creates a NATURAL LEFT JOIN relation.
func NaturalLeftJoin(left, right Relation) Relation { return join(left, right, joinLeft, true) }

// NaturalRightJoin creates a NATURAL RIGHT JOIN relation.
func NaturalRightJoin(left, right Relation) Relation { return join(left, right, joinRight, true) }

// NaturalFullJoin creates a NATURAL FULL JOIN relation.
func NaturalFullJoin(left, right Relation) Relation { return join(left, right, joinFull, true) }

// On owns a nonempty qualifier list; a zero condition fails rendering.
func (p PendingJoin) On(first Condition, rest ...Condition) Relation {
	conditions := make([]Condition, 1, 1+len(rest))
	conditions[0] = first
	conditions = append(conditions, rest...)
	completed := p.joinExpression
	completed.on = conditions
	return Relation{kind: relationJoin, value: &completed}
}

// Using owns a nonempty column list. Each column is a literal identifier.
func (p PendingJoin) Using(first string, rest ...string) Relation {
	columns := make([]string, 1, 1+len(rest))
	columns[0] = first
	columns = append(columns, rest...)
	completed := p.joinExpression
	completed.using = columns
	return Relation{kind: relationJoin, value: &completed}
}

// UsingAs names the joined USING columns, independently of a whole-relation alias.
// An empty alias omits that name; nonempty aliases require PostgreSQL 14 or newer.
func (p PendingJoin) UsingAs(alias string, first string, rest ...string) Relation {
	columns := make([]string, 1, 1+len(rest))
	columns[0] = first
	columns = append(columns, rest...)
	completed := p.joinExpression
	completed.using = columns
	completed.usingAlias = alias
	return Relation{kind: relationJoin, value: &completed}
}

// RecordColumn describes a column returned by a record-valued table function.
type RecordColumn struct {
	// Name is the output column name.
	Name string
	// Type is the PostgreSQL type of the output column.
	Type DataType
}

// Def defines one output column for a record-valued table function.
func Def(name string, typ DataType) RecordColumn { return RecordColumn{Name: name, Type: typ} }

// RecordFunction is a function in ROWS FROM, with optional record-column types.
type RecordFunction struct {
	expr    Expr
	columns []RecordColumn
}

// Function wraps an expression as a function in ROWS FROM.
func Function(expr Expr) RecordFunction { return RecordFunction{expr: expr} }

// DefineColumns replaces the record columns declared for a function.
func (f RecordFunction) DefineColumns(columns ...RecordColumn) RecordFunction {
	f.columns = cloneSlice(columns)
	return f
}

// TableFunc creates a relation from a table-function expression.
func TableFunc(expr Expr) Relation { return Relation{kind: relationFunction, value: Function(expr)} }

// DefineColumns replaces the record columns of a table-function relation.
func (r Relation) DefineColumns(columns ...RecordColumn) Relation {
	if r.kind != relationFunction {
		return RelationSQL(invalidExpr("function", "column definitions require a table function"))
	}
	f := ownedPayload[RecordFunction](r.value)
	f.columns = cloneSlice(columns)
	r.value = f
	return r
}

// RowsFrom creates a relation using PostgreSQL's ROWS FROM construct.
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
	n := ownedPayload[*castExpression](e.value)
	w.text("CAST(")
	w.expr(n.expr)
	w.text(" AS ")
	w.dataType(n.typ)
	w.byte(')')
}

func (w *renderer) relations(relations []Relation) {
	if w.err != nil {
		return
	}
	for i, relation := range relations {
		if i != 0 {
			w.text(", ")
		}
		w.relation(relation)
		if w.stopped("relation", i+1) {
			return
		}
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
	aliasRendered, ok := w.relationBody(r)
	if !ok {
		return
	}
	if !w.relationOrdinality(r) {
		return
	}
	if !aliasRendered {
		w.relationAlias(r)
	}
	w.relationSample(r)
}

func (w *renderer) relationBody(r Relation) (aliasRendered, ok bool) {
	switch r.kind {
	case relationTable:
		w.identifierPath(r.name, false)
	case relationTableIdent:
		w.identifierParts(Expr{value: r.value})
	case relationSubquery:
		return false, w.subqueryRelation(r)
	case relationJoin:
		w.join(ownedPayload[*joinExpression](r.value))
	case relationFunction:
		return w.functionRelation(r)
	case relationRowsFrom:
		return false, w.rowsFromRelation(r)
	case relationSQL:
		w.expr(ownedPayload[Expr](r.value))
	case relationJSONTable:
		w.jsonTable(ownedPayload[*JSONTableBuilder](r.value))
	case relationXMLTable:
		w.xmlTable(ownedPayload[*XMLTableBuilder](r.value))
	default:
		w.fail(ErrInvalid, "FROM", "zero relation")
		return false, false
	}
	return false, true
}

func (w *renderer) subqueryRelation(r Relation) bool {
	if !w.require(r.alias != "" || r.noAlias, "subquery", "explicit alias required; use Derived for an unaliased subquery") {
		return false
	}
	if r.alias == "" {
		w.feature(PostgreSQL16, "unaliased subquery")
	}
	var query Rowset
	if r.value != nil {
		query = ownedPayload[Rowset](r.value)
	}
	if !w.require(query != nil, "subquery", "requires a query source") {
		return false
	}
	width := statementWidth(query, w.options.MaxDepth)
	if width >= 0 && !w.require(len(r.columns) <= width, "subquery", "too many column aliases") {
		return false
	}
	w.byte('(')
	w.statement(query)
	if w.stopped("subquery", 0) {
		return false
	}
	w.byte(')')
	return true
}

func (w *renderer) functionRelation(r Relation) (aliasRendered, ok bool) {
	f := ownedPayload[RecordFunction](r.value)
	if !w.require(len(f.columns) == 0 || (!r.ordinality && len(r.columns) == 0), "function", "use ROWS FROM for typed records with ordinality; do not combine column aliases and definitions") {
		return false, false
	}
	w.functionRelationExpr(f.expr)
	if len(f.columns) == 0 {
		return false, true
	}
	w.text(" AS ")
	if r.alias != "" {
		w.identifierPart(r.alias)
		w.byte(' ')
	}
	w.recordColumns(f.columns)
	return true, true
}

func (w *renderer) rowsFromRelation(r Relation) bool {
	functions := ownedPayload[[]RecordFunction](r.value)
	if !w.require(len(functions) > 0, "ROWS FROM", "requires functions") {
		return false
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
	return true
}

func (w *renderer) relationOrdinality(r Relation) bool {
	if !r.ordinality {
		return true
	}
	if !w.require(r.kind == relationFunction || r.kind == relationRowsFrom, "WITH ORDINALITY", "requires a table function") {
		return false
	}
	w.text(" WITH ORDINALITY")
	return true
}

func (w *renderer) relationAlias(r Relation) {
	if r.alias != "" {
		w.text(" AS ")
		w.identifierPart(r.alias)
		if len(r.columns) > 0 {
			w.text(" (")
			w.names(r.columns)
			w.byte(')')
		}
		return
	}
	if len(r.columns) > 0 {
		w.fail(ErrInvalid, "alias", "column aliases require a relation alias")
	}
}

func (w *renderer) relationSample(r Relation) {
	if r.sample == nil {
		return
	}
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
func (w *renderer) join(j *joinExpression) {
	if !w.require(len(j.on) == 0 || len(j.using) == 0, "JOIN", "ON and USING are mutually exclusive") {
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
	if w.stopped("left", 0) {
		return
	}
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
	if w.stopped("right", 0) {
		return
	}
	if len(j.on) > 0 {
		w.text(" ON ")
		w.conditions(j.on)
		if w.stopped("ON", 0) {
			return
		}
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
