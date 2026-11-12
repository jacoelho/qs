package qs

import "testing"

func TestLiteralNumericAcceptedTokensPreserveSpelling(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		token string
	}{
		{"decimal_large", "123456789012345678901234567890.123456789"},
		{"decimal_leading_dot", ".5"},
		{"decimal_trailing_dot", "1."},
		{"decimal_signed_separators", "+12_345.67_89e-10_11"},
		{"binary", "0b101010"},
		{"binary_upper_signed", "-0B_1010"},
		{"octal", "0o_1_755"},
		{"hex", "0xDEAD_beef"},
		{"hex_e_digit", "0xE"},
		{"hex_huge", "0x10000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSQL(t, Select(LiteralNumeric(tc.token)), "SELECT "+tc.token)
		})
	}
}

func TestLiteralNumericRejectsMalformedTokens(t *testing.T) {
	t.Parallel()

	for _, token := range []string{
		"", ".", "+", "-", "1e", "1e+", "1e-",
		"0b", "0o", "0x", "0B_", "0O_", "0X_",
		"0b102", "0o8", "0xG", "0x1G", "0b_", "0o_", "0x_",
		"0b1010_", "0o_1_755_", "0xdead__beef", "1__2", "1_",
		"_1", "1_.2", "1._2", "1.2__3", "1.2_", "1e__2", "1e2_", "1e+_2", "1e_2",
		"1..2", "1.2.3", "1e2e3", "1e+2+3", "1+2", "1 2",
		"1; SELECT 2", "1/*x*/", "1--x", "'1'", `"1"`, " 1", "1 ", "1\x00", "١",
	} {
		t.Run(token, func(t *testing.T) {
			t.Parallel()

			checkError(t, Select(LiteralNumeric(token)), ErrInvalid)
		})
	}
}

func TestDerivedRelations(t *testing.T) {
	t.Parallel()

	inner := Select(Param("inner")).Where(Col("inner_key").Eq(Param(1)))
	outer := Select(Param("outer")).From(Derived(inner)).Where(Col("outer_key").Eq(Param(2)))
	checkSQL(t, outer, `SELECT $1 FROM (SELECT $2 WHERE ("inner_key" = $3)) WHERE ("outer_key" = $4)`, "outer", "inner", 1, 2)

	aliased := Derived(Select(Param(1))).As("d", "value")
	checkSQL(t, Select(Col("d", "value")).From(aliased), `SELECT "d"."value" FROM (SELECT $1) AS "d" ("value")`, 1)
	checkError(t, Select(Star()).From(Derived(Select(Param(1))).As("d", "a", "b")), ErrInvalid)
	checkError(t, Select(Star()).From(Subquery(Select(Param(1)), "")), ErrInvalid)
	checkError(t, Select(Star()).From(Derived(nil)), ErrInvalid)
	checkError(t, Select(Derived(Select(Param(1))).Col("value")), ErrInvalid)

	checkSQL(t, Select(Star()).From(Derived(Select(Param(1)))), `SELECT * FROM (SELECT $1)`, 1)
	checkSQL(t, Select(Star()).From(aliased), `SELECT * FROM (SELECT $1) AS "d" ("value")`, 1)
}

func TestDerivedRelationKeepsTheQueryItWasBuiltFrom(t *testing.T) {
	t.Parallel()

	child := Select(Param(1))
	query := Select(Star()).From(Derived(child))
	widened := Select(Star()).From(Derived(child.Columns(Param(2))))

	checkSQL(t, query, `SELECT * FROM (SELECT $1)`, 1)
	checkSQL(t, widened, `SELECT * FROM (SELECT $1, $2)`, 1, 2)
}
