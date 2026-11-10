package qs

// PendingSearchedCase is a searched CASE with no branch yet. It is not an
// expression: When supplies the first branch.
type PendingSearchedCase struct{}

// SearchedCaseBuilder constructs a searched CASE expression with at least one
// branch. It is an immutable value: When and Else return a new builder and
// leave the receiver unchanged.
type SearchedCaseBuilder struct {
	searched caseExpression
}

// PendingSimpleCase is a simple CASE with its operand and no branch yet. It
// is not an expression: WhenValue supplies the first branch.
type PendingSimpleCase struct {
	operand Expr
}

// SimpleCaseBuilder constructs a simple CASE expression with at least one
// branch. It is an immutable value: WhenValue and Else return a new builder
// and leave the receiver unchanged.
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

// Case starts a searched CASE. When supplies its first branch.
func Case() PendingSearchedCase { return PendingSearchedCase{} }

// CaseOf starts a simple CASE that compares value with each branch. WhenValue
// supplies its first branch; a zero operand remains a rendering error.
func CaseOf(value Expr) PendingSimpleCase { return PendingSimpleCase{operand: value} }

// When supplies the first branch; a zero condition is invalid.
func (PendingSearchedCase) When(condition Condition, result Expr) SearchedCaseBuilder {
	return SearchedCaseBuilder{}.When(condition, result)
}

// WhenValue supplies the first operand comparison.
func (p PendingSimpleCase) WhenValue(value, result Expr) SimpleCaseBuilder {
	return SimpleCaseBuilder{simple: caseExpression{operand: p.operand}}.WhenValue(value, result)
}

// When appends a branch in evaluation order; a zero condition is invalid.
func (b SearchedCaseBuilder) When(condition Condition, result Expr) SearchedCaseBuilder {
	b.searched.branches = extend(b.searched.branches, caseBranch{condition.expr, result})
	return b
}

// Else replaces the fallback. Use Param(nil) for an explicit SQL NULL.
func (b SearchedCaseBuilder) Else(result Expr) SearchedCaseBuilder {
	b.searched.otherwise = result
	b.searched.hasElse = true
	return b
}

// End returns the CASE expression. A zero SearchedCaseBuilder has no branch
// and fails rendering.
func (b SearchedCaseBuilder) End() Expr {
	expression := b.searched
	expression.simple = false
	return nodeExpr(&expression)
}

// WhenValue appends an operand comparison in evaluation order.
func (b SimpleCaseBuilder) WhenValue(value, result Expr) SimpleCaseBuilder {
	b.simple.branches = extend(b.simple.branches, caseBranch{value, result})
	return b
}

// Else replaces the fallback. Use Param(nil) for an explicit SQL NULL.
func (b SimpleCaseBuilder) Else(result Expr) SimpleCaseBuilder {
	b.simple.otherwise = result
	b.simple.hasElse = true
	return b
}

// End returns the CASE expression. A zero SimpleCaseBuilder has no branch and
// fails rendering.
func (b SimpleCaseBuilder) End() Expr {
	expression := b.simple
	expression.simple = true
	return nodeExpr(&expression)
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
