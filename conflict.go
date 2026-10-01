package qx

import "slices"

// IndexElement is an ON CONFLICT index-inference element.
type IndexElement struct {
	expr               Expr
	column             bool
	collation, opclass string
}

// IndexColumn names one column; dots are literal characters, not qualification.
func IndexColumn(column string) IndexElement            { return IndexElement{expr: Ident(column), column: true} }
func IndexExpr(expr Expr) IndexElement                  { return IndexElement{expr: expr} }
func (e IndexElement) Collate(name string) IndexElement { e.collation = name; return e }
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
	target      conflictTarget
	columns     []string
	index       []IndexElement
	constraint  string
	targetWhere []Condition
	doNothing   bool
	updates     []Assignment
	where       []Condition
}

func AnyConflict() ConflictClause { return ConflictClause{target: conflictAny} }
func ConflictColumns(columns ...string) ConflictClause {
	return ConflictClause{target: conflictColumns, columns: cloneSlice(columns)}
}
func ConflictIndex(elements ...IndexElement) ConflictClause {
	return ConflictClause{target: conflictIndex, index: cloneSlice(elements)}
}
func ConflictConstraint(name string) ConflictClause {
	return ConflictClause{target: conflictConstraint, constraint: name}
}
func (c ConflictClause) TargetWhere(conditions ...Condition) ConflictClause {
	c.targetWhere = slices.Concat(c.targetWhere, conditions)
	return c
}
func (c ConflictClause) DoNothing() ConflictClause { c.doNothing = true; c.updates = nil; return c }
func (c ConflictClause) DoUpdate(assignments ...Assignment) ConflictClause {
	c.doNothing = false
	c.updates = cloneSlice(assignments)
	return c
}
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
