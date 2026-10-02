package qs

import "testing"

func TestImmutableWindowSiblingBranches(t *testing.T) {
	t.Parallel()

	base := WindowSpec{partition: make([]Expr, 1, 8)}
	base.partition[0] = Param("base")
	leftInput := []Expr{Param("left")}
	rightInput := []Expr{Param("right")}
	left := base.PartitionBy(leftInput...)
	right := base.PartitionBy(rightInput...)
	leftInput[0], rightInput[0] = Param("changed"), Param("changed")
	checkSQL(t, Select(CountAll().Over(base)), `SELECT count(*) OVER (PARTITION BY $1)`, "base")
	checkSQL(t, Select(CountAll().Over(left)), `SELECT count(*) OVER (PARTITION BY $1, $2)`, "base", "left")
	checkSQL(t, Select(CountAll().Over(right)), `SELECT count(*) OVER (PARTITION BY $1, $2)`, "base", "right")
}

func TestRowConstructionOwnsStructuralInputs(t *testing.T) {
	t.Parallel()

	row := Row(Col("a"), Col("b"))
	payload := []byte("old")
	values := []any{payload, 2}
	comparison := row.EqValues(values...)
	values[0], values[1] = "changed", 99
	payload[0] = 'n'
	checkSQL(t, Select(comparison.Expr()), `SELECT (ROW("a", "b") = ROW($1, $2))`, []byte("nld"), 2)

	rows := []RowExpr{Row(Param(3), Param(4)), Row(Param(5), Param(6))}
	membership := row.In(rows...)
	rows[0] = Row(Param(7), Param(8))
	checkSQL(t, Select(membership.Expr()), `SELECT (ROW("a", "b") IN (ROW($1, $2), ROW($3, $4)))`, 3, 4, 5, 6)
	checkSQL(t, Select(row.In().Expr(), row.NotIn().Expr()), `SELECT FALSE, TRUE`)
	checkError(t, Select(row.EqValues(Param(1), 2).Expr()), ErrInvalid)
}

func TestSelectBatchOrderingAndReuse(t *testing.T) {
	t.Parallel()

	query := SelectCols("a", "b").ColumnNames("c", "d").ColumnsSQL("count(*)").
		GroupBy("a", "b").GroupBy("c", "d").From("first", "second")
	clone := query.Clone()
	query.ColumnNames().ColumnsSQL().GroupBy().From("last")
	checkSQL(t, query, `SELECT "a", "b", "c", "d", count(*) FROM "last" GROUP BY "a", "b", "c", "d"`)
	query.From()
	checkSQL(t, query, `SELECT "a", "b", "c", "d", count(*) GROUP BY "a", "b", "c", "d"`)
	query.Reset()
	query.ColumnNames("x", "y").From("next").GroupBy("x", "y")
	checkSQL(t, query, `SELECT "x", "y" FROM "next" GROUP BY "x", "y"`)
	checkSQL(t, clone, `SELECT "a", "b", "c", "d", count(*) FROM "first", "second" GROUP BY "a", "b", "c", "d"`)
}
