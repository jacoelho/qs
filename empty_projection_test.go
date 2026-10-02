package qs

import "testing"

func TestSelectNoColumnsRendersAndRetainsExplicitIntent(t *testing.T) {
	t.Parallel()

	q := SelectNoColumns().Columns(Col("removed")).RemoveColumns()
	checkSQL(t, q, `SELECT `)

	clone := q.Clone()
	checkSQL(t, clone, `SELECT `)

	q.Reset()
	checkError(t, q, ErrInvalid)
	checkError(t, Select().Columns(Col("removed")).RemoveColumns(), ErrInvalid)
}

func TestSelectNoColumnsRejectsDistinctAndKnownWidthMismatches(t *testing.T) {
	t.Parallel()

	checkError(t, SelectNoColumns().Distinct(), ErrInvalid)
	checkError(t, SelectNoColumns().DistinctOn(LiteralInt(1)), ErrInvalid)
	checkError(t, Select(Scalar(SelectNoColumns())), ErrInvalid)
	checkError(t, Union(SelectNoColumns(), Select(LiteralInt(1))), ErrInvalid)
	checkError(t, InsertInto("target").Columns("value").From(SelectNoColumns()), ErrInvalid)
}

func TestSelectNoColumnsCanRenderWithRows(t *testing.T) {
	t.Parallel()

	checkSQL(t, SelectNoColumns().Where(False()), `SELECT  WHERE FALSE`)
	checkSQL(t, SelectNoColumns().FromExpr(Values(1).As("v", "value")), `SELECT  FROM (VALUES ($1)) AS "v" ("value")`, 1)
}
