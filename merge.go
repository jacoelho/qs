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

// MergeWhen is a completed, immutable MERGE branch. Its zero value is invalid.
type MergeWhen struct {
	conditions []Condition
	set        []Assignment
	columns    []string
	values     []WriteValue
	kind       matchKind
	byTarget   bool
	action     mergeAction
	defaults   bool
	overriding overridingMode
}

// mergeMatch is the shared immutable selector payload. The exported wrappers
// use distinct field names so callers cannot explicitly convert one role into
// another; byTarget is meaningful only for the NOT MATCHED role.
type mergeMatch struct {
	conditions []Condition
	kind       matchKind
	byTarget   bool
}

// MergeMatched selects a MATCHED or NOT MATCHED BY SOURCE branch.
type MergeMatched struct{ matched mergeMatch }

// MergeNotMatched selects a NOT MATCHED or NOT MATCHED BY TARGET branch.
type MergeNotMatched struct{ notMatched mergeMatch }

// MergeInsertOverride selects an INSERT override for a NOT MATCHED branch;
// override selection precedes the action and has no default or no-op action.
type MergeInsertOverride struct {
	insertOverride mergeMatch
	overriding     overridingMode
}

// Matched starts an unconditional selector; And can restrict its applicability.
func Matched() MergeMatched {
	return MergeMatched{matched: mergeMatch{kind: matchMatched}}
}

// NotMatched selects source rows without a target match, permitting insertion.
func NotMatched() MergeNotMatched {
	return MergeNotMatched{notMatched: mergeMatch{kind: matchNotMatched}}
}

// NotMatchedByTarget has the same reachability category as NotMatched.
func NotMatchedByTarget() MergeNotMatched {
	return MergeNotMatched{notMatched: mergeMatch{kind: matchNotMatched, byTarget: true}}
}

// NotMatchedBySource selects target rows without a source match.
func NotMatchedBySource() MergeMatched {
	return MergeMatched{matched: mergeMatch{kind: matchNotMatchedBySource}}
}

// And copies the added conditions so selectors can produce independent siblings.
func (m MergeMatched) And(conditions ...Condition) MergeMatched {
	m.matched.conditions = slices.Concat(m.matched.conditions, conditions)
	return m
}

// ThenUpdate owns its assignment list; rendering rejects an empty list.
func (m MergeMatched) ThenUpdate(assignments ...Assignment) MergeWhen {
	branch := m.matched.complete(mergeUpdate)
	branch.set = cloneSlice(assignments)
	return branch
}

// ThenDelete completes the branch without changing the reusable selector.
func (m MergeMatched) ThenDelete() MergeWhen {
	return m.matched.complete(mergeDelete)
}

// ThenDoNothing still closes the category when its selector is unconditional.
func (m MergeMatched) ThenDoNothing() MergeWhen {
	return m.matched.complete(mergeNothing)
}

// And copies the added conditions so selectors can produce independent siblings.
func (m MergeNotMatched) And(conditions ...Condition) MergeNotMatched {
	m.notMatched.conditions = slices.Concat(m.notMatched.conditions, conditions)
	return m
}

// ThenInsert owns its assignment list; rendering rejects an empty list.
func (m MergeNotMatched) ThenInsert(assignments ...Assignment) MergeWhen {
	branch := m.notMatched.complete(mergeInsert)
	branch.set = cloneSlice(assignments)
	return branch
}

// ThenInsertValues owns both lists; their widths must match at rendering.
func (m MergeNotMatched) ThenInsertValues(columns []string, first WriteValue, rest ...WriteValue) MergeWhen {
	branch := m.notMatched.complete(mergeInsert)
	branch.columns = cloneSlice(columns)
	branch.values = ownedWriteValues(first, rest)
	return branch
}

// ThenInsertDefault leaves value selection to the table's server-side defaults.
func (m MergeNotMatched) ThenInsertDefault() MergeWhen {
	branch := m.notMatched.complete(mergeInsert)
	branch.defaults = true
	return branch
}

// ThenDoNothing still closes the category when its selector is unconditional.
func (m MergeNotMatched) ThenDoNothing() MergeWhen {
	return m.notMatched.complete(mergeNothing)
}

// OverridingSystemValue selects explicit GENERATED ALWAYS identity values
// before completing an INSERT action.
func (m MergeNotMatched) OverridingSystemValue() MergeInsertOverride {
	return MergeInsertOverride{insertOverride: m.notMatched, overriding: overridingSystemValue}
}

// OverridingUserValue selects generated identity values before completing an
// INSERT action.
func (m MergeNotMatched) OverridingUserValue() MergeInsertOverride {
	return MergeInsertOverride{insertOverride: m.notMatched, overriding: overridingUserValue}
}

// ThenInsert owns its assignment list and preserves the selected identity policy.
func (m MergeInsertOverride) ThenInsert(assignments ...Assignment) MergeWhen {
	branch := m.insertOverride.complete(mergeInsert)
	branch.set = cloneSlice(assignments)
	branch.overriding = m.overriding
	return branch
}

// ThenInsertValues owns both lists and preserves the selected identity policy.
func (m MergeInsertOverride) ThenInsertValues(columns []string, first WriteValue, rest ...WriteValue) MergeWhen {
	branch := m.insertOverride.complete(mergeInsert)
	branch.columns = cloneSlice(columns)
	branch.values = ownedWriteValues(first, rest)
	branch.overriding = m.overriding
	return branch
}

func (m mergeMatch) complete(action mergeAction) MergeWhen {
	return MergeWhen{kind: m.kind, byTarget: m.byTarget, conditions: m.conditions, action: action}
}

// MergeBuilder constructs a PostgreSQL MERGE statement. Branch selectors are
// completed by the role-specific MergeMatched and MergeNotMatched APIs.
type MergeBuilder struct {
	aliases   ReturningAliases
	on        []Condition
	branches  []MergeWhen
	returning []Expr
	target    Relation
	source    Relation
	base      statementBase
}

// MergeInto starts a MERGE for a named target table.
func MergeInto(table string) *MergeBuilder { return &MergeBuilder{target: Table(table)} }

// MergeIntoTable starts a MERGE for a relation target.
func MergeIntoTable(table Relation) *MergeBuilder { return &MergeBuilder{target: table} }

// Using replaces the MERGE source relation.
func (b *MergeBuilder) Using(source Relation) *MergeBuilder { b.source = source; return b }

// On appends predicates to the MERGE match condition.
func (b *MergeBuilder) On(conditions ...Condition) *MergeBuilder {
	b.on = append(b.on, conditions...)
	return b
}

// When appends completed WHEN branches in rendering order.
func (b *MergeBuilder) When(branches ...MergeWhen) *MergeBuilder {
	b.branches = append(b.branches, branches...)
	return b
}

// Returning appends expressions to the MERGE RETURNING projection.
func (b *MergeBuilder) Returning(expressions ...Expr) *MergeBuilder {
	b.returning = append(b.returning, expressions...)
	return b
}

// ReturningCols appends named columns to the MERGE RETURNING projection.
func (b *MergeBuilder) ReturningCols(columns ...string) *MergeBuilder {
	for _, c := range columns {
		b.returning = append(b.returning, Col(c))
	}
	return b
}

// ReturningRows replaces the OLD and NEW row aliases for MERGE RETURNING.
func (b *MergeBuilder) ReturningRows(aliases ReturningAliases) *MergeBuilder {
	b.aliases = aliases
	return b
}

func (b *MergeBuilder) append(w *renderer) {
	w.feature(PostgreSQL15, "MERGE")
	if w.stopped("version", 0) {
		return
	}
	if !w.require(len(b.on) > 0 && len(b.branches) > 0, "MERGE", "requires ON and at least one WHEN branch") {
		return
	}
	if !w.require(!b.base.recursive, "MERGE", "WITH RECURSIVE is not supported") {
		return
	}
	w.head(b.base)
	if w.err != nil {
		return
	}
	w.text("MERGE INTO ")
	w.target(b.target, false)
	if w.stopped("target", 0) {
		return
	}
	w.text(" USING ")
	w.relation(b.source)
	if w.stopped("USING", 0) {
		return
	}
	w.text(" ON ")
	w.conditions(b.on)
	if w.stopped("ON", 0) {
		return
	}
	for i, m := range b.branches {
		for j := range i {
			if b.branches[j].kind == m.kind && len(b.branches[j].conditions) == 0 {
				w.fail(ErrInvalid, "MERGE WHEN", "branch follows an unconditional branch of the same category")
				return
			}
		}
		w.mergeBranch(m)
		if w.stopped("WHEN", i+1) {
			return
		}
	}
	if len(b.returning) > 0 {
		w.feature(PostgreSQL17, "MERGE RETURNING")
	}
	w.returning(b.returning, b.aliases)
	if w.stopped("RETURNING", 0) {
		return
	}
	w.foot(b.base)
}
func (w *renderer) mergeBranch(m MergeWhen) {
	w.text(" WHEN ")
	if !w.mergeMatch(m) {
		return
	}
	if !w.mergeActionAllowed(m) {
		return
	}
	switch m.action {
	case mergeUpdate:
		w.text("UPDATE SET ")
		w.assignments(m.set)
		if w.stopped("SET", 0) {
			return
		}
	case mergeDelete:
		w.text("DELETE")
	case mergeNothing:
		w.text("DO NOTHING")
	case mergeInsert:
		w.mergeInsert(m)
		if w.stopped("INSERT", 0) {
			return
		}
	default:
		w.fail(ErrInvalid, "MERGE WHEN", "missing action")
	}
}

func (w *renderer) mergeMatch(m MergeWhen) bool {
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
		return false
	}
	if w.stopped("match", 0) {
		return false
	}
	if len(m.conditions) > 0 {
		w.text(" AND ")
		w.conditions(m.conditions)
		if w.stopped("condition", 0) {
			return false
		}
	}
	w.text(" THEN ")
	return true
}

func (w *renderer) mergeActionAllowed(m MergeWhen) bool {
	switch m.action {
	case mergeNothing:
	case mergeInsert:
		if !w.require(m.kind == matchNotMatched, "MERGE WHEN", "INSERT requires NOT MATCHED [BY TARGET]") {
			return false
		}
	case mergeUpdate, mergeDelete:
		if !w.require(m.kind != matchNotMatched, "MERGE WHEN", "UPDATE and DELETE require a target row") {
			return false
		}
	}
	if m.action != mergeInsert && !w.require(m.overriding == overridingNone, "MERGE WHEN", "OVERRIDING requires INSERT") {
		return false
	}
	return true
}

func (w *renderer) mergeInsert(m MergeWhen) {
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
			if w.stopped("target", i+1) {
				return
			}
		}
		w.byte(')')
	} else if len(m.columns) > 0 {
		w.text(" (")
		w.names(m.columns)
		w.byte(')')
	}
	if w.stopped("targets", 0) {
		return
	}
	if m.defaults {
		if !w.require(m.overriding == overridingNone, "MERGE INSERT", "DEFAULT VALUES cannot specify OVERRIDING") {
			return
		}
		w.text(" DEFAULT VALUES")
		return
	}
	w.overriding(m.overriding)
	if w.stopped("OVERRIDING", 0) {
		return
	}
	w.text(" VALUES (")
	if len(m.set) > 0 {
		for i, a := range m.set {
			if i != 0 {
				w.text(", ")
			}
			w.writeExpr(a.value)
			if w.stopped("value", i+1) {
				return
			}
		}
	} else {
		if len(m.columns) > 0 && !w.require(len(m.columns) == len(m.values), "MERGE INSERT", "column and value counts differ") {
			return
		}
		for i, value := range m.values {
			if i != 0 {
				w.text(", ")
			}
			w.writeExpr(value.expr())
			if w.stopped("value", i+1) {
				return
			}
		}
	}
	w.byte(')')
}

// Reset clears the MERGE builder, including its target and branches.
func (b *MergeBuilder) Reset() { *b = MergeBuilder{} }
