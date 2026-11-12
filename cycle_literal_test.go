package qs

import "testing"

func cycleValuesQuery(mark, otherwise Expr) SelectBuilder {
	return Select("id").
		From("c").
		WithRecursive(
			CTE("c", Select(Param(1))).Columns("id").Cycle(
				Cycle("id").Set("seen").Values(mark, otherwise).Using("path"),
			),
		)
}

func TestCycleMarkConstants(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		mark, otherwise Expr
		constants       string
	}{
		{"integer", LiteralInt(1), LiteralInt(0), `1 DEFAULT 0`},
		{"float", LiteralFloat(1.25), LiteralFloat(0.25), `1.25 DEFAULT 0.25`},
		{"boolean", LiteralBool(true), LiteralBool(false), `TRUE DEFAULT FALSE`},
		{"null", NullLiteral(), NullLiteral(), `NULL DEFAULT NULL`},
		{"string", LiteralString(`a'\b`), LiteralString(""), `E'a''\\b' DEFAULT E''`},
		{"bit", LiteralBit("1"), LiteralHex("0"), `B'1' DEFAULT X'0'`},
		{"radix", LiteralNumeric("0x1"), LiteralNumeric("0b0"), `0x1 DEFAULT 0b0`},
		{"separators", LiteralNumeric("1_000"), LiteralNumeric("0"), `1_000 DEFAULT 0`},
		{"exponent_sign", LiteralNumeric("1e-3"), LiteralNumeric("0"), `1e-3 DEFAULT 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSQL(t, cycleValuesQuery(tc.mark, tc.otherwise),
				`WITH RECURSIVE "c" ("id") AS (SELECT $1) CYCLE "id" SET "seen" TO `+
					tc.constants+` USING "path" SELECT "id" FROM "c"`, 1)
		})
	}

	radix := cycleValuesQuery(LiteralNumeric("0x1"), LiteralNumeric("0"))
	if _, _, err := radix.ToSQLWith(Options{MaxDepth: 3}); err != nil {
		t.Fatalf("mark constant should fit the same depth as the CTE body: %v", err)
	}
}

func TestCycleRejectsNonConstants(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		expr Expr
	}{
		{"current_date", CurrentDate()},
		{"current_user", CurrentUser()},
		{"parameter", Param(1)},
		{"column", Col("id")},
		{"function", Call("f")},
		{"cast", LiteralString("1").Cast(TypeInt4)},
		{"group", LiteralInt(1).Parenthesized()},
		{"negative_integer", LiteralInt(-1)},
		{"negative_float", LiteralFloat(-0.5)},
		{"positive_sign", LiteralNumeric("+1")},
		{"negative_radix", LiteralNumeric("-0x1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkError(t, cycleValuesQuery(tc.expr, LiteralInt(0)), ErrInvalid)
			checkError(t, cycleValuesQuery(LiteralInt(1), tc.expr), ErrInvalid)
		})
	}
}
