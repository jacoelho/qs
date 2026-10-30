package qs

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestSeekAfterRendersKeysetPredicate(t *testing.T) {
	t.Parallel()
	page := func() *SelectBuilder { return Select(Col("id")).From("t") }
	runCases(t, []renderCase{
		{"single_asc", page().OrderBy(Asc("id")).SeekAfter(5),
			`SELECT "id" FROM "t" WHERE ("id" > $1) ORDER BY "id" ASC`, []any{5}},
		{"single_desc", page().OrderBy(Desc("id")).SeekAfter(5),
			`SELECT "id" FROM "t" WHERE ("id" < $1) ORDER BY "id" DESC`, []any{5}},
		{"uniform_asc_after_where_before_limit", page().Where(Eq("tenant", 7)).OrderBy(Asc("a"), Asc("id")).SeekAfter(3, 9).Limit(10),
			`SELECT "id" FROM "t" WHERE ("tenant" = $1) AND (("a", "id") > ($2, $3)) ORDER BY "a" ASC, "id" ASC LIMIT $4`, []any{7, 3, 9, 10}},
		{"uniform_desc_three_keys", page().OrderBy(Desc("a"), Desc("b"), Desc("id")).SeekAfter("x", 2.5, int64(9)),
			`SELECT "id" FROM "t" WHERE (("a", "b", "id") < ($1, $2, $3)) ORDER BY "a" DESC, "b" DESC, "id" DESC`, []any{"x", 2.5, int64(9)}},
		{"mixed_leading_asc", page().OrderBy(Asc("a"), Desc("id")).SeekAfter(1, 2),
			`SELECT "id" FROM "t" WHERE (("a" >= $1) AND (("a" > $2) OR (("a" = $3) AND ("id" < $4)))) ORDER BY "a" ASC, "id" DESC`, []any{1, 1, 1, 2}},
		{"mixed_leading_desc", page().OrderBy(Desc("a"), Asc("id")).SeekAfter(1, 2),
			`SELECT "id" FROM "t" WHERE (("a" <= $1) AND (("a" < $2) OR (("a" = $3) AND ("id" > $4)))) ORDER BY "a" DESC, "id" ASC`, []any{1, 1, 1, 2}},
		{"mixed_three_keys", page().OrderBy(Asc("a"), Desc("b"), Asc("id")).SeekAfter(1, 2, 3),
			`SELECT "id" FROM "t" WHERE (("a" >= $1) AND (("a" > $2) OR (("a" = $3) AND ("b" < $4)) OR (("a" = $5) AND ("b" = $6) AND ("id" > $7)))) ORDER BY "a" ASC, "b" DESC, "id" ASC`, []any{1, 1, 1, 2, 1, 2, 3}},
		{"expression_key", page().OrderBy(Lower(Col("name")).Asc(), Asc("id")).SeekAfter("ann", 4),
			`SELECT "id" FROM "t" WHERE ((lower("name"), "id") > ($1, $2)) ORDER BY lower("name") ASC, "id" ASC`, []any{"ann", 4}},
		{"cursor_follows_later_where", page().OrderBy(Asc("id")).SeekAfter(5).Where(Eq("tenant", 7)),
			`SELECT "id" FROM "t" WHERE ("tenant" = $1) AND ("id" > $2) ORDER BY "id" ASC`, []any{7, 5}},
		{"distinct", Select(Col("a")).Distinct().From("t").OrderBy(Asc("a")).SeekAfter(1),
			`SELECT DISTINCT "a" FROM "t" WHERE ("a" > $1) ORDER BY "a" ASC`, []any{1}},
		{"group_by", Select(Col("a"), CountAll()).From("t").GroupBy("a").OrderBy(Asc("a")).SeekAfter(1),
			`SELECT "a", count(*) FROM "t" WHERE ("a" > $1) GROUP BY "a" ORDER BY "a" ASC`, []any{1}},
		{"alias_of_same_column", Select(Col("x").As("x")).From("t").OrderBy(Asc("x")).SeekAfter(1),
			`SELECT "x" AS "x" FROM "t" WHERE ("x" > $1) ORDER BY "x" ASC`, []any{1}},
		{"alias_differs_in_case", Select(Col("a").As("X")).From("t").OrderBy(Asc("x")).SeekAfter(1),
			`SELECT "a" AS "X" FROM "t" WHERE ("x" > $1) ORDER BY "x" ASC`, []any{1}},
		{"qualified_key_beside_alias", Select(Col("t.y").As("x")).From("t").OrderBy(Asc("t.x")).SeekAfter(1),
			`SELECT "t"."y" AS "x" FROM "t" WHERE ("t"."x" > $1) ORDER BY "t"."x" ASC`, []any{1}},
		{"alias_without_cursor", Select(Col("a").As("x")).From("t").OrderBy(Asc("x")),
			`SELECT "a" AS "x" FROM "t" ORDER BY "x" ASC`, nil},
	})
}

func TestSeekAfterPlaceholderStylesAndPrefixes(t *testing.T) {
	t.Parallel()
	q := Select(Col("id")).From("t").OrderBy(Asc("a"), Desc("id")).SeekAfter(1, 2)

	sql, args, err := ToSQLWith(q, Options{PlaceholderStyle: Question})
	want := `SELECT "id" FROM "t" WHERE (("a" >= ?) AND (("a" > ?) OR (("a" = ?) AND ("id" < ?)))) ORDER BY "a" ASC, "id" DESC`
	if err != nil || sql != want || !reflect.DeepEqual(args, []any{1, 1, 1, 2}) {
		t.Fatalf("Question: SQL=%q args=%#v err=%v", sql, args, err)
	}

	buf, args, err := AppendSQL([]byte("prefix;"), []any{"p1", "p2"}, q)
	want = `prefix;SELECT "id" FROM "t" WHERE (("a" >= $3) AND (("a" > $4) OR (("a" = $5) AND ("id" < $6)))) ORDER BY "a" ASC, "id" DESC`
	if err != nil || string(buf) != want || !reflect.DeepEqual(args, []any{"p1", "p2", 1, 1, 1, 2}) {
		t.Fatalf("AppendSQL: SQL=%q args=%#v err=%v", buf, args, err)
	}
}

func TestSeekAfterRejectsUnpageableOrdering(t *testing.T) {
	t.Parallel()
	page := func(terms ...Order) *SelectBuilder { return Select(Col("id")).From("t").OrderBy(terms...) }
	for _, tc := range []struct {
		name   string
		query  *SelectBuilder
		clause string
	}{
		{"no_order_by", Select(Col("id")).From("t").SeekAfter(1), "keyset"},
		{"more_values_than_terms", page(Asc("id")).SeekAfter(1, 2), "keyset"},
		{"fewer_values_than_terms", page(Asc("a"), Asc("id")).SeekAfter(1), "keyset"},
		{"order_extended_after_seek", page(Asc("id")).SeekAfter(1).OrderBy(Asc("a")), "keyset"},
		{"nulls_first", page(Asc("id").NullsFirst()).SeekAfter(1), "keyset"},
		{"nulls_last", page(Desc("id").NullsLast()).SeekAfter(1), "keyset"},
		{"using", page(Col("id").Using("<")).SeekAfter(1), "keyset"},
		{"reversed_using", page(Col("id").Using("<").Reverse()).SeekAfter(1), "keyset"},
		{"ordinal", page(Ordinal(1).Asc()).SeekAfter(1), "keyset"},
		{"bound_value_key", page(Param(1).Asc()).SeekAfter(1), "keyset"},
		{"zero_order", page(Order{}).SeekAfter(1), "keyset"},
		{"distinct_on", Select(Col("id")).DistinctOn(Col("a")).From("t").OrderBy(Asc("a"), Asc("id")).SeekAfter(1, 2), "keyset"},
		{"alias_of_other_expression", Select(Col("a").As("x")).From("t").OrderBy(Asc("x")).SeekAfter(1), "keyset"},
		{"alias_of_computed_expression", Select(Col("a").Add(Param(1)).As("x")).From("t").OrderBy(Ident("x").Asc()).SeekAfter(1), "keyset"},
		{"alias_of_qualified_column", Select(Col("t.x").As("x")).From("t").OrderBy(Asc("x")).SeekAfter(1), "keyset"},
		{"nil_first_value", page(Asc("a"), Asc("id")).SeekAfter(nil, 1), "comparison"},
		{"nil_rest_value", page(Asc("a"), Asc("id")).SeekAfter(1, nil), "comparison"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.query, ErrInvalid)
			_, _, err := tc.query.ToSQL()
			detail, _ := errors.AsType[*RenderError](err)
			if detail.Clause != tc.clause {
				t.Fatalf("clause=%q, want %q: %v", detail.Clause, tc.clause, err)
			}
		})
	}
}

func TestSeekAfterFailurePreservesAppendPrefix(t *testing.T) {
	t.Parallel()
	sql := append(make([]byte, 0, 64), "prefix"...)
	args := append(make([]any, 0, 8), "p")
	q := Select(Col("id")).From("t").Where(Eq("tenant", 7)).OrderBy(Asc("a"), Asc("id")).SeekAfter(1, nil)
	gotSQL, gotArgs, err := AppendSQL(sql, args, q)
	if !errors.Is(err, ErrInvalid) || string(gotSQL) != "prefix" || !reflect.DeepEqual(gotArgs, []any{"p"}) {
		t.Fatalf("SQL=%q args=%#v err=%v", gotSQL, gotArgs, err)
	}
	if spare := args[1:cap(args)]; slices.ContainsFunc(spare, func(v any) bool { return v != nil }) {
		t.Fatalf("failed render retained arguments in spare capacity: %#v", spare)
	}
}

func TestSeekAfterLifecycle(t *testing.T) {
	t.Parallel()
	page := func() *SelectBuilder { return Select(Col("id")).From("t").OrderBy(Asc("id")) }

	t.Run("replaces_cursor", func(t *testing.T) {
		t.Parallel()
		checkSQL(t, page().SeekAfter(1).SeekAfter(2), `SELECT "id" FROM "t" WHERE ("id" > $1) ORDER BY "id" ASC`, 2)
	})
	t.Run("remove_order_by_removes_cursor", func(t *testing.T) {
		t.Parallel()
		q := page().SeekAfter(1).RemoveOrderBy()
		checkSQL(t, q, `SELECT "id" FROM "t"`)
		checkSQL(t, q.OrderBy(Asc("id")), `SELECT "id" FROM "t" ORDER BY "id" ASC`)
	})
	t.Run("reset_removes_cursor", func(t *testing.T) {
		t.Parallel()
		q := page().SeekAfter(1)
		q.Reset()
		checkSQL(t, q.Columns(Col("id")).From("t").OrderBy(Asc("id")), `SELECT "id" FROM "t" ORDER BY "id" ASC`)
	})
	t.Run("clone_copies_cursor", func(t *testing.T) {
		t.Parallel()
		q := page().SeekAfter(1)
		c := q.Clone()
		q.SeekAfter(2)
		checkSQL(t, c, `SELECT "id" FROM "t" WHERE ("id" > $1) ORDER BY "id" ASC`, 1)
		c.SeekAfter(3)
		checkSQL(t, q, `SELECT "id" FROM "t" WHERE ("id" > $1) ORDER BY "id" ASC`, 2)
	})
	t.Run("copies_variadic_values", func(t *testing.T) {
		t.Parallel()
		values := []any{1, 2}
		q := Select(Col("id")).From("t").OrderBy(Asc("a"), Asc("id")).SeekAfter(values[0], values[1:]...)
		values[1] = 99
		checkSQL(t, q, `SELECT "id" FROM "t" WHERE (("a", "id") > ($1, $2)) ORDER BY "a" ASC, "id" ASC`, 1, 2)
	})
}

// AllocsPerRun cannot run in a parallel test.
func TestSeekAfterReusesCursorStorage(t *testing.T) {
	q := Select(Col("id")).From("t").OrderBy(Asc("id")).SeekAfter(1)
	q.RemoveOrderBy()
	q.Reset()
	if allocs := testing.AllocsPerRun(100, func() { q.SeekAfter(2) }); allocs != 0 {
		t.Fatalf("SeekAfter after RemoveOrderBy and Reset allocated %.2f times", allocs)
	}
}

func TestOrderReverse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		order Order
		want  string
	}{
		{"asc", Asc("a"), `SELECT 1 ORDER BY "a" DESC`},
		{"desc", Desc("a"), `SELECT 1 ORDER BY "a" ASC`},
		{"asc_nulls_first", Asc("a").NullsFirst(), `SELECT 1 ORDER BY "a" DESC NULLS LAST`},
		{"desc_nulls_last", Desc("a").NullsLast(), `SELECT 1 ORDER BY "a" ASC NULLS FIRST`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, Select(LiteralInt(1)).OrderBy(tc.order.Reverse()), tc.want)
			if back := tc.order.Reverse().Reverse(); !reflect.DeepEqual(back, tc.order) {
				t.Fatalf("Reverse twice = %#v, want %#v", back, tc.order)
			}
		})
	}
	for _, using := range []Order{Col("a").Using("<"), Col("a").Using("<").NullsFirst(), Col("a").Using("<").NullsLast()} {
		original := Select(LiteralInt(1)).OrderBy(using)
		want, _, err := original.ToSQL()
		if err != nil {
			t.Fatal(err)
		}
		checkError(t, Select(LiteralInt(1)).OrderBy(using.Reverse()), ErrInvalid)
		checkSQL(t, Select(LiteralInt(1)).OrderBy(using.Reverse().Reverse()), want)
	}
}

func TestKeysetPagesRenderWithoutAllocating(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    *SelectBuilder
	}{
		{"uniform", SelectCols("id", "name").From("users").Where(Eq("active", true)).OrderBy(Desc("created_at"), Desc("id")).SeekAfter(int64(1700000000), int64(42)).Limit(20)},
		{"mixed", SelectCols("id", "name").From("users").Where(Eq("active", true)).OrderBy(Asc("name"), Desc("id")).SeekAfter("m", int64(42)).Limit(20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, 0, 1024)
			args := make([]any, 0, 16)
			allocs := testing.AllocsPerRun(1000, func() {
				var err error
				buf, args, err = tc.q.AppendSQL(buf[:0], args[:0])
				if err != nil {
					panic(err)
				}
			})
			if allocs != 0 {
				t.Fatalf("warm keyset AppendSQL allocated %.2f times", allocs)
			}
		})
	}
}
