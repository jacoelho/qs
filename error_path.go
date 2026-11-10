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
