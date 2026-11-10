package qs

import "testing"

func writeRows(n int) [][]WriteValue {
	rows := make([][]WriteValue, n)
	for i := range rows {
		rows[i] = []WriteValue{Value(i), Value(-i)}
	}
	return rows
}

// Rows is the entry for rows sized by data: what it allocates for the row
// list must not depend on how many rows it is given.
func TestRowsAllocationsDoNotDependOnRowCount(t *testing.T) {
	few, many := writeRows(4), writeRows(4096)
	target := InsertInto("t").Columns("a", "b")
	insert := func(rows [][]WriteValue) float64 {
		return testing.AllocsPerRun(20, func() { _ = target.Rows(rows...) })
	}
	if small, large := insert(few), insert(many); small != large {
		t.Errorf("INSERT: %v allocations for 4 rows, %v for 4096", small, large)
	}

	fewExprs, manyExprs := make([][]Expr, 4), make([][]Expr, 4096)
	for i := range manyExprs {
		manyExprs[i] = []Expr{LiteralInt(int64(i))}
	}
	copy(fewExprs, manyExprs)
	values := func(rows [][]Expr) float64 {
		return testing.AllocsPerRun(20, func() { _ = Values().RowsExpr(rows...) })
	}
	if small, large := values(fewExprs), values(manyExprs); small != large {
		t.Errorf("VALUES: %v allocations for 4 rows, %v for 4096", small, large)
	}
}

func TestRowsEqualsOneCallPerRow(t *testing.T) {
	t.Parallel()

	target := InsertInto("t").Columns("a", "b")
	wantSQL := `INSERT INTO "t" ("a", "b") VALUES ($1, $2), ($3, $4), ($5, $6)`
	wantArgs := []any{0, 0, 1, -1, 2, -2}
	checkSQL(t, target.Values(Value(0), Value(0)).Values(Value(1), Value(-1)).Values(Value(2), Value(-2)), wantSQL, wantArgs...)
	checkSQL(t, target.Rows(writeRows(3)...), wantSQL, wantArgs...)
	checkSQL(t, target.Rows(writeRows(1)...).Rows(writeRows(3)[1:]...), wantSQL, wantArgs...)
	checkSQL(t, InsertInto("t").Rows(writeRows(2)...), `INSERT INTO "t" VALUES ($1, $2), ($3, $4)`, 0, 0, 1, -1)

	checkSQL(t, Values().Rows([]any{1, "a"}, []any{2, "b"}), "VALUES ($1, $2), ($3, $4)", 1, "a", 2, "b")
	checkSQL(t, Values(1, "a").Rows([]any{2, "b"}), "VALUES ($1, $2), ($3, $4)", 1, "a", 2, "b")
	checkSQL(t, Values().RowsExpr([]Expr{LiteralInt(1)}, []Expr{LiteralInt(2)}), "VALUES (1), (2)")
}

// Rows copies the structure it is given and shares nothing with a sibling
// derived from the same base.
func TestRowsOwnsItsRows(t *testing.T) {
	t.Parallel()

	source := writeRows(2)
	base := InsertInto("t").Columns("a", "b").Rows(source...)
	source[0][0] = Value(99)
	source[1] = nil
	first := base.Rows([]WriteValue{Value(10), Value(11)})
	second := base.Rows([]WriteValue{Value(20), Value(21)})
	firstChild := first.Values(Value(12), Value(13))

	const two = `INSERT INTO "t" ("a", "b") VALUES ($1, $2), ($3, $4)`
	checkSQL(t, base, two, 0, 0, 1, -1)
	checkSQL(t, first, two+", ($5, $6)", 0, 0, 1, -1, 10, 11)
	checkSQL(t, second, two+", ($5, $6)", 0, 0, 1, -1, 20, 21)
	checkSQL(t, firstChild, two+", ($5, $6), ($7, $8)", 0, 0, 1, -1, 10, 11, 12, 13)
}

func TestRowsRejectsNoRowsAndEmptyRows(t *testing.T) {
	t.Parallel()

	for name, statement := range map[string]Statement{
		"insert_no_rows":    InsertInto("t").Columns("a").Rows(),
		"insert_empty_row":  InsertInto("t").Columns("a").Rows([]WriteValue{Value(1)}, nil),
		"insert_row_width":  InsertInto("t").Columns("a").Rows([]WriteValue{Value(1)}, []WriteValue{Value(1), Value(2)}),
		"values_no_rows":    Values().Rows(),
		"values_empty_row":  Values().Rows([]any{1}, []any{}),
		"values_row_width":  Values().RowsExpr([]Expr{LiteralInt(1)}, []Expr{LiteralInt(1), LiteralInt(2)}),
		"values_expression": Values().Rows([]any{LiteralInt(1)}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}
