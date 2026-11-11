package qs

import (
	"fmt"
	"slices"
)

type overridingMode uint8

const (
	overridingNone overridingMode = iota
	overridingSystemValue
	overridingUserValue
)

// InsertTarget holds only the relation that will receive a completed INSERT.
// Select a source or a target-column list before adding other INSERT clauses.
type InsertTarget struct{ table Relation }

// InsertColumnsTarget holds an INSERT relation and its immutable target-column
// list. Select a source to obtain a completed INSERT role.
type InsertColumnsTarget struct {
	columnsTargets []Expr
	columnsTable   Relation
}

// InsertInto starts an INSERT target for a named table.
func InsertInto(table string) InsertTarget { return InsertTarget{table: Table(table)} }

// InsertIntoTable starts an INSERT target for a relation expression.
func InsertIntoTable(table Relation) InsertTarget { return InsertTarget{table: table} }

func newInsertColumnsTarget(table Relation, first string, rest []string) InsertColumnsTarget {
	targets := make([]Expr, 1+len(rest))
	targets[0] = unqualifiedColumn(first)
	for i, column := range rest {
		targets[i+1] = unqualifiedColumn(column)
	}
	return InsertColumnsTarget{columnsTable: table, columnsTargets: targets}
}

// ColumnsSlice selects literal target-column names and copies the slice.
// An empty list is invalid when the completed INSERT is rendered.
func (t InsertTarget) ColumnsSlice(columns []string) InsertColumnsTarget {
	targets := make([]Expr, len(columns))
	for i, column := range columns {
		targets[i] = unqualifiedColumn(column)
	}
	return InsertColumnsTarget{columnsTable: t.table, columnsTargets: targets}
}

func newInsertExprTarget(table Relation, first Expr, rest []Expr) InsertColumnsTarget {
	targets := make([]Expr, 1+len(rest))
	targets[0] = first
	copy(targets[1:], rest)
	return InsertColumnsTarget{columnsTable: table, columnsTargets: targets}
}

type insertBase struct {
	table     Relation
	aliases   ReturningAliases
	conflict  *conflictClause
	returning []Expr
	targets   []Expr
	statementBase
	overriding      overridingMode
	targetsSelected bool
}

// InsertRows is an INSERT whose source is one or more direct value rows.
type InsertRows struct {
	rows [][]WriteValue
	base insertBase
}

// InsertSelect is an INSERT whose source is a rowset. The source and the
// target columns are fixed together when it is created, so neither can be
// replaced apart from the other.
type InsertSelect struct {
	source Rowset
	base   insertBase
}

// InsertAssignments is an INSERT whose source is one row of assignments.
type InsertAssignments struct {
	assignments []Assignment
	base        insertBase
}

// InsertDefaults is an INSERT using the target table's default values.
type InsertDefaults struct{ base insertBase }

func (t InsertTarget) base() insertBase { return insertBase{table: t.table} }

func (t InsertColumnsTarget) base() insertBase {
	return insertBase{table: t.columnsTable, targets: t.columnsTargets, targetsSelected: true}
}

func insertReturningCols(base *insertBase, columns ...string) {
	base.returning = extendWith(base.returning, columns, unqualifiedColumn)
}

// Values selects a direct value-row source.
func (t InsertTarget) Values(first WriteValue, rest ...WriteValue) InsertRows {
	return InsertRows{base: t.base()}.Values(first, rest...)
}

// ValuesSlice selects one direct value row and copies its structural slice.
// An empty row is invalid when rendered.
func (t InsertTarget) ValuesSlice(values []WriteValue) InsertRows {
	return InsertRows{base: t.base()}.ValuesSlice(values)
}

// Rows selects a direct value-row source holding every row of rows. It is the
// entry for rows sized by data; see InsertRows.Rows.
func (t InsertTarget) Rows(rows ...[]WriteValue) InsertRows {
	return InsertRows{base: t.base()}.Rows(rows...)
}

// From selects a rowset source.
func (t InsertTarget) From(source Rowset) InsertSelect {
	return InsertSelect{base: t.base(), source: ownedRowset(source)}
}

// FromArrays selects the named columns and fills each from its array, one row
// per element: INSERT INTO t ("id", "name") SELECT * FROM unnest($1, $2).
// A column and its array are one argument, where a Columns list and a separate
// projection pair by position. The parameter count follows the columns, not
// the rows. Each array must have a scalar element type: an array of a
// composite type expands to one column per field. Supply arrays of equal
// length, because PostgreSQL pads shorter ones with NULL; empty arrays insert
// no rows.
func (t InsertTarget) FromArrays(first ArrayColumn, rest ...ArrayColumn) InsertSelect {
	names, arrays := splitArrayColumns(first, rest)
	return t.ColumnsSlice(names).From(Select(Star()).FromExpr(TableFunc(Unnest(arrays...))))
}

// FromNamed selects a query source and takes the target columns from the names
// of the query's output columns, in order: a column reference names its
// column, and As names anything else. Each target is then written beside the
// expression that fills it, where Columns followed by From pairs two lists by
// position:
//
//	InsertInto("events").FromNamed(Select(u.Col("name"), u.Col("ordinality").As("position")).FromExpr(u))
//
// renders INSERT INTO "events" ("name", "position") SELECT "u"."name",
// "u"."ordinality" AS "position" FROM ... . Rendering fails for an output
// column that is neither a column reference nor aliased, such as a star,
// trusted SQL or an unaliased expression, for an aliased star or composite
// expansion, for a duplicate name and for an empty projection.
func (t InsertTarget) FromNamed(query SelectBuilder) InsertSelect {
	targets := make([]Expr, len(query.columns))
	for i, column := range query.columns {
		targets[i] = outputColumn(column)
	}
	return InsertColumnsTarget{columnsTable: t.table, columnsTargets: targets}.From(query)
}

// outputColumn is the insert target that a projected expression names: its
// alias, or the column a reference ends in. qs does not derive the label
// PostgreSQL would give any other expression.
func outputColumn(e Expr) Expr {
	if e.isColumn() {
		return unqualifiedTarget(e)
	}
	if n, ok := e.node().(*aliasExpression); ok {
		switch n.expr.node().(type) {
		case *starExpression, *fieldsExpression:
			return invalidExpr("INSERT", "FromNamed cannot name a star or a composite expansion with one alias")
		}
		return unqualifiedColumn(n.alias)
	}
	return invalidExpr("INSERT", "FromNamed requires each output column to be a column reference or to have an alias")
}

// Set selects an assignment source.
func (t InsertTarget) Set(first Assignment, rest ...Assignment) InsertAssignments {
	return InsertAssignments{base: t.base()}.Set(first, rest...)
}

// DefaultValues selects the target table's server-side defaults.
func (t InsertTarget) DefaultValues() InsertDefaults {
	return InsertDefaults{base: t.base()}
}

// Values appends a direct value row to the INSERT. Each call copies the row
// list, so a loop that adds one row per call does work quadratic in the number
// of rows: add rows sized by data with Rows.
func (b InsertRows) Values(first WriteValue, rest ...WriteValue) InsertRows {
	b.rows = extend(b.rows, ownedList(first, rest))
	return b
}

// ValuesSlice appends one direct value row and copies its structural slice.
// An empty row is invalid when rendered. Bound values are not copied. Like
// Values, each call copies the row list.
func (b InsertRows) ValuesSlice(values []WriteValue) InsertRows {
	b.rows = extend(b.rows, cloneSlice(values))
	return b
}

// Rows appends every row of rows in one step, copying their structural
// slices; bound values are not copied. Its cost is linear in the rows added,
// which makes it the way to add rows sized by data. An INSERT with no rows, or
// with an empty row, is invalid when rendered.
func (b InsertRows) Rows(rows ...[]WriteValue) InsertRows {
	b.rows = extendRows(b.rows, rows)
	return b
}

// Set appends assignments to the single-row INSERT source.
func (b InsertAssignments) Set(first Assignment, rest ...Assignment) InsertAssignments {
	b.assignments = slices.Concat(b.assignments, []Assignment{first}, rest)
	return b
}

func (b *insertBase) validateTargets(w *renderer) bool {
	if !w.require(!b.targetsSelected || len(b.targets) > 0, "INSERT", "requires at least one target column") {
		return false
	}
	for i, target := range b.targets {
		for j := range i {
			if sameIdentifier(b.targets[j], target) {
				w.fail(ErrInvalid, "INSERT", "duplicate target column")
				return false
			}
		}
	}
	return true
}

func (b *insertBase) appendHeader(w *renderer, assignments []Assignment, allowOverride bool) bool {
	w.head(b.statementBase)
	if w.err != nil {
		return false
	}
	w.text("INSERT INTO ")
	w.target(b.table, true)
	if w.stopped("target", 0) {
		return false
	}
	if len(assignments) > 0 {
		w.text(" (")
		for i, assignment := range assignments {
			if !w.require(!assignment.row, "INSERT SET", "tuple assignments are not supported in single-row insert assignments") {
				return false
			}
			if i != 0 {
				w.text(", ")
			}
			w.assignmentTarget(assignment.target)
			if w.stopped("target", i+1) {
				return false
			}
		}
		w.byte(')')
	} else if b.targetsSelected {
		w.text(" (")
		for i, target := range b.targets {
			if i != 0 {
				w.text(", ")
			}
			w.assignmentTarget(target)
			if w.stopped("target", i+1) {
				return false
			}
		}
		w.byte(')')
	}
	if !allowOverride && !w.require(b.overriding == overridingNone, "INSERT", "DEFAULT VALUES cannot specify OVERRIDING") {
		return false
	}
	w.overriding(b.overriding)
	if w.stopped("OVERRIDING", 0) {
		return false
	}
	w.byte(' ')
	return true
}

func (b *insertBase) appendTail(w *renderer) {
	if b.conflict != nil {
		w.conflict(*b.conflict)
		if w.stopped("ON CONFLICT", 0) {
			return
		}
	}
	w.returning(b.returning, b.aliases)
	if w.stopped("RETURNING", 0) {
		return
	}
	w.foot(b.statementBase)
}

func (w *renderer) insertRows(b *InsertRows) {
	if !b.base.validateTargets(w) {
		return
	}
	if b.base.targetsSelected {
		for i, row := range b.rows {
			width := writeProjectionWidth(row)
			if !w.rowWidth("INSERT", i+1, width, len(b.base.targets)) {
				return
			}
		}
	}
	if !b.base.appendHeader(w, nil, true) {
		return
	}
	w.writeValues(b.rows)
	if w.stopped("VALUES", 0) {
		return
	}
	b.base.appendTail(w)
}

func (w *renderer) insertSelect(b *InsertSelect) {
	if !b.base.validateTargets(w) {
		return
	}
	width := statementWidth(b.source, w.options.MaxDepth)
	if width >= 0 && b.base.targetsSelected && width != len(b.base.targets) {
		w.fail(ErrInvalid, "INSERT SELECT", fmt.Sprintf("expected %d target columns, got %d projected columns", len(b.base.targets), width))
		return
	}
	if !b.base.appendHeader(w, nil, true) {
		return
	}
	w.statement(b.source)
	if w.stopped("source", 0) {
		return
	}
	b.base.appendTail(w)
}

func (w *renderer) insertAssignments(b *InsertAssignments) {
	if !w.require(len(b.assignments) > 0, "INSERT", "requires assignments") {
		return
	}
	if !w.validateAssignments(b.assignments) {
		return
	}
	if !b.base.appendHeader(w, b.assignments, true) {
		return
	}
	w.text("VALUES (")
	for i, assignment := range b.assignments {
		if i != 0 {
			w.text(", ")
		}
		w.writeExpr(assignment.value)
		if w.stopped("assignment", i+1) {
			return
		}
	}
	w.byte(')')
	b.base.appendTail(w)
}

func (w *renderer) insertDefaults(b *InsertDefaults) {
	if !b.base.validateTargets(w) {
		return
	}
	if !b.base.appendHeader(w, nil, false) {
		return
	}
	w.text("DEFAULT VALUES")
	b.base.appendTail(w)
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
