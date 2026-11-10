package qs

import "testing"

func TestSelectNoColumnsRendersWhileEmptySelectFails(t *testing.T) {
	t.Parallel()

	checkSQL(t, SelectNoColumns(), `SELECT `)
	checkSQL(t, SelectNoColumns().Where(True()), `SELECT  WHERE TRUE`)
	checkError(t, SelectSlice(nil), ErrInvalid)
	checkError(t, SelectBuilder{}, ErrInvalid)
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
