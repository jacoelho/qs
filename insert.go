package qx

type overridingMode uint8

const (
	overridingNone overridingMode = iota
	overridingSystemValue
	overridingUserValue
)

// InsertBuilder supports rows, DEFAULT VALUES, INSERT SELECT, and ordered
// assignments for a single row. These source forms are mutually exclusive.
type InsertBuilder struct {
	base       statementBase
	table      Relation
	targets    []Expr
	rows       [][]Expr
	set        []Assignment
	source     Rowset
	hasSource  bool
	defaults   bool
	overriding overridingMode
	conflict   *ConflictClause
	returning  []Expr
	aliases    ReturningAliases
}

func InsertInto(table string) *InsertBuilder              { return &InsertBuilder{table: Table(table)} }
func InsertIntoTable(table Relation) *InsertBuilder       { return &InsertBuilder{table: table} }
func (b *InsertBuilder) Into(table string) *InsertBuilder { b.table = Table(table); return b }
func (b *InsertBuilder) Columns(columns ...string) *InsertBuilder {
	for _, column := range columns {
		b.targets = append(b.targets, Ident(column))
	}
	return b
}

// Targets names columns or composite/array locations receiving each value.
// Use Columns for ordinary names; field and subscript targets use expression
// constructors and retain SQL placeholder order.
func (b *InsertBuilder) Targets(targets ...Expr) *InsertBuilder {
	b.targets = append(b.targets, targets...)
	return b
}
func (b *InsertBuilder) Values(values ...any) *InsertBuilder {
	exprs := make([]Expr, len(values))
	for i, v := range values {
		exprs[i] = parameter(v)
	}
	b.rows = append(b.rows, exprs)
	return b
}
func (b *InsertBuilder) ValuesExpr(expressions ...Expr) *InsertBuilder {
	b.rows = append(b.rows, cloneSlice(expressions))
	return b
}
func (b *InsertBuilder) Set(assignments ...Assignment) *InsertBuilder {
	b.set = append(b.set, assignments...)
	return b
}
func (b *InsertBuilder) From(source Rowset) *InsertBuilder {
	b.source = source
	b.hasSource = true
	return b
}

// DefaultValues requests a row of defaults, excluding other INSERT source forms.
func (b *InsertBuilder) DefaultValues() *InsertBuilder { b.defaults = true; return b }

// OverridingSystemValue permits explicit values for GENERATED ALWAYS identities.
func (b *InsertBuilder) OverridingSystemValue() *InsertBuilder {
	b.overriding = overridingSystemValue
	return b
}

// OverridingUserValue ignores supplied identity values and generates replacements.
func (b *InsertBuilder) OverridingUserValue() *InsertBuilder {
	b.overriding = overridingUserValue
	return b
}
func (b *InsertBuilder) OnConflict(conflict ConflictClause) *InsertBuilder {
	b.conflict = &conflict
	return b
}
func (b *InsertBuilder) Returning(expressions ...Expr) *InsertBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}
func (b *InsertBuilder) ReturningCols(columns ...string) *InsertBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}
func (b *InsertBuilder) ReturningRows(aliases ReturningAliases) *InsertBuilder {
	b.aliases = aliases
	return b
}

func (b *InsertBuilder) append(w *renderer) {
	modes := 0
	if len(b.rows) > 0 {
		modes++
	}
	if len(b.set) > 0 {
		modes++
	}
	if b.hasSource {
		modes++
	}
	if b.defaults {
		modes++
	}
	if !w.require(modes == 1, "INSERT", "requires exactly one of VALUES, assignments, SELECT or DEFAULT VALUES") {
		return
	}
	if !w.require(!(len(b.set) > 0 && len(b.targets) > 0), "INSERT", "assignments already define the target columns") {
		return
	}
	if !w.require(!b.defaults || b.overriding == overridingNone, "INSERT", "DEFAULT VALUES cannot specify OVERRIDING") {
		return
	}
	for i, c := range b.targets {
		for j := range i {
			if sameIdentifier(b.targets[j], c) {
				w.fail(ErrInvalid, "INSERT", "duplicate target column")
				return
			}
		}
	}
	if len(b.set) > 0 && !w.validateAssignments(b.set) {
		return
	}
	w.head(b.base)
	w.text("INSERT INTO ")
	w.target(b.table, true)
	if len(b.targets) > 0 {
		w.text(" (")
		for i, target := range b.targets {
			if i > 0 {
				w.text(", ")
			}
			w.assignmentTarget(target)
		}
		w.byte(')')
	}
	if len(b.set) > 0 {
		w.text(" (")
		for i, a := range b.set {
			if !w.require(!a.row, "INSERT SET", "tuple assignments are not supported in single-row insert assignments") {
				return
			}
			if i != 0 {
				w.text(", ")
			}
			w.assignmentTarget(a.target)
		}
		w.byte(')')
	}
	w.overriding(b.overriding)
	w.byte(' ')
	switch {
	case len(b.rows) > 0:
		if len(b.targets) > 0 {
			for _, row := range b.rows {
				width := projectionWidth(row)
				if width >= 0 && !w.require(width == len(b.targets), "INSERT", "value count differs from target columns") {
					return
				}
			}
		}
		w.values(b.rows, true)
	case len(b.set) > 0:
		w.text("VALUES (")
		for i, a := range b.set {
			if i != 0 {
				w.text(", ")
			}
			w.expr(a.value)
		}
		w.byte(')')
	case b.hasSource:
		width := statementWidth(b.source, w.options.MaxDepth)
		if width >= 0 && len(b.targets) > 0 && !w.require(width == len(b.targets), "INSERT SELECT", "projection width differs from target columns") {
			return
		}
		w.statement(b.source)
	case b.defaults:
		w.text("DEFAULT VALUES")
	}
	if b.conflict != nil {
		w.conflict(*b.conflict)
	}
	w.returning(b.returning, b.aliases)
	w.foot(b.base)
}
func (w *renderer) overriding(mode overridingMode) {
	switch mode {
	case overridingNone:
	case overridingSystemValue:
		w.text(" OVERRIDING SYSTEM VALUE")
	case overridingUserValue:
		w.text(" OVERRIDING USER VALUE")
	default:
		w.fail(ErrInvalid, "OVERRIDING", "unknown mode")
	}
}
func (b *InsertBuilder) Reset() { *b = InsertBuilder{} }
