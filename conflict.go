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

type conflictClause struct {
	constraint  string
	columns     []string
	index       []IndexElement
	targetWhere []Condition
	updates     []Assignment
	where       []Condition
	target      conflictTarget
	doNothing   bool
}

// AnyConflictTarget targets any conflict and can only be completed with
// DoNothing.
type AnyConflictTarget struct{ anyTarget conflictClause }

// ConflictColumnsTarget infers a conflict target from named columns.
type ConflictColumnsTarget struct{ columnsTarget conflictClause }

// ConflictIndexTarget infers a conflict target from index elements.
type ConflictIndexTarget struct{ indexTarget conflictClause }

// ConflictConstraintTarget targets a named unique or exclusion constraint.
type ConflictConstraintTarget struct{ constraintTarget conflictClause }

// ConflictNothing is a completed ON CONFLICT DO NOTHING action.
type ConflictNothing struct{ nothing conflictClause }

// ConflictUpdate is a completed ON CONFLICT DO UPDATE action.
type ConflictUpdate struct{ update conflictClause }

// conflictAction closes the only two accepted ON CONFLICT action types. Its
// union is kept private so callers cannot supply a different action wrapper.
type conflictAction interface {
	conflictInto(base *insertBase)
	ConflictNothing | ConflictUpdate
}

func (c ConflictNothing) conflictInto(base *insertBase) {
	clause := c.nothing
	base.conflict = &clause
}

func (c ConflictUpdate) conflictInto(base *insertBase) {
	clause := c.update
	base.conflict = &clause
}

// setInsertConflict normalizes either completed action without retaining an
// interface value in the builder.
func setInsertConflict[C conflictAction](base *insertBase, action C) {
	action.conflictInto(base)
}

// AnyConflict starts an unrestricted ON CONFLICT target.
func AnyConflict() AnyConflictTarget {
	return AnyConflictTarget{anyTarget: conflictClause{target: conflictAny}}
}

// ConflictColumns starts inference from named columns.
func ConflictColumns(first string, rest ...string) ConflictColumnsTarget {
	columns := make([]string, 1+len(rest))
	columns[0] = first
	copy(columns[1:], rest)
	return ConflictColumnsTarget{columnsTarget: conflictClause{
		target:  conflictColumns,
		columns: columns,
	}}
}

// ConflictIndex starts inference from index elements.
func ConflictIndex(first IndexElement, rest ...IndexElement) ConflictIndexTarget {
	elements := make([]IndexElement, 1+len(rest))
	elements[0] = first
	copy(elements[1:], rest)
	return ConflictIndexTarget{indexTarget: conflictClause{
		target: conflictIndex,
		index:  elements,
	}}
}

// ConflictConstraint starts a constraint-named ON CONFLICT target.
func ConflictConstraint(name string) ConflictConstraintTarget {
	return ConflictConstraintTarget{constraintTarget: conflictClause{
		target:     conflictConstraint,
		constraint: name,
	}}
}

// TargetWhere appends a predicate to column inference.
func (c ConflictColumnsTarget) TargetWhere(conditions ...Condition) ConflictColumnsTarget {
	c.columnsTarget.targetWhere = slices.Concat(c.columnsTarget.targetWhere, conditions)
	return c
}

// TargetWhere appends a predicate to index inference.
func (c ConflictIndexTarget) TargetWhere(conditions ...Condition) ConflictIndexTarget {
	c.indexTarget.targetWhere = slices.Concat(c.indexTarget.targetWhere, conditions)
	return c
}

// DoNothing completes an unrestricted conflict target.
func (c AnyConflictTarget) DoNothing() ConflictNothing {
	c.anyTarget.doNothing = true
	return ConflictNothing{nothing: c.anyTarget}
}

// DoNothing completes a column-inference target.
func (c ConflictColumnsTarget) DoNothing() ConflictNothing {
	c.columnsTarget.doNothing = true
	return ConflictNothing{nothing: c.columnsTarget}
}

// DoUpdate completes a column-inference target with a non-empty assignment list.
func (c ConflictColumnsTarget) DoUpdate(first Assignment, rest ...Assignment) ConflictUpdate {
	c.columnsTarget.doNothing = false
	c.columnsTarget.updates = ownedAssignments(first, rest)
	return ConflictUpdate{update: c.columnsTarget}
}

// DoUpdateSlice completes a column-inference target with a copy of assignments.
// Nil or empty slices fail rendering with ErrInvalid. Nested queries remain live
// and bound application values are shallow-copied.
func (c ConflictColumnsTarget) DoUpdateSlice(assignments []Assignment) ConflictUpdate {
	c.columnsTarget.doNothing = false
	c.columnsTarget.updates = cloneSlice(assignments)
	return ConflictUpdate{update: c.columnsTarget}
}

// DoNothing completes an index-inference target.
func (c ConflictIndexTarget) DoNothing() ConflictNothing {
	c.indexTarget.doNothing = true
	return ConflictNothing{nothing: c.indexTarget}
}

// DoUpdate completes an index-inference target with a non-empty assignment list.
func (c ConflictIndexTarget) DoUpdate(first Assignment, rest ...Assignment) ConflictUpdate {
	c.indexTarget.doNothing = false
	c.indexTarget.updates = ownedAssignments(first, rest)
	return ConflictUpdate{update: c.indexTarget}
}

// DoUpdateSlice completes an index-inference target with a copy of assignments.
// Nil or empty slices fail rendering with ErrInvalid. Nested queries remain live
// and bound application values are shallow-copied.
func (c ConflictIndexTarget) DoUpdateSlice(assignments []Assignment) ConflictUpdate {
	c.indexTarget.doNothing = false
	c.indexTarget.updates = cloneSlice(assignments)
	return ConflictUpdate{update: c.indexTarget}
}

// DoNothing completes a constraint target.
func (c ConflictConstraintTarget) DoNothing() ConflictNothing {
	c.constraintTarget.doNothing = true
	return ConflictNothing{nothing: c.constraintTarget}
}

// DoUpdate completes a constraint target with a non-empty assignment list.
func (c ConflictConstraintTarget) DoUpdate(first Assignment, rest ...Assignment) ConflictUpdate {
	c.constraintTarget.doNothing = false
	c.constraintTarget.updates = ownedAssignments(first, rest)
	return ConflictUpdate{update: c.constraintTarget}
}

// DoUpdateSlice completes a constraint target with a copy of assignments.
// Nil or empty slices fail rendering with ErrInvalid. Nested queries remain live
// and bound application values are shallow-copied.
func (c ConflictConstraintTarget) DoUpdateSlice(assignments []Assignment) ConflictUpdate {
	c.constraintTarget.doNothing = false
	c.constraintTarget.updates = cloneSlice(assignments)
	return ConflictUpdate{update: c.constraintTarget}
}

// Where appends the predicate controlling an ON CONFLICT update action.
func (c ConflictUpdate) Where(conditions ...Condition) ConflictUpdate {
	c.update.where = slices.Concat(c.update.where, conditions)
	return c
}

func (w *renderer) conflict(c conflictClause) {
	if w.err != nil {
		return
	}
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
			if w.stopped("index", i+1) {
				return
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
	if w.stopped("target", 0) {
		return
	}
	if len(c.targetWhere) > 0 {
		if !w.require(c.target == conflictColumns || c.target == conflictIndex, "ON CONFLICT", "index predicate requires index inference") {
			return
		}
		w.text(" WHERE ")
		w.conditions(c.targetWhere)
		if w.stopped("target predicate", 0) {
			return
		}
	}
	if c.doNothing {
		w.text(" DO NOTHING")
	} else {
		w.text(" DO UPDATE SET ")
		w.assignments(c.updates)
		if w.stopped("SET", 0) {
			return
		}
		if len(c.where) > 0 {
			w.text(" WHERE ")
			w.conditions(c.where)
			if w.stopped("WHERE", 0) {
				return
			}
		}
	}
}
