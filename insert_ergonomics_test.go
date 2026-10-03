package qs

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestInsertSlicesCaptureStructureAndRetainLiveChildren(t *testing.T) {
	t.Parallel()
	columns := []string{"a.b", `a"b`}
	child := Select(Param("original"))
	row := []WriteValue{Write(Scalar(child)), Value("retained")}
	target := InsertInto("items").ColumnsSlice(columns)
	columns[0] = "wrong"
	first := target.ValuesSlice(row)
	second := target.ValuesSlice(row)
	first.ValuesSlice([]WriteValue{Value("later"), Value(nil)})
	row[0], row[1] = Value("replaced"), Value("replaced")
	child.RemoveColumns().Columns(Param("live"))
	checkSQL(t, first, `INSERT INTO "items" ("a.b", "a""b") VALUES ((SELECT $1), $2), ($3, $4)`, "live", "retained", "later", nil)
	checkSQL(t, second, `INSERT INTO "items" ("a.b", "a""b") VALUES ((SELECT $1), $2)`, "live", "retained")
	positional := []WriteValue{Value(nil)}
	statement := InsertInto("items").ValuesSlice(positional)
	positional[0] = Value("changed")
	checkSQL(t, statement, `INSERT INTO "items" VALUES ($1)`, nil)
}

func TestInsertSlicesRejectEmptySelectionsAndRows(t *testing.T) {
	t.Parallel()
	for _, columns := range [][]string{nil, {}} {
		target := InsertInto("items").ColumnsSlice(columns)
		for _, statement := range []Statement{
			target.Values(Value(1)), target.From(Select(Star())), target.DefaultValues(),
		} {
			for _, query := range []Statement{statement, Clone(statement)} {
				_, _, err := ToSQL(query)
				var renderErr *RenderError
				if !errors.As(err, &renderErr) || renderErr.Clause != "INSERT" || renderErr.Detail != "requires at least one target column" {
					t.Fatalf("empty target: %v", err)
				}
				checkError(t, query, ErrInvalid)
			}
		}
	}
	for _, row := range [][]WriteValue{nil, {}} {
		for _, statement := range []*InsertRows{
			InsertInto("items").ValuesSlice(row).Values(Value(1)),
			InsertInto("items").Columns("a").ValuesSlice(row).Values(Value(1)),
			InsertInto("items").Values(Value(1)).ValuesSlice(row).Values(Value(2)),
		} {
			checkError(t, statement, ErrInvalid)
			checkError(t, statement.Clone(), ErrInvalid)
		}
	}
	checkSQL(t, InsertInto("items").DefaultValues(), `INSERT INTO "items" DEFAULT VALUES`)
	checkSQL(t, InsertInto("items").From(Select(Param(1))), `INSERT INTO "items" SELECT $1`, 1)
}

func TestRowWidthDiagnostics(t *testing.T) {
	t.Parallel()
	const secret = "secret-bound-value"
	cases := []struct {
		name           string
		query          Statement
		clause, detail string
	}{
		{"target width", InsertInto("items").Columns("a", "b").Values(Value(secret)), "INSERT", "row 1: expected 2 values, got 1 value"},
		{"insert rows", InsertInto("items").Values(Value(secret)).Values(Value(2), Value(3)), "INSERT", "row 2: expected 1 value, got 2 values"},
		{"values rows", Values(secret).Row(2, 3), "VALUES", "row 2: expected 1 value, got 2 values"},
		{"select width", InsertInto("items").Columns("a", "b").From(Select(Param(secret), Param(2), Param(3))), "INSERT SELECT", "expected 2 target columns, got 3 projected columns"},
		{"no rows", Values(), "VALUES", "requires at least one row"},
		{"empty first", Values().Row(), "VALUES", "row 1: expected at least 1 value, got 0"},
		{"empty later", Values(secret).Row(), "VALUES", "row 2: expected at least 1 value, got 0"},
		{"empty insert", InsertInto("items").Values(Value(secret)).ValuesSlice(nil), "INSERT", "row 2: expected at least 1 value, got 0"},
		{"unknown values", Values(secret).RowExpr(UnsafeSQL("1")).Row(2, 3), "VALUES", "row 3: expected 1 value, got 2 values"},
		{"unknown insert", InsertInto("items").Values(Value(secret)).Values(Write(UnsafeSQL("1"))).Values(Value(2), Value(3)), "INSERT", "row 3: expected 1 value, got 2 values"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := AppendSQL([]byte("prefix "), []any{"existing"}, tc.query)
			var renderErr *RenderError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &renderErr) {
				t.Fatalf("expected invalid RenderError, got %v", err)
			}
			if renderErr.Clause != tc.clause || renderErr.Detail != tc.detail || strings.Contains(err.Error(), secret) {
				t.Fatalf("diagnostic = %v; want %s: %s", err, tc.clause, tc.detail)
			}
			if string(sql) != "prefix " || !reflect.DeepEqual(args, []any{"existing"}) {
				t.Fatalf("render leaked output: %q %#v", sql, args)
			}
		})
	}
	checkSQL(t, Values(1).RowExpr(UnsafeSQL("2")).Row(3), `VALUES ($1), (2), ($2)`, 1, 3)
	checkSQL(t, InsertInto("items").Columns("a", "b").Values(Write(UnsafeSQL("1, 2"))), `INSERT INTO "items" ("a", "b") VALUES (1, 2)`)
}
