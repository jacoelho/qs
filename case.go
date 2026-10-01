package qx

// CaseBuilder constructs a searched CASE (Case) or simple CASE (CaseOf).
// End snapshots the branches so later builder changes cannot affect an Expr.
type CaseBuilder struct {
	operand   Expr
	simple    bool
	branches  []caseBranch
	otherwise Expr
	hasElse   bool
	invalid   string
}
type caseBranch struct{ when, then Expr }

func Case() *CaseBuilder             { return &CaseBuilder{} }
func CaseOf(value Expr) *CaseBuilder { return &CaseBuilder{operand: value, simple: true} }
func (b *CaseBuilder) When(condition Condition, result Expr) *CaseBuilder {
	if b.simple {
		b.invalid = "use WhenValue for a simple CASE"
	}
	b.branches = append(b.branches, caseBranch{condition.expr, result})
	return b
}
func (b *CaseBuilder) WhenValue(value, result Expr) *CaseBuilder {
	if !b.simple {
		b.invalid = "use When for a searched CASE"
	}
	b.branches = append(b.branches, caseBranch{value, result})
	return b
}
func (b *CaseBuilder) Else(result Expr) *CaseBuilder {
	b.otherwise = result
	b.hasElse = true
	return b
}
func (b *CaseBuilder) End() Expr {
	if b == nil {
		return invalidExpr("CASE", "nil CASE builder")
	}
	copy := *b
	copy.branches = cloneSlice(b.branches)
	return Expr{kind: exprCase, value: &copy}
}
func (w *renderer) caseExpr(b *CaseBuilder) {
	if !w.require(b.invalid == "", "CASE", b.invalid) {
		return
	}
	if !w.require(len(b.branches) != 0, "CASE", "requires at least one WHEN") {
		return
	}
	w.text("CASE")
	if b.simple {
		w.byte(' ')
		w.expr(b.operand)
	}
	for _, branch := range b.branches {
		w.text(" WHEN ")
		w.expr(branch.when)
		w.text(" THEN ")
		w.expr(branch.then)
	}
	if b.hasElse {
		w.text(" ELSE ")
		w.expr(b.otherwise)
	}
	w.text(" END")
}
