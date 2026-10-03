package qs

import (
	"errors"
	"strconv"
)

// stopped closes a scope immediately, before a sibling can fail. Callers must
// enter clean and return when it reports failure; otherwise a later scope could
// incorrectly claim an earlier error. Labels are static grammar roles.
func (w *renderer) stopped(label string, index int) bool {
	if w.err == nil {
		return false
	}
	w.errorPath(label, index)
	return true
}

// Paths accumulate leaf-first without a success-path stack. AppendWith reverses
// the error-owned slice once, after the entire failing traversal has unwound.
func (w *renderer) errorPath(label string, index int) {
	err, ok := errors.AsType[*RenderError](w.err)
	if !ok {
		return
	}
	if index > 0 {
		label += "[" + strconv.Itoa(index) + "]"
	}
	err.Path = append(err.Path, label)
}

func (w *renderer) statementErrorPath(s Statement) {
	var label string
	switch s.(type) {
	case *SelectBuilder:
		label = "SELECT"
	case *InsertRows, *InsertSelect, *InsertAssignments, *InsertDefaults:
		label = "INSERT"
	case *UpdateBuilder:
		label = "UPDATE"
	case *DeleteBuilder:
		label = "DELETE"
	case *MergeBuilder:
		label = "MERGE"
	case *SetBuilder:
		label = "set operation"
	case *ValuesBuilder:
		label = "VALUES"
	case *TableBuilder:
		label = "TABLE"
	case *ExplainBuilder:
		label = "EXPLAIN"
	case *TruncateBuilder:
		label = "TRUNCATE"
	case *ExecuteBuilder:
		label = "EXECUTE"
	case *CreateTableAsBuilder:
		label = "CREATE TABLE AS"
	case *MaterializedViewBuilder:
		label = "CREATE MATERIALIZED VIEW"
	case *DeclareCursorBuilder:
		label = "DECLARE CURSOR"
	case *SelectIntoBuilder:
		label = "SELECT INTO"
	case *SQLStatement:
		label = "statement fragment"
	default:
		label = "statement"
	}
	w.errorPath(label, 0)
}
