package qs

// Aggregate families own their argument grammar; FILTER and OVER share traversal.
type aggregateTail struct {
	window     *WindowSpec
	windowName string
	filter     []Condition
}

func (w *renderer) aggregateTail(t aggregateTail) {
	if w.err != nil {
		return
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
	} else if t.windowName != "" {
		w.text(" OVER ")
		w.identifierPart(t.windowName)
		if w.stopped("OVER", 0) {
			return
		}
	}
}
