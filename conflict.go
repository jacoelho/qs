package qs

import "slices"

// IndexElement is an ON CONFLICT index-inference element.
type IndexElement struct {
	collation identifierParts
	opclass   identifierParts
	expr      Expr
	column    bool
}

// IndexColumn names one column; dots are literal characters, not qualification.
func IndexColumn(column string) IndexElement {
	return IndexElement{expr: unqualifiedColumn(column), column: true}
}

// IndexExpr adds an expression to ON CONFLICT index inference.
func IndexExpr(expr Expr) IndexElement { return IndexElement{expr: expr} }

// Collate sets the collation for an index-inference element. Each argument is
// one identifier.
func (e IndexElement) Collate(first string, rest ...string) IndexElement {
	e.collation = qualifiedName(first, rest)
	return e
}

// OpClass sets the operator class for an index-inference element.
func (e IndexElement) OpClass(first string, rest ...string) IndexElement {
	e.opclass = qualifiedName(first, rest)
	return e
}

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
func ConflictColumns[T string | []string](first T, rest ...string) ConflictColumnsTarget {
	columns := gather(first, rest)
	return ConflictColumnsTarget{columnsTarget: conflictClause{
		target:  conflictColumns,
		columns: columns,
	}}
}

// ConflictIndex starts inference from index elements.
func ConflictIndex[T IndexElement | []IndexElement](first T, rest ...IndexElement) ConflictIndexTarget {
	elements := gather(first, rest)
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

// DoUpdate completes a column-inference target. first is one assignment or a list built at
// run time, which is copied; an empty list fails rendering with ErrInvalid.
func (c ConflictColumnsTarget) DoUpdate[T Assignment | []Assignment](first T, rest ...Assignment) ConflictUpdate {
	c.columnsTarget.doNothing = false
	c.columnsTarget.updates = gather(first, rest)
	return ConflictUpdate{update: c.columnsTarget}
}

// DoNothing completes an index-inference target.
func (c ConflictIndexTarget) DoNothing() ConflictNothing {
	c.indexTarget.doNothing = true
	return ConflictNothing{nothing: c.indexTarget}
}

// DoUpdate completes an index-inference target. first is one assignment or a list built at
// run time, which is copied; an empty list fails rendering with ErrInvalid.
func (c ConflictIndexTarget) DoUpdate[T Assignment | []Assignment](first T, rest ...Assignment) ConflictUpdate {
	c.indexTarget.doNothing = false
	c.indexTarget.updates = gather(first, rest)
	return ConflictUpdate{update: c.indexTarget}
}

// DoNothing completes a constraint target.
func (c ConflictConstraintTarget) DoNothing() ConflictNothing {
	c.constraintTarget.doNothing = true
	return ConflictNothing{nothing: c.constraintTarget}
}

// DoUpdate completes a constraint target. first is one assignment or a list built at
// run time, which is copied; an empty list fails rendering with ErrInvalid.
func (c ConflictConstraintTarget) DoUpdate[T Assignment | []Assignment](first T, rest ...Assignment) ConflictUpdate {
	c.constraintTarget.doNothing = false
	c.constraintTarget.updates = gather(first, rest)
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
			if len(e.collation) != 0 {
				w.text(" COLLATE ")
				w.qualifiedName(e.collation, "COLLATE")
			}
			if len(e.opclass) != 0 {
				w.byte(' ')
				w.qualifiedName(e.opclass, "operator class")
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
