package qs

// SearchedCaseBuilder constructs a searched CASE expression.
// End snapshots the branches so later builder changes cannot affect an Expr.
type SearchedCaseBuilder struct {
	searched caseExpression
}

// SimpleCaseBuilder constructs a simple CASE expression.
// End snapshots the branches so later builder changes cannot affect an Expr.
type SimpleCaseBuilder struct {
	simple caseExpression
}

type caseExpression struct {
	operand   Expr
	otherwise Expr
	branches  []caseBranch
	simple    bool
	hasElse   bool
}

type caseBranch struct{ when, then Expr }

// Case starts a searched CASE. Rendering rejects an expression with no branches.
func Case() *SearchedCaseBuilder { return &SearchedCaseBuilder{} }

// CaseOf starts a simple CASE; a zero operand remains a rendering error.
func CaseOf(value Expr) *SimpleCaseBuilder {
	return &SimpleCaseBuilder{simple: caseExpression{operand: value}}
}

// When appends a branch in evaluation order; a zero condition is invalid.
func (b *SearchedCaseBuilder) When(condition Condition, result Expr) *SearchedCaseBuilder {
	b.searched.branches = append(b.searched.branches, caseBranch{condition.expr, result})
	return b
}

// Else replaces the fallback. Use Param(nil) for an explicit SQL NULL.
func (b *SearchedCaseBuilder) Else(result Expr) *SearchedCaseBuilder {
	b.searched.otherwise = result
	b.searched.hasElse = true
	return b
}

// End snapshots branch selection while retaining live nested statements.
// A nil receiver produces an expression that fails rendering.
func (b *SearchedCaseBuilder) End() Expr {
	if b == nil {
		return invalidExpr("CASE", "nil CASE builder")
	}
	snapshot := b.searched
	snapshot.simple = false
	snapshot.branches = cloneSlice(b.searched.branches)
	return Expr{kind: exprCase, value: &snapshot}
}

// WhenValue appends an operand comparison in evaluation order.
func (b *SimpleCaseBuilder) WhenValue(value, result Expr) *SimpleCaseBuilder {
	b.simple.branches = append(b.simple.branches, caseBranch{value, result})
	return b
}

// Else replaces the fallback. Use Param(nil) for an explicit SQL NULL.
func (b *SimpleCaseBuilder) Else(result Expr) *SimpleCaseBuilder {
	b.simple.otherwise = result
	b.simple.hasElse = true
	return b
}

// End snapshots branch selection while retaining live nested statements.
// A nil receiver produces an expression that fails rendering.
func (b *SimpleCaseBuilder) End() Expr {
	if b == nil {
		return invalidExpr("CASE", "nil CASE builder")
	}
	snapshot := b.simple
	snapshot.simple = true
	snapshot.branches = cloneSlice(b.simple.branches)
	return Expr{kind: exprCase, value: &snapshot}
}

func (w *renderer) caseExpr(b *caseExpression) {
	if !w.require(len(b.branches) != 0, "CASE", "requires at least one WHEN") {
		return
	}
	w.text("CASE")
	if b.simple {
		w.byte(' ')
		w.expr(b.operand)
		if w.stopped("CASE operand", 0) {
			return
		}
	}
	for i, branch := range b.branches {
		w.text(" WHEN ")
		w.expr(branch.when)
		if w.stopped("CASE WHEN", i+1) {
			return
		}
		w.text(" THEN ")
		w.expr(branch.then)
		if w.stopped("CASE THEN", i+1) {
			return
		}
	}
	if b.hasElse {
		w.text(" ELSE ")
		w.expr(b.otherwise)
		if w.stopped("CASE ELSE", 0) {
			return
		}
	}
	w.text(" END")
}
