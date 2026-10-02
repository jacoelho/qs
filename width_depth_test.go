package qs

import "testing"

func leftNestedUnion(depth int) Rowset {
	var query Rowset = Select(LiteralInt(0))
	for i := range depth {
		query = Union(query, Select(LiteralInt(int64(i))))
	}
	return query
}

func TestSetWidthAnalysisUsesConfiguredDepth(t *testing.T) {
	t.Parallel()

	left := leftNestedUnion(257)
	valid := Union(left, Select(LiteralInt(1)))
	if _, _, err := ToSQLWith(valid, Options{MaxDepth: 4096}); err != nil {
		t.Fatalf("deep equal-width set operation: %v", err)
	}

	invalid := Union(left, Select(LiteralInt(1), LiteralInt(2)))
	checkErrorWithOptions(t, invalid, Options{MaxDepth: 4096}, ErrInvalid)
	checkErrorWithOptions(t, valid, Options{MaxDepth: 128}, ErrDepth)
	checkErrorWithOptions(t, InsertInto("t").Columns("a", "b").From(left), Options{MaxDepth: 4096}, ErrInvalid)
	checkSQL(t, Union(Select(Star()).From("t"), Select(Param(1), Param(2))), `(SELECT * FROM "t") UNION (SELECT $1, $2)`, 1, 2)
}

func TestSetWidthAnalysisBoundsCycles(t *testing.T) {
	t.Parallel()

	cycle := Union(Select(LiteralInt(1)), Select(LiteralInt(2)))
	cycle.left = cycle
	checkErrorWithOptions(t, cycle, Options{MaxDepth: 32}, ErrDepth)
}

func TestSetOperatorValidation(t *testing.T) {
	t.Parallel()

	query := Union(Select(LiteralInt(1)), Select(LiteralInt(1)))
	query.operator = setOperator(255)
	checkError(t, query, ErrInvalid)
}
