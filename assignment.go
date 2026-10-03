package qs

import (
	"slices"
	"strings"
)

// Assignment is a scalar, composite-field, subscript or row assignment.
type Assignment struct {
	target  Expr
	value   Expr
	query   Rowset
	columns []Expr
	row     bool
}

// Set assigns a destination value to a column.
func Set(column string, value WriteValue) Assignment {
	return Assignment{target: Col(column), value: value.expr()}
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
	return Assignment{target: target, value: value.expr()}
}

// SetRow assigns destination values to a row of named columns.
func SetRow(columns []string, first WriteValue, rest ...WriteValue) Assignment {
	targets := make([]Expr, len(columns))
	for i, c := range columns {
		targets[i] = Col(c)
	}
	return Assignment{columns: targets, value: WriteRow(first, rest...).rowExpr().expr, row: true}
}

// SetRowFrom assigns the result columns of a rowset to named columns.
func SetRowFrom(columns []string, query Rowset) Assignment {
	targets := make([]Expr, len(columns))
	for i, c := range columns {
		targets[i] = Col(c)
	}
	return Assignment{columns: targets, query: query, row: true}
}

// AssignRow preserves explicit or parenthesized row syntax and supports field
// and subscript targets. Structural lists are copied; nested queries stay live.
func AssignRow(targets []Expr, values WriteRowValue) Assignment {
	return Assignment{columns: cloneSlice(targets), value: values.rowExpr().expr, row: true}
}

// AssignRowFrom assigns a rowset to explicit or parenthesized row targets.
func AssignRowFrom(targets []Expr, query Rowset) Assignment {
	return Assignment{columns: cloneSlice(targets), query: query, row: true}
}
func assignTypedTarget(column Expr, value WriteValue) Assignment {
	return Assignment{target: unqualifiedTarget(column), value: value.expr()}
}

func ownedAssignments(first Assignment, rest []Assignment) []Assignment {
	assignments := make([]Assignment, 1+len(rest))
	assignments[0] = first
	copy(assignments[1:], rest)
	return assignments
}

func appendAssignments(dst []Assignment, first Assignment, rest []Assignment) []Assignment {
	n := len(dst)
	need := 1 + len(rest)
	dst = slices.Grow(dst, need)
	dst = dst[:n+need]
	dst[n] = first
	copy(dst[n+1:], rest)
	return dst
}

func unqualifiedTarget(column Expr) Expr {
	switch column.kind {
	case exprIdentifier:
		if i := strings.LastIndexByte(column.text, '.'); i >= 0 {
			column.text = column.text[i+1:]
		}
	case exprIdentifierParts:
		parts := ownedPayload[identifierParts](column.value)
		if len(parts) > 0 {
			column = Ident(parts[len(parts)-1])
		}
	case exprField, exprSubscript:
		n := *column.node
		n.left = unqualifiedTarget(n.left)
		column.node = &n
	case exprSlice:
		n := *ownedPayload[*sliceExpression](column.value)
		n.value = unqualifiedTarget(n.value)
		column.value = &n
	default:
		column = invalidExpr("assignment", "typed field must refer to a column")
	}
	return column
}

// Excluded references the proposed row in an ON CONFLICT update.
func Excluded(column string) Expr { return Ident("excluded", column) }

// SetAllExcluded creates one assignment from each literal column name to its
// EXCLUDED value. Dots are part of the name; use Assign for field or subscript targets.
func SetAllExcluded(columns ...string) []Assignment {
	assignments := make([]Assignment, len(columns))
	for i, column := range columns {
		assignments[i] = Assign(Ident(column), Write(Excluded(column)))
	}
	return assignments
}

func sameIdentifier(a, b Expr) bool {
	if a.kind == exprIdentifier && b.kind == exprIdentifier {
		return a.text == b.text
	}
	if a.kind == exprIdentifierParts && b.kind == exprIdentifierParts {
		left, right := ownedPayload[identifierParts](a.value), ownedPayload[identifierParts](b.value)
		if len(left) != len(right) {
			return false
		}
		for i := range left {
			if left[i] != right[i] {
				return false
			}
		}
		return true
	}
	if a.kind == exprIdentifierParts && b.kind == exprIdentifier {
		a, b = b, a
	}
	if a.kind == exprIdentifier && b.kind == exprIdentifierParts {
		parts := ownedPayload[identifierParts](b.value)
		return len(parts) == 1 && !strings.Contains(a.text, ".") && parts[0] == a.text
	}
	return false
}
func assignmentWidth(a Assignment) int {
	if a.row {
		return len(a.columns)
	}
	return 1
}
func assignmentColumn(a Assignment, i int) Expr {
	if a.row {
		return a.columns[i]
	}
	return a.target
}
func (w *renderer) validateAssignments(assignments []Assignment) bool {
	if !w.require(len(assignments) > 0, "SET", "requires assignments") {
		return false
	}
	for i, a := range assignments {
		if a.row && !w.require(len(a.columns) > 0, "SET ROW", "requires columns") {
			return false
		}
		for x := range assignmentWidth(a) {
			col := assignmentColumn(a, x)
			for j := range i + 1 {
				prior := assignments[j]
				end := assignmentWidth(prior)
				if j == i {
					end = x
				}
				for y := range end {
					if sameIdentifier(col, assignmentColumn(prior, y)) {
						w.fail(ErrInvalid, "SET", "duplicate assignment target")
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
	switch e.kind {
	case exprIdentifier:
		w.identifierPath(e.text, false)
	case exprIdentifierParts:
		w.identifierParts(e)
	case exprField:
		w.assignmentTarget(e.node.left)
		w.byte('.')
		w.identifierPart(e.text)
	case exprSubscript:
		w.assignmentTarget(e.node.left)
		w.byte('[')
		w.expr(e.node.right)
		w.byte(']')
	case exprSlice:
		n := ownedPayload[*sliceExpression](e.value)
		w.assignmentTarget(n.value)
		w.byte('[')
		if n.hasLower {
			w.expr(n.lower)
		}
		w.byte(':')
		if n.hasUpper {
			w.expr(n.upper)
		}
		w.byte(']')
	case exprInvalid:
		w.expr(e)
	default:
		w.fail(ErrInvalid, "assignment", "target must be a column, subfield or subscript")
	}
}
func (w *renderer) assignments(assignments []Assignment) {
	if !w.validateAssignments(assignments) {
		return
	}
	for i, a := range assignments {
		if i != 0 {
			w.text(", ")
		}
		if a.row {
			w.byte('(')
			for j, c := range a.columns {
				if j != 0 {
					w.text(", ")
				}
				w.assignmentTarget(c)
			}
			w.text(") = ")
			if a.query != nil {
				width := statementWidth(a.query, w.options.MaxDepth)
				if width >= 0 && !w.require(width == len(a.columns), "SET ROW", "subquery projection width differs") {
					return
				}
				w.byte('(')
				w.statement(a.query)
				w.byte(')')
			} else {
				if !w.require(a.value.kind == exprList && (a.value.text == sqlRow || a.value.text == ""), "SET ROW", "requires a row value or subquery") {
					return
				}
				width := projectionWidth(ownedPayload[[]Expr](a.value.value))
				if width >= 0 && !w.require(width == len(a.columns), "SET ROW", "value count differs from target count or nil subquery") {
					return
				}
				w.writeRow(a.value)
			}
		} else {
			w.assignmentTarget(a.target)
			w.text(" = ")
			w.writeExpr(a.value)
		}
	}
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
	return versionExpression(PostgreSQL18, "OLD returning row", Ident("old", column))
}

// New references a post-mutation RETURNING column (PostgreSQL 18+).
func New(column string) Expr {
	return versionExpression(PostgreSQL18, "NEW returning row", Ident("new", column))
}

// MergeAction reports the action taken for a MERGE RETURNING row (PostgreSQL 17+).
func MergeAction() Expr { return versionedCall(PostgreSQL17, "merge_action") }

func (w *renderer) returning(expressions []Expr, aliases ReturningAliases) {
	if len(expressions) == 0 {
		if aliases.Old != "" || aliases.New != "" {
			w.fail(ErrInvalid, "RETURNING", "row aliases require a projection")
		}
		return
	}
	w.text(" RETURNING ")
	if aliases.Old != "" || aliases.New != "" {
		w.feature(PostgreSQL18, "RETURNING row aliases")
		if !w.require(aliases.Old == "" || aliases.Old != aliases.New, "RETURNING", "OLD and NEW aliases must differ") {
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
