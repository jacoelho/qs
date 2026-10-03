package qs

// Clone copies the complete builder graph, preserving shared subqueries and
// cycles. Bound application values are intentionally not deep-copied. The
// original graph must not be mutated concurrently with Clone.
func Clone[T Statement](statement T) T {
	if any(statement) == nil {
		return statement
	}
	c := cloneContext{seen: make(map[Statement]Statement)}
	cloned, ok := c.statement(statement).(T)
	if !ok {
		panic("qs: Clone internal result is not the requested concrete statement type")
	}
	return cloned
}

type cloneContext struct{ seen map[Statement]Statement }

// utilitySource is the one non-tagged union in the statement graph. Its
// discriminator is an explicit bool, so the generator treats this value as a
// classified boundary rather than trying to infer the relationship between
// query and execute fields.
func (c *cloneContext) utilitySource(s queryUtilitySource) queryUtilitySource {
	if s.executeSource {
		if s.execute != nil {
			s.execute = ownedPayload[*ExecuteBuilder](c.statement(s.execute))
		}
		return s
	}
	s.query = c.rowset(s.query)
	return s
}
