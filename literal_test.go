package qs

import (
	"errors"
	"testing"
)

func TestExactPostgreSQLLiterals(t *testing.T) {
	t.Parallel()

	checkSQL(t, Select(LiteralNumeric("123456789012345678901234567890.123456789"), LiteralNumeric(".5"), LiteralNumeric("1."), LiteralNumeric("-1.25e+40")), `SELECT 123456789012345678901234567890.123456789, .5, 1., -1.25e+40`)
	checkSQL(t, Select(LiteralBit("00101"), LiteralBit(""), LiteralHex("Ab09")), `SELECT B'00101', B'', X'Ab09'`)
	checkSQL(t, Select(PrefixOperator("|/", LiteralInt(9)), PrefixOperator("||/", LiteralInt(27))), `SELECT (|/ 9), (||/ 27)`)
	for _, value := range []string{"", ".", "+", "1e", "1e+", "1; SELECT 2", "1/*x*/", " 1", "1 ", "NaN", "1.2.3", "1\x00", "١"} {
		if _, _, err := ExprToSQL(LiteralNumeric(value)); !errors.Is(err, ErrInvalid) {
			t.Errorf("numeric %q: %v", value, err)
		}
	}
	for _, expr := range []Expr{LiteralBit("102"), LiteralBit("0'"), LiteralHex("gg"), LiteralHex("A'; SELECT 1"), PrefixOperator("--", LiteralInt(1)), PrefixOperator("SELECT", LiteralInt(1))} {
		if _, _, err := ExprToSQL(expr); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid literal/operator accepted: %v", err)
		}
	}
}
