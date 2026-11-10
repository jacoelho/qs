package qs

import "slices"

// Aggregate families own their argument grammar; FILTER and OVER share traversal.
// An aggregate has one window: an inline definition or a named reference.
type aggregateTail struct {
	window     *WindowSpec
	windowName string
	filter     []Condition
	named      bool
}

func (t aggregateTail) filtered(conditions []Condition) aggregateTail {
	t.filter = slices.Concat(t.filter, conditions)
	return t
}

func (t aggregateTail) over(window WindowSpec) aggregateTail {
	t.window, t.windowName, t.named = &window, "", false
	return t
}

func (t aggregateTail) overNamed(name string) aggregateTail {
	t.window, t.windowName, t.named = nil, name, true
	return t
}

// aggregateTail writes FILTER and OVER. ordered says the call has an in-call
// ORDER BY, which PostgreSQL does not implement for a window function.
func (w *renderer) aggregateTail(t aggregateTail, ordered bool) {
	if w.err != nil {
		return
	}
	if t.window != nil || t.named {
		if !w.require(!ordered, "OVER", "an aggregate with ORDER BY cannot be a window function") {
			return
		}
		w.hasWindow = true
	}
	if len(t.filter) != 0 {
		w.text(" FILTER (WHERE ")
		w.conditions(t.filter)
		if w.stopped("FILTER", 0) {
			return
		}
		w.byte(')')
	}
	if t.window != nil {
		w.text(" OVER (")
		w.window(*t.window)
		if w.stopped("OVER", 0) {
			return
		}
		w.byte(')')
	} else if t.named {
		w.text(" OVER ")
		w.identifierPart(t.windowName)
		if w.stopped("OVER", 0) {
			return
		}
	}
}
