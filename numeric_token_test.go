package qs

import (
	"errors"
	"testing"
)

func TestLiteralNumericAcceptedTokensPreserveSpelling(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		token   string
		gated16 bool
	}{
		{"decimal_large", "123456789012345678901234567890.123456789", false},
		{"decimal_leading_dot", ".5", false},
		{"decimal_trailing_dot", "1.", false},
		{"decimal_signed_separators", "+12_345.67_89e-10_11", true},
		{"binary", "0b101010", true},
		{"binary_upper_signed", "-0B_1010", true},
		{"octal", "0o_1_755", true},
		{"hex", "0xDEAD_beef", true},
		{"hex_e_digit", "0xE", true},
		{"hex_huge", "0x10000000000000000", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSQL(t, Select(LiteralNumeric(tc.token)), "SELECT "+tc.token)

			for _, version := range []PostgreSQLVersion{PostgreSQL15, PostgreSQL16} {
				sql, args, err := ToSQLWith(Select(LiteralNumeric(tc.token)), Options{PostgreSQL: version})
				if tc.gated16 && version == PostgreSQL15 {
					if !errors.Is(err, ErrUnsupported) || sql != "" || args != nil {
						t.Fatalf("PostgreSQL 15 accepted %q: %q %#v %v", tc.token, sql, args, err)
					}
					continue
				}
				if err != nil || sql != "SELECT "+tc.token || args != nil {
					t.Fatalf("PostgreSQL %d rendered %q as %q %#v: %v", version, tc.token, sql, args, err)
				}
			}
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

	inner := Select(Param("inner")).Where(Eq("inner_key", 1))
	outer := Select(Param("outer")).FromExpr(Derived(inner)).Where(Eq("outer_key", 2))
	checkSQL(t, outer, `SELECT $1 FROM (SELECT $2 WHERE ("inner_key" = $3)) WHERE ("outer_key" = $4)`, "outer", "inner", 1, 2)

	aliased := Derived(Select(Param(1))).As("d", "value")
	checkSQL(t, Select(Col("d.value")).FromExpr(aliased), `SELECT "d"."value" FROM (SELECT $1) AS "d" ("value")`, 1)
	checkError(t, Select(Star()).FromExpr(Derived(Select(Param(1))).As("d", "a", "b")), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(Subquery(Select(Param(1)), "")), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(Derived(nil)), ErrInvalid)
	checkError(t, Select(Derived(Select(Param(1))).Col("value")), ErrInvalid)

	for _, version := range []PostgreSQLVersion{PostgreSQL15, PostgreSQL16} {
		sql, args, err := ToSQLWith(Select(Star()).FromExpr(Derived(Select(Param(1)))), Options{PostgreSQL: version})
		if version == PostgreSQL15 {
			if !errors.Is(err, ErrUnsupported) || sql != "" || args != nil {
				t.Fatalf("PostgreSQL 15 accepted unaliased derived relation: %q %#v %v", sql, args, err)
			}
		} else if err != nil || sql != `SELECT * FROM (SELECT $1)` || len(args) != 1 || args[0] != 1 {
			t.Fatalf("PostgreSQL 16 derived relation: %q %#v %v", sql, args, err)
		}
	}

	aliasedSQL, args, err := ToSQLWith(Select(Star()).FromExpr(aliased), Options{PostgreSQL: PostgreSQL15})
	if err != nil || aliasedSQL != `SELECT * FROM (SELECT $1) AS "d" ("value")` || len(args) != 1 || args[0] != 1 {
		t.Fatalf("PostgreSQL 15 aliased derived relation: %q %#v %v", aliasedSQL, args, err)
	}
}

func TestDerivedCloneOwnsChild(t *testing.T) {
	t.Parallel()

	child := Select(Param(1))
	query := Select(Star()).FromExpr(Derived(child))
	clone := query.Clone()
	child.Columns(Param(2))

	checkSQL(t, clone, `SELECT * FROM (SELECT $1)`, 1)
	checkSQL(t, query, `SELECT * FROM (SELECT $1, $2)`, 1, 2)
}
