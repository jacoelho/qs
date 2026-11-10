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

func TestSetOperatorValidation(t *testing.T) {
	t.Parallel()

	query := Union(Select(LiteralInt(1)), Select(LiteralInt(1)))
	query.operator = setOperator(255)
	checkError(t, query, ErrInvalid)
}

// A chain's width is found once and handed down its left spine. A branch of
// another width must still be refused wherever in the chain it sits.
func TestSetChainRefusesAMismatchedBranchAtAnyPosition(t *testing.T) {
	t.Parallel()

	const branches = 6
	narrow, wide := Select(LiteralInt(1)), Select(LiteralInt(1), LiteralInt(2))
	chain := func(mismatch int) Rowset {
		pick := func(i int) Rowset {
			if i == mismatch {
				return wide
			}
			return narrow
		}
		query := pick(0)
		for i := 1; i < branches; i++ {
			query = Union(query, pick(i))
		}
		return query
	}
	if _, _, err := ToSQL(chain(-1)); err != nil {
		t.Fatalf("equal-width chain: %v", err)
	}
	for position := range branches {
		checkError(t, chain(position), ErrInvalid)
	}

	// A set operation as a right branch is measured on its own.
	checkError(t, Union(chain(-1), Union(wide, wide)), ErrInvalid)
	checkError(t, Union(chain(-1), Union(narrow, wide)), ErrInvalid)
	// A leftmost leaf of unknown width leaves the chain unchecked, as before.
	checkSQL(t, Union(Union(Select(Star()).From("t"), narrow), wide),
		`((SELECT * FROM "t") UNION (SELECT 1)) UNION (SELECT 1, 2)`)
}
