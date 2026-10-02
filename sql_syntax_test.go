package qs

import (
	"reflect"
	"testing"
)

func TestSQLSyntaxExpressions(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{
			"substring_from_for",
			Select(SubstringFrom(Param("abcdef"), Param(2), Param(3))),
			`SELECT SUBSTRING($1 FROM $2 FOR $3)`,
			[]any{"abcdef", 2, 3},
		},
		{
			"substring_from",
			Select(SubstringFrom(Param("abcdef"), Param(2))),
			`SELECT SUBSTRING($1 FROM $2)`,
			[]any{"abcdef", 2},
		},
		{
			"substring_for",
			Select(SubstringFor(Param("abcdef"), Param(3))),
			`SELECT SUBSTRING($1 FOR $2)`,
			[]any{"abcdef", 3},
		},
		{
			"substring_similar",
			Select(SubstringSimilar(Param("abcdef"), Param("%b%"), Param("\\"))),
			`SELECT SUBSTRING($1 SIMILAR $2 ESCAPE $3)`,
			[]any{"abcdef", "%b%", "\\"},
		},
		{
			"ordinary_substring_preserved",
			Select(Substring(Param("abcdef"), Param(2), Param(3))),
			`SELECT substr($1, $2, $3)`,
			[]any{"abcdef", 2, 3},
		},
		{
			"position_sql_operand_order",
			Select(Position(Param("bc"), Param("abc"))),
			`SELECT POSITION($1 IN $2)`,
			[]any{"bc", "abc"},
		},
		{
			"normalize_default",
			Select(Normalize(Param("e\u0301"))),
			`SELECT NORMALIZE($1)`,
			[]any{"e\u0301"},
		},
		{
			"normalize_form",
			Select(Normalize(Param("e\u0301"), NFC)),
			`SELECT NORMALIZE($1, NFC)`,
			[]any{"e\u0301"},
		},
		{
			"overlay_for",
			Select(Overlay(Param("abcdef"), Param("XY"), Param(2), Param(2))),
			`SELECT OVERLAY($1 PLACING $2 FROM $3 FOR $4)`,
			[]any{"abcdef", "XY", 2, 2},
		},
		{
			"overlay_default_length",
			Select(Overlay(Param("abcdef"), Param("XY"), Param(2))),
			`SELECT OVERLAY($1 PLACING $2 FROM $3)`,
			[]any{"abcdef", "XY", 2},
		},
		{
			"overlaps",
			Select(Overlaps(Param(1), Param(2), Param(3), Param(4)).Expr()),
			`SELECT (($1, $2) OVERLAPS ($3, $4))`,
			[]any{1, 2, 3, 4},
		},
		{
			"at_local",
			Select(AtLocal(Param("2026-01-01 00:00:00"))),
			`SELECT ($1 AT LOCAL)`,
			[]any{"2026-01-01 00:00:00"},
		},
		{
			"trim_leading",
			Select(TrimSyntax(Param("xxvalue"), TrimLeadingDirection, Param("x"))),
			`SELECT TRIM(LEADING $1 FROM $2)`,
			[]any{"x", "xxvalue"},
		},
		{
			"trim_trailing_default_character",
			Select(TrimSyntax(Param("valuexx"), TrimTrailingDirection)),
			`SELECT TRIM(TRAILING FROM $1)`,
			[]any{"valuexx"},
		},
		{
			"trim_both",
			Select(TrimSyntax(Param(" value "), TrimBothDirection)),
			`SELECT TRIM(BOTH FROM $1)`,
			[]any{" value "},
		},
		{
			"collation_for",
			Select(CollationFor(Col("value"))),
			`SELECT COLLATION FOR ("value")`,
			nil,
		},
		{
			"is_normalized",
			Select(IsNormalized(Param("value")).Expr()),
			`SELECT ($1 IS NORMALIZED)`,
			[]any{"value"},
		},
		{
			"is_not_nfd_normalized",
			Select(IsNotNormalized(Param("value"), NFD).Expr()),
			`SELECT ($1 IS NOT NFD NORMALIZED)`,
			[]any{"value"},
		},
	})
}

func TestSQLValueFunctions(t *testing.T) {
	t.Parallel()

	checkSQL(t,
		Select(CurrentTime(3), CurrentTimestamp(6), LocalTime(), LocalTimestamp(2), SessionUser(), CurrentSchema(), CurrentCatalog()),
		`SELECT CURRENT_TIME(3), CURRENT_TIMESTAMP(6), LOCALTIME, LOCALTIMESTAMP(2), SESSION_USER, CURRENT_SCHEMA, CURRENT_CATALOG`,
	)
	checkSQL(t, Select(CurrentDate(), CurrentUser()), `SELECT CURRENT_DATE, CURRENT_USER`)
}

func TestExtractNamedAliases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		part string
		want string
	}{
		{"microsecond", "microsecond", "microsecond"},
		{"microsec", "microsec", "microsec"},
		{"millisecond", "millisecond", "millisecond"},
		{"seconds", "seconds", "seconds"},
		{"timezone_hour_alias", "timezone_h", "timezone_h"},
		{"timezone_minute_alias", "timezone_m", "timezone_m"},
		{"julian", "julian", "julian"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSQL(t, Select(ExtractNamed(tc.part, LiteralString("2026-01-01"))),
				`SELECT EXTRACT(`+tc.want+` FROM E'2026-01-01')`)
		})
	}
	checkError(t, Select(ExtractNamed("year) FROM 1; DROP TABLE x; --", Param(1))), ErrInvalid)
	checkError(t, Select(Param("earlier"), ExtractNamed("fortnight", Param("value"))), ErrInvalid)
}

func TestSQLSyntaxPlaceholderStyles(t *testing.T) {
	t.Parallel()

	query := Select(Position(Param("needle"), Param("haystack")), Normalize(Param("value"), NFKC))
	wantArgs := []any{"needle", "haystack", "value"}
	for _, tc := range []struct {
		name  string
		style PlaceholderStyle
		sql   string
	}{
		{"dollar", Dollar, `SELECT POSITION($1 IN $2), NORMALIZE($3, NFKC)`},
		{"question", Question, `SELECT POSITION(? IN ?), NORMALIZE(?, NFKC)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotSQL, gotArgs, err := query.ToSQLWith(Options{PlaceholderStyle: tc.style})
			if err != nil {
				t.Fatal(err)
			}
			if gotSQL != tc.sql || !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("SQL=%s args=%#v; want %s %#v", gotSQL, gotArgs, tc.sql, wantArgs)
			}
		})
	}
}

func TestSQLSyntaxCloneChildren(t *testing.T) {
	t.Parallel()

	original := Select(SubstringFrom(Param("original"), LiteralInt(1)))
	cloned := Clone(original)
	syntax, ok := cloned.columns[0].value.(*sqlSyntaxExpression)
	if !ok {
		t.Fatalf("cloned expression has type %T, want *sqlSyntaxExpression", cloned.columns[0].value)
	}
	syntax.args[0] = Param("clone")

	checkSQL(t, original, `SELECT SUBSTRING($1 FROM 1)`, "original")
	checkSQL(t, cloned, `SELECT SUBSTRING($1 FROM 1)`, "clone")
}

func TestSQLSyntaxConstructionErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		q    Statement
	}{
		{"substring_length_arity", Select(SubstringFrom(Param("x"), Param(1), Param(2), Param(3)))},
		{"normalize_form", Select(Normalize(Param("x"), UnicodeNormalForm(99)))},
		{"normalize_form_arity", Select(Normalize(Param("x"), NFC, NFD))},
		{"trim_direction", Select(TrimSyntax(Param("x"), TrimDirection(99)))},
		{"timestamp_precision_low", Select(CurrentTimestamp(-1))},
		{"timestamp_precision_high", Select(CurrentTimestamp(7))},
		{"time_precision_arity", Select(CurrentTime(1, 2))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.q, ErrInvalid)
		})
	}
}
