package qs

import (
	"reflect"
	"testing"
)

func TestSharedParamBindsOnceAcrossUses(t *testing.T) {
	t.Parallel()
	tenant := SharedParam("acme")
	other := SharedParam("acme")
	inner := Select(Col("id")).From("members").Where(Col("tenant").Eq(tenant))
	runCases(t, []renderCase{
		{"every_use_repeats_the_number", Select(tenant.As("t")).From("users").Where(Col("tenant").Eq(tenant), Col("owner").Ne(tenant)),
			`SELECT $1 AS "t" FROM "users" WHERE ("tenant" = $1) AND ("owner" <> $1)`, []any{"acme"}},
		{"numbered_at_first_use", Select(Col("id")).From("users").Where(Col("age").Gt(Param(18)), Col("tenant").Eq(tenant), Col("n").Lt(Param(7)), Col("owner").Eq(tenant)),
			`SELECT "id" FROM "users" WHERE ("age" > $1) AND ("tenant" = $2) AND ("n" < $3) AND ("owner" = $2)`, []any{18, "acme", 7}},
		{"shared_with_a_subquery", Select(Col("id")).From("users").Where(Col("tenant").Eq(tenant), Col("id").InQuery(inner)),
			`SELECT "id" FROM "users" WHERE ("tenant" = $1) AND ("id" IN (SELECT "id" FROM "members" WHERE ("tenant" = $1)))`, []any{"acme"}},
		{"each_call_is_its_own_parameter", Select(Col("id")).From("users").Where(Col("tenant").Eq(tenant), Col("owner").Eq(other)),
			`SELECT "id" FROM "users" WHERE ("tenant" = $1) AND ("owner" = $2)`, []any{"acme", "acme"}},
		{"param_still_binds_at_each_use", Select(Col("id")).From("users").Where(Col("a").Eq(Param(1)), Col("b").Eq(Param(1))),
			`SELECT "id" FROM "users" WHERE ("a" = $1) AND ("b" = $2)`, []any{1, 1}},
		{"cast_applies_per_use", Select(tenant.Cast(TypeText)).From("users").Where(Col("tenant").Eq(tenant)),
			`SELECT ($1)::text FROM "users" WHERE ("tenant" = $1)`, []any{"acme"}},
		{"in_list", Select(Col("id")).From("users").Where(Col("a").In(tenant, Param("x"), tenant)),
			`SELECT "id" FROM "users" WHERE ("a" IN ($1, $2, $1))`, []any{"acme", "x"}},
	})
}

func TestSharedParamNumbersAfterAppendPrefix(t *testing.T) {
	t.Parallel()
	v := SharedParam(5)
	q := Select(Col("id")).From("t").Where(Col("a").Eq(v), Col("b").Eq(v))
	buf, args, err := AppendSQL([]byte("prefix;"), []any{"p1", "p2"}, q)
	want := `prefix;SELECT "id" FROM "t" WHERE ("a" = $3) AND ("b" = $3)`
	if err != nil || string(buf) != want || !reflect.DeepEqual(args, []any{"p1", "p2", 5}) {
		t.Fatalf("AppendSQL: SQL=%q args=%#v err=%v", buf, args, err)
	}
}

// A statement holding a SharedParam renders the same in every render: the
// numbering belongs to the render, not to the expression.
func TestSharedParamRendersTheSameInEveryStatement(t *testing.T) {
	t.Parallel()
	v := SharedParam(5)
	first := Select(Col("id")).From("t").Where(Col("x").Eq(Param(1)), Col("a").Eq(v))
	second := Select(Col("id")).From("u").Where(Col("a").Eq(v), Col("b").Eq(v))
	checkSQL(t, first, `SELECT "id" FROM "t" WHERE ("x" = $1) AND ("a" = $2)`, 1, 5)
	checkSQL(t, second, `SELECT "id" FROM "u" WHERE ("a" = $1) AND ("b" = $1)`, 5)
	checkSQL(t, first, `SELECT "id" FROM "t" WHERE ("x" = $1) AND ("a" = $2)`, 1, 5)
}

// More shared parameters than the renderer keeps inline still resolve.
func TestSharedParamManyDistinct(t *testing.T) {
	t.Parallel()
	columns := make([]Expr, 0, 12)
	want := make([]any, 0, 6)
	shared := make([]Expr, 6)
	for i := range shared {
		shared[i] = SharedParam(i)
		want = append(want, i)
	}
	columns = append(columns, shared...)
	columns = append(columns, shared...)
	checkSQL(t, SelectSlice(columns), `SELECT $1, $2, $3, $4, $5, $6, $1, $2, $3, $4, $5, $6`, want...)
}

func TestSharedParamCountsOnceTowardsParameterLimit(t *testing.T) {
	t.Parallel()
	v := SharedParam(1)
	q := Select(v, v, v)
	if _, args, err := ToSQLWith(q, Options{MaxParameters: 1}); err != nil || len(args) != 1 {
		t.Fatalf("args=%v err=%v, want one argument within a limit of one", args, err)
	}
	checkErrorWithOptions(t, Select(v, Param(2)), Options{MaxParameters: 1}, ErrParameterLimit)
}

// What Param refuses to bind or compare, SharedParam refuses too.
func TestSharedParamRejectsWhatParamRejects(t *testing.T) {
	t.Parallel()
	for name, statement := range map[string]Statement{
		"expression value":      Select(SharedParam(Col("a"))),
		"qs.Null value":         Select(SharedParam(NullOf[int]())),
		"untyped nil compared":  Select(Col("id")).From("t").Where(Col("a").Eq(SharedParam(nil))),
		"untyped nil in a list": Select(Col("id")).From("t").Where(Col("a").In(SharedParam(nil))),
		"bound keyset key":      Select(Col("id")).From("t").OrderBy(SharedParam(1).Asc()).SeekAfter(1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
	checkSQL(t, Select(SharedParam(nil).Cast(TypeText)), `SELECT ($1)::text`, nil)
}

func TestSeekAfterCursorForms(t *testing.T) {
	t.Parallel()
	page := func() SelectBuilder {
		return Select(Col("id")).From("t").OrderBy(Col("t", "a").Asc(), Col("t", "id").Desc())
	}
	var none []any
	runCases(t, []renderCase{
		{"no_cursor_reads_the_first_page", page().SeekAfter(none...),
			`SELECT "id" FROM "t" ORDER BY "t"."a" ASC, "t"."id" DESC`, nil},
		{"empty_cursor_replaces_an_earlier_one", page().SeekAfter(1, 2).SeekAfter(),
			`SELECT "id" FROM "t" ORDER BY "t"."a" ASC, "t"."id" DESC`, nil},
		{"spread_slice", Select(Col("id")).From("t").OrderBy(Col("t", "a").Asc(), Col("t", "id").Asc()).SeekAfter([]any{1, 2}...),
			`SELECT "id" FROM "t" WHERE (("t"."a", "t"."id") > ($1, $2)) ORDER BY "t"."a" ASC, "t"."id" ASC`, []any{1, 2}},
		{"expression_cursor_is_compared_as_written", Select(Col("id")).From("t").OrderBy(Col("t", "id").Asc()).SeekAfter(Param("7f").Cast(TypeUUID)),
			`SELECT "id" FROM "t" WHERE ("t"."id" > ($1)::uuid) ORDER BY "t"."id" ASC`, []any{"7f"}},
		{"expression_cursor_binds_at_each_use", page().SeekAfter(Param(1).Cast(TypeInt8), 2),
			`SELECT "id" FROM "t" WHERE (("t"."a" >= ($1)::bigint) AND (("t"."a" > ($2)::bigint) OR (("t"."a" = ($3)::bigint) AND ("t"."id" < $4)))) ORDER BY "t"."a" ASC, "t"."id" DESC`, []any{1, 1, 1, 2}},
		{"shared_expression_cursor_binds_once", page().SeekAfter(SharedParam(1).Cast(TypeInt8), 2),
			`SELECT "id" FROM "t" WHERE (("t"."a" >= ($1)::bigint) AND (("t"."a" > ($1)::bigint) OR (("t"."a" = ($1)::bigint) AND ("t"."id" < $2)))) ORDER BY "t"."a" ASC, "t"."id" DESC`, []any{1, 2}},
		{"trusted_cursor_is_one_operand", Select(Col("id")).From("t").OrderBy(Col("t", "id").Asc()).SeekAfter(UnsafeSQL("a OR b")),
			`SELECT "id" FROM "t" WHERE ("t"."id" > (a OR b)) ORDER BY "t"."id" ASC`, nil},
	})
	for name, statement := range map[string]Statement{
		"unspread slice":         Select(Col("id")).From("t").OrderBy(Col("t", "id").Asc()).SeekAfter([]any{1}),
		"untyped nil":            Select(Col("id")).From("t").OrderBy(Col("t", "id").Asc()).SeekAfter(nil),
		"untyped nil expression": Select(Col("id")).From("t").OrderBy(Col("t", "id").Asc()).SeekAfter(Param(nil)),
		"cursor without order":   Select(Col("id")).From("t").SeekAfter(1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}
