package qs

// Aggregate families own their argument grammar; FILTER and OVER share traversal.
type aggregateTail struct {
	window     *WindowSpec
	windowName string
	filter     []Condition
}

func (w *renderer) aggregateTail(t aggregateTail) {
	if len(t.filter) != 0 {
		w.text(" FILTER (WHERE ")
		w.conditions(t.filter)
		w.byte(')')
	}
	if t.window != nil {
		w.text(" OVER (")
		w.window(*t.window)
		w.byte(')')
	} else if t.windowName != "" {
		w.text(" OVER ")
		w.identifierPart(t.windowName)
	}
}

func (c *cloneContext) aggregateTail(t aggregateTail) aggregateTail {
	t.filter = c.conditions(t.filter)
	if t.window != nil {
		window := c.window(*t.window)
		t.window = &window
	}
	return t
}
