package qs

import (
	"reflect"
	"testing"
)

func TestParenthesizedIndirection(t *testing.T) {
	t.Parallel()

	base := Col("a").Index(Param(1))
	query := Select(base.Index(Param(2)), base.Parenthesized().Index(Param(3)), base.Parenthesized().Field("f"))
	for _, tc := range []struct {
		style PlaceholderStyle
		sql   string
	}{
		{Dollar, `SELECT ("a")[$1][$2], (("a")[$3])[$4], (("a")[$5])."f"`},
		{Question, `SELECT ("a")[?][?], (("a")[?])[?], (("a")[?])."f"`},
	} {
		sql, args, err := query.ToSQLWith(Options{PlaceholderStyle: tc.style})
		if err != nil || sql != tc.sql || !reflect.DeepEqual(args, []any{1, 2, 1, 3, 1}) {
			t.Fatalf("grouping: %q %#v %v", sql, args, err)
		}
	}
	checkSQL(t, Select(base), `SELECT ("a")[$1]`, 1)
	checkSQL(t, Select(base.Parenthesized().Parenthesized().Index(LiteralInt(2))), `SELECT ((("a")[$1]))[2]`, 1)
}

func TestParenthesizedProjectionAndClone(t *testing.T) {
	t.Parallel()

	expanded := Select(Col("person").Fields().Parenthesized()).From("people")
	checkSQL(t, Select(Star()).FromExpr(expanded.As("p", "first", "last")), `SELECT * FROM (SELECT (("person").*) FROM "people") AS "p" ("first", "last")`)
	checkSQL(t, InsertInto("names").Columns("first", "last").From(expanded), `INSERT INTO "names" ("first", "last") SELECT (("person").*) FROM "people"`)
	checkError(t, Select(Star()).FromExpr(Select(Param(1).Parenthesized()).As("p", "first", "last")), ErrInvalid)

	child := Select(Param("before"))
	query := Select(Scalar(child).Parenthesized())
	cloned := Clone(query)
	child.RemoveColumns().Columns(Param("after"))
	checkSQL(t, query, `SELECT ((SELECT $1))`, "after")
	checkSQL(t, cloned, `SELECT ((SELECT $1))`, "before")
}

func TestFunctionRelationCastGrammar(t *testing.T) {
	t.Parallel()

	query := Select(Param("projection"), Param(4).Cast(Int4)).FromExpr(
		TableFunc(Param(5).Cast(Int4)).As("direct", "v"),
		RowsFrom(Function(Param(6).Cast(Int8)), Function(Call("generate_series", Param(7), Param(8)))).As("rows", "v", "n"),
	)
	for _, tc := range []struct {
		style PlaceholderStyle
		sql   string
	}{
		{Dollar, `SELECT $1, ($2)::integer FROM CAST($3 AS integer) AS "direct" ("v"), ROWS FROM (CAST($4 AS bigint), "generate_series"($5, $6)) AS "rows" ("v", "n")`},
		{Question, `SELECT ?, (?)::integer FROM CAST(? AS integer) AS "direct" ("v"), ROWS FROM (CAST(? AS bigint), "generate_series"(?, ?)) AS "rows" ("v", "n")`},
	} {
		sql, args, err := query.ToSQLWith(Options{PlaceholderStyle: tc.style})
		if err != nil || sql != tc.sql || !reflect.DeepEqual(args, []any{"projection", 4, 5, 6, 7, 8}) {
			t.Fatalf("FROM cast: %q %#v %v", sql, args, err)
		}
	}
	checkSQL(t, Select(Star()).FromExpr(TableFunc(Param(1).Cast(Int4).Cast(Int8))), `SELECT * FROM CAST(($1)::integer AS bigint)`, 1)
}

func TestGrammarBoundaryDepth(t *testing.T) {
	t.Parallel()

	grouped := Param(1)
	for range 4 {
		grouped = grouped.Parenthesized()
	}
	checkErrorWithOptions(t, Select(grouped), Options{MaxDepth: 5}, ErrDepth)
	checkSQL(t, Select(grouped), `SELECT (((($1))))`, 1)
	cast := Select(Star()).FromExpr(TableFunc(Param(1).Cast(Int4)))
	checkErrorWithOptions(t, cast, Options{MaxDepth: 3}, ErrDepth)
	if sql, args, err := cast.ToSQLWith(Options{MaxDepth: 4}); err != nil || sql != `SELECT * FROM CAST($1 AS integer)` || !reflect.DeepEqual(args, []any{1}) {
		t.Fatalf("FROM cast at depth boundary: %q %#v %v", sql, args, err)
	}
}
