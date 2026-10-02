package qs

import "slices"

// IndexElement is an ON CONFLICT index-inference element.
type IndexElement struct {
	collation string
	opclass   string
	expr      Expr
	column    bool
}

// IndexColumn names one column; dots are literal characters, not qualification.
func IndexColumn(column string) IndexElement { return IndexElement{expr: Ident(column), column: true} }

// IndexExpr adds an expression to ON CONFLICT index inference.
func IndexExpr(expr Expr) IndexElement { return IndexElement{expr: expr} }

// Collate sets the collation for an index-inference element.
func (e IndexElement) Collate(name string) IndexElement { e.collation = name; return e }

// OpClass sets the operator class for an index-inference element.
func (e IndexElement) OpClass(name string) IndexElement { e.opclass = name; return e }

type conflictTarget uint8

const (
	conflictAny conflictTarget = iota + 1
	conflictColumns
	conflictIndex
	conflictConstraint
)

// ConflictClause keeps the index-inference predicate (TargetWhere) separate
// from the update-action predicate (Where).
type ConflictClause struct {
	constraint  string
	columns     []string
	index       []IndexElement
	targetWhere []Condition
	updates     []Assignment
	where       []Condition
	target      conflictTarget
	doNothing   bool
}

// AnyConflict targets any conflict and can only be used with DoNothing.
func AnyConflict() ConflictClause { return ConflictClause{target: conflictAny} }

// ConflictColumns infers a conflict target from named columns.
func ConflictColumns(columns ...string) ConflictClause {
	return ConflictClause{target: conflictColumns, columns: cloneSlice(columns)}
}

// ConflictIndex infers a conflict target from index elements.
func ConflictIndex(elements ...IndexElement) ConflictClause {
	return ConflictClause{target: conflictIndex, index: cloneSlice(elements)}
}

// ConflictConstraint targets a named unique or exclusion constraint.
func ConflictConstraint(name string) ConflictClause {
	return ConflictClause{target: conflictConstraint, constraint: name}
}

// TargetWhere appends a predicate to index inference.
func (c ConflictClause) TargetWhere(conditions ...Condition) ConflictClause {
	c.targetWhere = slices.Concat(c.targetWhere, conditions)
	return c
}

// DoNothing selects the ON CONFLICT DO NOTHING action and clears assignments.
func (c ConflictClause) DoNothing() ConflictClause { c.doNothing = true; c.updates = nil; return c }

// DoUpdate selects the ON CONFLICT DO UPDATE action and replaces assignments.
func (c ConflictClause) DoUpdate(assignments ...Assignment) ConflictClause {
	c.doNothing = false
	c.updates = cloneSlice(assignments)
	return c
}

// Where appends the predicate that controls an ON CONFLICT update action.
func (c ConflictClause) Where(conditions ...Condition) ConflictClause {
	c.where = slices.Concat(c.where, conditions)
	return c
}

func (w *renderer) conflict(c ConflictClause) {
	w.text(" ON CONFLICT")
	switch c.target {
	case conflictAny:
		if !w.require(c.doNothing, "ON CONFLICT", "DO UPDATE requires an explicit conflict target") {
			return
		}
	case conflictColumns:
		if !w.require(len(c.columns) > 0, "ON CONFLICT", "requires conflict columns") {
			return
		}
		w.text(" (")
		w.names(c.columns)
		w.byte(')')
	case conflictIndex:
		if !w.require(len(c.index) > 0, "ON CONFLICT", "requires index elements") {
			return
		}
		w.text(" (")
		for i, e := range c.index {
			if i != 0 {
				w.text(", ")
			}
			if e.column {
				w.assignmentTarget(e.expr)
			} else {
				w.byte('(')
				w.expr(e.expr)
				w.byte(')')
			}
			if e.collation != "" {
				w.text(" COLLATE ")
				w.identifierPath(e.collation, false)
			}
			if e.opclass != "" {
				w.byte(' ')
				w.identifierPath(e.opclass, false)
			}
		}
		w.byte(')')
	case conflictConstraint:
		w.text(" ON CONSTRAINT ")
		w.identifierPart(c.constraint)
	default:
		w.fail(ErrInvalid, "ON CONFLICT", "zero conflict clause")
		return
	}
	if len(c.targetWhere) > 0 {
		if !w.require(c.target == conflictColumns || c.target == conflictIndex, "ON CONFLICT", "index predicate requires index inference") {
			return
		}
		w.text(" WHERE ")
		w.conditions(c.targetWhere)
	}
	if c.doNothing {
		if !w.require(len(c.where) == 0, "ON CONFLICT", "DO NOTHING has no update predicate") {
			return
		}
		w.text(" DO NOTHING")
	} else {
		w.text(" DO UPDATE SET ")
		w.assignments(c.updates)
		if len(c.where) > 0 {
			w.text(" WHERE ")
			w.conditions(c.where)
		}
	}
}
