package qs

import "slices"

// aggregateWindow is which OVER clause an aggregate has. Each variant reads only
// its own payload in aggregateTail: inline reads window, named reads windowName.
type aggregateWindow uint8

const (
	windowNone aggregateWindow = iota
	windowInline
	windowNamed
)

// Aggregate families own their argument grammar; FILTER and OVER share traversal.
// An aggregate has one window: none, an inline definition or a named reference.
type aggregateTail struct {
	window     *WindowSpec
	windowName string
	filter     []Condition
	over       aggregateWindow
}

func (t aggregateTail) filtered(conditions []Condition) aggregateTail {
	t.filter = slices.Concat(t.filter, conditions)
	return t
}

func (t aggregateTail) inlineWindow(window WindowSpec) aggregateTail {
	t.over, t.window, t.windowName = windowInline, &window, ""
	return t
}

func (t aggregateTail) namedWindow(name string) aggregateTail {
	t.over, t.window, t.windowName = windowNamed, nil, name
	return t
}

// windowed reports whether the call is a window function.
func (t aggregateTail) windowed() bool { return t.over != windowNone }

// aggregateTail writes FILTER and OVER. ordered says the call has an in-call
// ORDER BY, which PostgreSQL does not implement for a window function.
func (w *renderer) aggregateTail(t aggregateTail, ordered bool) {
	if w.err != nil {
		return
	}
	if t.windowed() {
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
	switch t.over {
	case windowNone:
	case windowInline:
		w.text(" OVER (")
		w.window(*t.window)
		if w.stopped("OVER", 0) {
			return
		}
		w.byte(')')
	case windowNamed:
		w.text(" OVER ")
		w.identifierPart(t.windowName)
		if w.stopped("OVER", 0) {
			return
		}
	default:
		w.fail(ErrInvalid, "OVER", "unknown window kind")
	}
}
