package qs

import "slices"

type matchKind uint8

const (
	matchMatched matchKind = iota + 1
	matchNotMatched
	matchNotMatchedBySource
)

type mergeAction uint8

const (
	mergeUpdate mergeAction = iota + 1
	mergeDelete
	mergeInsert
	mergeNothing
)

// MergeWhen is an immutable, ordered MERGE branch.
type MergeWhen struct {
	kind       matchKind
	byTarget   bool
	conditions []Condition
	action     mergeAction
	set        []Assignment
	columns    []string
	values     []Expr
	defaults   bool
	overriding overridingMode
}

// Selecting an action replaces any previously selected action and its values.
func (m MergeWhen) withAction(action mergeAction) MergeWhen {
	m.action = action
	m.set = nil
	m.columns = nil
	m.values = nil
	m.defaults = false
	return m
}

func Matched() MergeWhen            { return MergeWhen{kind: matchMatched} }
func NotMatched() MergeWhen         { return MergeWhen{kind: matchNotMatched} }
func NotMatchedByTarget() MergeWhen { return MergeWhen{kind: matchNotMatched, byTarget: true} }
func NotMatchedBySource() MergeWhen { return MergeWhen{kind: matchNotMatchedBySource} }
func (m MergeWhen) And(conditions ...Condition) MergeWhen {
	m.conditions = slices.Concat(m.conditions, conditions)
	return m
}
func (m MergeWhen) ThenUpdate(assignments ...Assignment) MergeWhen {
	m = m.withAction(mergeUpdate)
	m.set = cloneSlice(assignments)
	return m
}
func (m MergeWhen) ThenDelete() MergeWhen    { m = m.withAction(mergeDelete); return m }
func (m MergeWhen) ThenDoNothing() MergeWhen { m = m.withAction(mergeNothing); return m }
func (m MergeWhen) ThenInsert(assignments ...Assignment) MergeWhen {
	m = m.withAction(mergeInsert)
	m.set = cloneSlice(assignments)
	return m
}
func (m MergeWhen) ThenInsertValues(columns []string, values ...Expr) MergeWhen {
	m = m.withAction(mergeInsert)
	m.columns = cloneSlice(columns)
	m.values = cloneSlice(values)
	return m
}
func (m MergeWhen) ThenInsertDefault() MergeWhen {
	m = m.withAction(mergeInsert)
	m.defaults = true
	return m
}

// OverridingSystemValue permits explicit GENERATED ALWAYS identities for INSERT.
func (m MergeWhen) OverridingSystemValue() MergeWhen { m.overriding = overridingSystemValue; return m }

// OverridingUserValue generates identities instead of using supplied INSERT values.
func (m MergeWhen) OverridingUserValue() MergeWhen { m.overriding = overridingUserValue; return m }

type MergeBuilder struct {
	base           statementBase
	target, source Relation
	on             []Condition
	branches       []MergeWhen
	returning      []Expr
	aliases        ReturningAliases
}

func MergeInto(table string) *MergeBuilder                  { return &MergeBuilder{target: Table(table)} }
func MergeIntoTable(table Relation) *MergeBuilder           { return &MergeBuilder{target: table} }
func (b *MergeBuilder) Using(source Relation) *MergeBuilder { b.source = source; return b }
func (b *MergeBuilder) On(conditions ...Condition) *MergeBuilder {
	b.on = append(b.on, conditions...)
	return b
}
func (b *MergeBuilder) When(branches ...MergeWhen) *MergeBuilder {
	b.branches = append(b.branches, branches...)
	return b
}
func (b *MergeBuilder) Returning(expressions ...Expr) *MergeBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}
func (b *MergeBuilder) ReturningCols(columns ...string) *MergeBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}
func (b *MergeBuilder) ReturningRows(aliases ReturningAliases) *MergeBuilder {
	b.aliases = aliases
	return b
}

func (b *MergeBuilder) append(w *renderer) {
	w.feature(PostgreSQL15, "MERGE")
	if !w.require(len(b.on) > 0 && len(b.branches) > 0, "MERGE", "requires ON and at least one WHEN branch") {
		return
	}
	if !w.require(!b.base.recursive, "MERGE", "WITH RECURSIVE is not supported") {
		return
	}
	w.head(b.base)
	w.text("MERGE INTO ")
	w.target(b.target, false)
	w.text(" USING ")
	w.relation(b.source)
	w.text(" ON ")
	w.conditions(b.on)
	for i, m := range b.branches {
		for j := range i {
			if b.branches[j].kind == m.kind && len(b.branches[j].conditions) == 0 {
				w.fail(ErrInvalid, "MERGE WHEN", "branch follows an unconditional branch of the same category")
				return
			}
		}
		w.mergeBranch(m)
	}
	if len(b.returning) > 0 {
		w.feature(PostgreSQL17, "MERGE RETURNING")
	}
	w.returning(b.returning, b.aliases)
	w.foot(b.base)
}
func (w *renderer) mergeBranch(m MergeWhen) {
	w.text(" WHEN ")
	switch m.kind {
	case matchMatched:
		w.text("MATCHED")
	case matchNotMatched:
		w.text("NOT MATCHED")
		if m.byTarget {
			w.feature(PostgreSQL17, "MERGE BY TARGET")
			w.text(" BY TARGET")
		}
	case matchNotMatchedBySource:
		w.feature(PostgreSQL17, "MERGE BY SOURCE")
		w.text("NOT MATCHED BY SOURCE")
	default:
		w.fail(ErrInvalid, "MERGE WHEN", "zero or unknown match category")
		return
	}
	if len(m.conditions) > 0 {
		w.text(" AND ")
		w.conditions(m.conditions)
	}
	w.text(" THEN ")
	if m.action == mergeInsert {
		if !w.require(m.kind == matchNotMatched, "MERGE WHEN", "INSERT requires NOT MATCHED [BY TARGET]") {
			return
		}
	} else if m.action == mergeUpdate || m.action == mergeDelete {
		if !w.require(m.kind != matchNotMatched, "MERGE WHEN", "UPDATE and DELETE require a target row") {
			return
		}
	}
	if m.action != mergeInsert && !w.require(m.overriding == overridingNone, "MERGE WHEN", "OVERRIDING requires INSERT") {
		return
	}
	switch m.action {
	case mergeUpdate:
		w.text("UPDATE SET ")
		w.assignments(m.set)
	case mergeDelete:
		w.text("DELETE")
	case mergeNothing:
		w.text("DO NOTHING")
	case mergeInsert:
		for i, column := range m.columns {
			for j := range i {
				if column == m.columns[j] {
					w.fail(ErrInvalid, "MERGE INSERT", "duplicate target column")
					return
				}
			}
		}
		modes := 0
		if len(m.set) > 0 {
			modes++
		}
		if len(m.values) > 0 {
			modes++
		}
		if m.defaults {
			modes++
		}
		if !w.require(modes == 1, "MERGE INSERT", "requires one insert source") {
			return
		}
		w.text("INSERT")
		if len(m.set) > 0 {
			if !w.validateAssignments(m.set) {
				return
			}
			w.text(" (")
			for i, a := range m.set {
				if !w.require(!a.row, "MERGE INSERT", "tuple assignments cannot define insert columns") {
					return
				}
				if i != 0 {
					w.text(", ")
				}
				w.assignmentTarget(a.target)
			}
			w.byte(')')
		} else if len(m.columns) > 0 {
			w.text(" (")
			w.names(m.columns)
			w.byte(')')
		}
		if m.defaults {
			if !w.require(m.overriding == overridingNone, "MERGE INSERT", "DEFAULT VALUES cannot specify OVERRIDING") {
				return
			}
			w.text(" DEFAULT VALUES")
			return
		}
		w.overriding(m.overriding)
		w.text(" VALUES (")
		if len(m.set) > 0 {
			for i, a := range m.set {
				if i != 0 {
					w.text(", ")
				}
				w.expr(a.value)
			}
		} else {
			if len(m.columns) > 0 && !w.require(len(m.columns) == len(m.values), "MERGE INSERT", "column and value counts differ") {
				return
			}
			w.exprs(m.values, ", ")
		}
		w.byte(')')
	default:
		w.fail(ErrInvalid, "MERGE WHEN", "missing action")
	}
}
func (b *MergeBuilder) Reset() { *b = MergeBuilder{} }
