package qs

import (
	"slices"
)

// Assignment is a scalar, composite-field, subscript or row assignment.
type Assignment struct {
	target  Expr   // assignColumn
	value   Expr   // assignColumn and assignRowValues
	query   Rowset // assignRowQuery
	columns []Expr // assignRowValues and assignRowQuery
	form    assignmentForm
}

// assignmentForm is which of the three SET grammars an Assignment renders. The
// constructor that chooses it sets it; each form reads only the fields marked
// with it above. The zero form is the zero Assignment, which fails rendering.
type assignmentForm uint8

const (
	// assignColumn is target = value.
	assignColumn assignmentForm = iota + 1
	// assignRowValues is (columns) = row value.
	assignRowValues
	// assignRowQuery is (columns) = (query).
	assignRowQuery
)

// Set assigns a destination value to a column.
func Set(column string, value WriteValue) Assignment {
	return Assignment{form: assignColumn, target: unqualifiedColumn(column), value: value.expr()}
}

// SetNull assigns SQL NULL to a column.
func SetNull(column string) Assignment { return Set(column, Write(NullLiteral())) }

// SetDefault assigns SQL DEFAULT to a column.
func SetDefault(column string) Assignment { return Set(column, Default()) }

// SetNullable assigns a typed nullable value as a bound parameter.
func SetNullable[T any](column string, value Null[T]) Assignment {
	return Set(column, Write(ParamNull(value)))
}

// Assign permits explicit composite-field and subscript targets.
func Assign(target Expr, value WriteValue) Assignment {
	return Assignment{form: assignColumn, target: target, value: value.expr()}
}

// SetRow assigns destination values to a row of named columns.
func SetRow(columns []string, first WriteValue, rest ...WriteValue) Assignment {
	targets := make([]Expr, len(columns))
	for i, c := range columns {
		targets[i] = unqualifiedColumn(c)
	}
	return Assignment{form: assignRowValues, columns: targets, value: WriteRow(first, rest...).rowExpr().expr}
}

// SetRowFrom assigns the result columns of a rowset to named columns.
func SetRowFrom(columns []string, query Rowset) Assignment {
	targets := make([]Expr, len(columns))
	for i, c := range columns {
		targets[i] = unqualifiedColumn(c)
	}
	return Assignment{form: assignRowQuery, columns: targets, query: ownedRowset(query)}
}

// AssignRow preserves explicit or parenthesized row syntax and supports field
// and subscript targets. The target list is copied.
func AssignRow(targets []Expr, values WriteRowValue) Assignment {
	return Assignment{form: assignRowValues, columns: cloneSlice(targets), value: values.rowExpr().expr}
}

// AssignRowFrom assigns a rowset to explicit or parenthesized row targets.
func AssignRowFrom(targets []Expr, query Rowset) Assignment {
	return Assignment{form: assignRowQuery, columns: cloneSlice(targets), query: ownedRowset(query)}
}
func assignTypedTarget(column Expr, value WriteValue) Assignment {
	return Assignment{form: assignColumn, target: unqualifiedTarget(column), value: value.expr()}
}

func unqualifiedTarget(target Expr) Expr {
	if target.kind == exprIdentifier {
		return target
	}
	if target.kind == exprQualified {
		return unqualifiedColumn(target.text)
	}
	switch n := target.node().(type) {
	case *qualifiedIdentifier:
		return unqualifiedColumn(n.parts[len(n.parts)-1])
	case *fieldExpression:
		return nodeExpr(&fieldExpression{base: unqualifiedTarget(n.base), name: n.name})
	case *subscriptExpression:
		return nodeExpr(&subscriptExpression{base: unqualifiedTarget(n.base), index: n.index})
	case *sliceExpression:
		slice := *n
		slice.base = unqualifiedTarget(n.base)
		return nodeExpr(&slice)
	default:
		return invalidExpr("assignment", "typed field must refer to a column")
	}
}

// Excluded references the proposed row in an ON CONFLICT update.
func Excluded(column string) Expr { return Col("excluded", column) }

// SetAllExcluded creates one assignment from each literal column name to its
// EXCLUDED value. Dots are part of the name; use Assign for field or subscript targets.
func SetAllExcluded(columns ...string) []Assignment {
	assignments := make([]Assignment, len(columns))
	for i, column := range columns {
		assignments[i] = Assign(unqualifiedColumn(column), Write(Excluded(column)))
	}
	return assignments
}

// sameIdentifier reports whether a and b name the same column. A qualified
// name has at least two parts, so it never equals an unqualified one.
func sameIdentifier(a, b Expr) bool {
	if a.kind != exprNode || b.kind != exprNode {
		return a.kind == b.kind && (a.kind == exprIdentifier || a.kind == exprQualified) &&
			sameName(a.text, b.text) && sameName(a.qualifier(), b.qualifier())
	}
	left, ok := a.node().(*qualifiedIdentifier)
	if !ok {
		return false
	}
	right, ok := b.node().(*qualifiedIdentifier)
	return ok && slices.EqualFunc(left.parts, right.parts, sameName)
}

// width is how many targets a assigns: one for a column, the target list for a row.
func (a Assignment) width() int {
	if a.form == assignColumn {
		return 1
	}
	return len(a.columns)
}

// targetAt returns the ith of a's width targets.
func (a Assignment) targetAt(i int) Expr {
	if a.form == assignColumn {
		return a.target
	}
	return a.columns[i]
}

// validateAssignments checks a SET list before any consumer reads it: every
// assignment has a known form, a row names its targets, and no target repeats.
// INSERT and MERGE INSERT rely on it to reject the zero Assignment before they
// test for the column form.
func (w *renderer) validateAssignments(assignments []Assignment) bool {
	if !w.require(len(assignments) > 0, "SET", "requires assignments") {
		return false
	}
	for i, a := range assignments {
		var ok bool
		switch a.form {
		case assignColumn:
			ok = true
		case assignRowValues, assignRowQuery:
			ok = w.require(len(a.columns) > 0, "SET ROW", "requires columns")
		default:
			ok = w.require(false, "SET", "zero or unknown assignment")
		}
		if !ok {
			w.errorPath("assignment", i+1)
			return false
		}
		for x := range a.width() {
			col := a.targetAt(x)
			for j := range i + 1 {
				prior := assignments[j]
				end := prior.width()
				if j == i {
					end = x
				}
				for y := range end {
					if sameIdentifier(col, prior.targetAt(y)) {
						w.fail(ErrInvalid, "SET", "duplicate assignment target")
						w.errorPath("assignment", i+1)
						return false
					}
				}
			}
		}
	}
	return true
}
func (w *renderer) assignmentTarget(e Expr) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	if e.kind == exprIdentifier || e.kind == exprQualified {
		w.expr(e)
		return
	}
	switch n := e.node().(type) {
	case *qualifiedIdentifier:
		w.qualifiedName(n.parts, "identifier")
	case *fieldExpression:
		w.assignmentTarget(n.base)
		w.byte('.')
		w.identifierPart(n.name)
	case *subscriptExpression:
		w.assignmentTarget(n.base)
		w.byte('[')
		w.expr(n.index)
		w.byte(']')
	case *sliceExpression:
		w.assignmentTarget(n.base)
		w.byte('[')
		if n.hasLower {
			w.expr(n.lower)
		}
		w.byte(':')
		if n.hasUpper {
			w.expr(n.upper)
		}
		w.byte(']')
	case *invalidExpression:
		w.expr(e)
	default:
		if e.kind == exprZero {
			w.expr(e)
			return
		}
		w.fail(ErrInvalid, "assignment", "target must be a column, subfield or subscript")
	}
}
func (w *renderer) assignments(assignments []Assignment) {
	if w.err != nil {
		return
	}
	if !w.validateAssignments(assignments) {
		return
	}
	for i := range assignments {
		if i != 0 {
			w.text(", ")
		}
		w.assignment(&assignments[i])
		if w.stopped("assignment", i+1) {
			return
		}
	}
}

func (w *renderer) assignment(a *Assignment) {
	switch a.form {
	case assignColumn:
		w.assignmentTarget(a.target)
		if w.stopped("target", 0) {
			return
		}
		w.text(" = ")
		w.writeExpr(a.value)
		if w.stopped("value", 0) {
			return
		}
	case assignRowValues:
		row, ok := rowList(a.value)
		if !w.require(ok, "SET ROW", "requires a row value") {
			return
		}
		width := projectionWidth(row.items)
		if width >= 0 && !w.require(width == len(a.columns), "SET ROW", "value count differs from target count") {
			return
		}
		if !w.rowTargets(a.columns) {
			return
		}
		w.writeRow(a.value)
		if w.stopped("value", 0) {
			return
		}
	case assignRowQuery:
		if !w.require(a.query != nil, "SET ROW", "requires a subquery") {
			return
		}
		width := statementWidth(a.query, w.options.MaxDepth)
		if width >= 0 && !w.require(width == len(a.columns), "SET ROW", "subquery projection width differs") {
			return
		}
		if !w.rowTargets(a.columns) {
			return
		}
		w.byte('(')
		w.statement(a.query)
		if w.stopped("subquery", 0) {
			return
		}
		w.byte(')')
	default:
		w.fail(ErrInvalid, "SET", "zero or unknown assignment")
	}
}

// rowTargets writes the parenthesized target list of a row assignment and its
// " = ", and reports whether it succeeded.
func (w *renderer) rowTargets(columns []Expr) bool {
	w.byte('(')
	for j, c := range columns {
		if j != 0 {
			w.text(", ")
		}
		w.assignmentTarget(c)
		if w.stopped("target", j+1) {
			return false
		}
	}
	w.text(") = ")
	return true
}

// ReturningAliases names OLD and NEW output rows (PostgreSQL 18+).
type ReturningAliases struct {
	// Old is the alias for the row before the mutation.
	Old string
	// New is the alias for the row after the mutation.
	New string
}

// Old references a pre-mutation RETURNING column (PostgreSQL 18+).
func Old(column string) Expr {
	return Col("old", column)
}

// New references a post-mutation RETURNING column (PostgreSQL 18+).
func New(column string) Expr {
	return Col("new", column)
}

// MergeAction reports the action taken for a MERGE RETURNING row (PostgreSQL 17+).
func MergeAction() Expr { return builtin("merge_action") }

func (w *renderer) returning(expressions []Expr, aliases ReturningAliases) {
	if w.err != nil {
		return
	}
	if len(expressions) == 0 {
		if aliases.Old != "" || aliases.New != "" {
			w.fail(ErrInvalid, "RETURNING", "row aliases require a projection")
		}
		return
	}
	w.text(" RETURNING ")
	if aliases.Old != "" || aliases.New != "" {
		if !w.require(aliases.Old == "" || !sameName(aliases.Old, aliases.New), "RETURNING", "OLD and NEW aliases must differ") {
			return
		}
		w.text("WITH (")
		if aliases.Old != "" {
			w.text("OLD AS ")
			w.identifierPart(aliases.Old)
		}
		if aliases.New != "" {
			if aliases.Old != "" {
				w.text(", ")
			}
			w.text("NEW AS ")
			w.identifierPart(aliases.New)
		}
		w.text(") ")
	}
	w.exprs(expressions, ", ")
}
