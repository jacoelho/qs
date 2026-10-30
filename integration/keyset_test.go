//go:build postgres

package integration_test

import (
	"cmp"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/jacoelho/qs"
)

// createKeysetRows creates 13 rows whose (a, b) pairs repeat, so only the
// unique id breaks ties.
func createKeysetRows(t *testing.T, session *testSession) {
	t.Helper()
	if _, err := session.Exec(session, `CREATE TEMP TABLE r (a bigint NOT NULL, b bigint NOT NULL, id bigint PRIMARY KEY);
		INSERT INTO r SELECT i % 3, (i / 3) % 2, i FROM generate_series(1, 13) AS i`); err != nil {
		t.Fatal(err)
	}
}

func keysetRows(t *testing.T, session *testSession, query *qs.SelectBuilder) [][3]int64 {
	t.Helper()
	sql, args, err := session.render(t, query)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var got [][3]int64
	for rows.Next() {
		var row [3]int64
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return got
}

func TestKeysetPaginationMatchesFullOrdering(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	keys := []string{"a", "b", "id"}
	for mask := range 8 {
		order := make([]qs.Order, len(keys))
		for i, key := range keys {
			order[i] = qs.Asc("r." + key)
			if mask&(1<<i) != 0 {
				order[i] = qs.Desc("r." + key)
			}
		}
		page := func() *qs.SelectBuilder { return qs.SelectCols(keys...).From("r").OrderBy(order...) }
		full := keysetRows(t, session, page())
		if len(full) != 13 {
			t.Fatalf("mask %03b: full ordering has %d rows", mask, len(full))
		}
		for _, size := range []int{1, 4, 13} {
			var got [][3]int64
			query := page().Limit(size)
			// 13 rows at size 1 take 13 pages and one empty page.
			for pages := 0; ; pages++ {
				if pages == 15 {
					t.Fatalf("mask %03b size %d: pagination did not terminate", mask, size)
				}
				rows := keysetRows(t, session, query)
				if len(rows) == 0 {
					break
				}
				got = append(got, rows...)
				last := rows[len(rows)-1]
				query = page().SeekAfter(last[0], last[1], last[2]).Limit(size)
			}
			if !reflect.DeepEqual(got, full) {
				t.Fatalf("mask %03b size %d: pages %v; want %v", mask, size, got, full)
			}
		}

		reversed := make([]qs.Order, len(order))
		for i, term := range order {
			reversed[i] = term.Reverse()
		}
		anchor := full[6]
		previous := keysetRows(t, session, qs.SelectCols(keys...).From("r").OrderBy(reversed...).
			SeekAfter(anchor[0], anchor[1], anchor[2]).Limit(3))
		slices.Reverse(previous)
		if !reflect.DeepEqual(previous, full[3:6]) {
			t.Fatalf("mask %03b: previous page %v; want %v", mask, previous, full[3:6])
		}
	}
}

// A cast output takes the name of its column, so ORDER BY resolves a bare id to
// the text output while the cursor in WHERE compares the integer column.
// Qualified keys resolve to the input column in both clauses.
func TestKeysetPaginationBesideSameNamedOutput(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	query := func(order ...qs.Order) *qs.SelectBuilder {
		return qs.Select(qs.Col("r.a"), qs.Col("r.b"), qs.Col("r.id").Cast(qs.TypeText)).From("r").OrderBy(order...)
	}
	rows := func(query *qs.SelectBuilder) [][3]int64 {
		t.Helper()
		sql, args, err := session.render(t, query)
		if err != nil {
			t.Fatal(err)
		}
		result, err := session.Query(session, sql, args...)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		defer result.Close()
		var got [][3]int64
		for result.Next() {
			var row [3]int64
			var id string
			if err := result.Scan(&row[0], &row[1], &id); err != nil {
				t.Fatal(err)
			}
			if row[2], err = strconv.ParseInt(id, 10, 64); err != nil {
				t.Fatal(err)
			}
			got = append(got, row)
		}
		if err := result.Err(); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return got
	}

	byID := keysetFixture()
	bareOrder := rows(query(qs.Asc("id")))
	if reflect.DeepEqual(bareOrder, byID) {
		t.Fatalf("bare id key sorted numerically, so this test cannot detect a mismatch: %v", bareOrder)
	}

	want := keysetFixture()
	slices.SortFunc(want, func(x, y [3]int64) int { return cmp.Or(cmp.Compare(x[0], y[0]), cmp.Compare(y[2], x[2])) })
	order := []qs.Order{qs.Asc("r.a"), qs.Desc("r.id")}
	var got [][3]int64
	page := query(order...).Limit(4)
	for pages := 0; ; pages++ {
		if pages == 6 {
			t.Fatalf("pagination did not terminate; pages so far %v", got)
		}
		result := rows(page)
		if len(result) == 0 {
			break
		}
		got = append(got, result...)
		last := result[len(result)-1]
		page = query(order...).SeekAfter(last[0], last[2]).Limit(4)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages %v; want %v", got, want)
	}
}

func TestCountRowsMatchesIndependentCounts(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	count := func(query qs.Statement) int64 { t.Helper(); return scalarCount(t, session, query) }

	all := keysetRows(t, session, qs.SelectCols("a", "b", "id").From("r"))
	var matching int64
	groups := map[[2]int64]int{}
	for _, row := range all {
		if row[2] > 2 {
			matching++
		}
		groups[[2]int64{row[0], row[1]}]++
	}
	repeated := int64(0)
	for _, n := range groups {
		if n > 1 {
			repeated++
		}
	}

	page := qs.SelectCols("a", "b", "id").From("r").Where(qs.Gt("id", int64(2))).
		OrderBy(qs.Asc("r.a"), qs.Desc("r.id")).Limit(4)
	total := qs.CountRows(page)
	var seen int64
	for pages := 0; ; pages++ {
		if pages == 5 {
			t.Fatal("pagination did not terminate")
		}
		if got := count(total); got != matching {
			t.Fatalf("page %d: count %d; want %d", pages+1, got, matching)
		}
		rows := keysetRows(t, session, page)
		if len(rows) == 0 {
			break
		}
		seen += int64(len(rows))
		last := rows[len(rows)-1]
		page.SeekAfter(last[0], last[2])
	}
	if seen != matching {
		t.Fatalf("pages returned %d rows; count is %d", seen, matching)
	}

	distinct := qs.Select(qs.Col("a")).Distinct().From("r").OrderBy(qs.Asc("a")).Limit(1)
	if got := count(qs.CountRows(distinct)); got != 3 {
		t.Fatalf("distinct count %d; want 3", got)
	}
	grouped := qs.Select(qs.Col("a"), qs.Col("b")).From("r").GroupBy("a", "b").
		Having(qs.CountAll().Gt(int64(1))).OrderBy(qs.Asc("a"), qs.Asc("b")).Limit(1)
	if got := count(qs.CountRows(grouped)); got != repeated {
		t.Fatalf("grouped count %d; want %d", got, repeated)
	}
	if locked := qs.SelectCols("id").From("r").Lock(qs.ForUpdate()); count(qs.CountRows(locked)) != int64(len(all)) {
		t.Fatal("locked query count differs from row count")
	}
}

// scalarCount runs a statement that returns one count.
func scalarCount(t *testing.T, session *testSession, statement qs.Statement) int64 {
	t.Helper()
	sql, args, err := session.render(t, statement)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := session.QueryRow(session, sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// fetchedRows runs query and returns how many rows PostgreSQL produced.
func fetchedRows(t *testing.T, session *testSession, query *qs.SelectBuilder) int64 {
	t.Helper()
	sql, args, err := session.render(t, query)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// ORDER BY can change which rows a query returns: PostgreSQL evaluates a
// set-returning function there once per row and an aggregate there groups the
// query. The count of a paged query must match the rows of the unpaged query.
//
//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestCountRowsMatchesRowsOfOrderedQuery(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	for _, tc := range []struct {
		name  string
		query func() *qs.SelectBuilder
		seek  []any
		want  int64
	}{
		{"set_returning_function", func() *qs.SelectBuilder {
			return qs.SelectCols("id").From("r").OrderBy(qs.Call("unnest", qs.UnsafeSQL("ARRAY[1, 2]")).Asc(), qs.Asc("id"))
		}, nil, 26},
		{"aggregate", func() *qs.SelectBuilder {
			return qs.Select(qs.LiteralInt(1)).From("r").OrderBy(qs.CountAll().Asc())
		}, nil, 1},
		{"window_over_aggregate", func() *qs.SelectBuilder {
			return qs.Select(qs.LiteralInt(1)).From("r").OrderBy(qs.Sum(qs.CountAll()).Over(qs.Window()).Asc())
		}, nil, 1},
		{"outer_level_aggregate", func() *qs.SelectBuilder {
			return qs.Select(qs.LiteralInt(1)).From("r").OrderBy(qs.Scalar(qs.Select(qs.Max(qs.Col("r.id")))).Asc())
		}, nil, 1},
		{"grouped_aggregate", func() *qs.SelectBuilder {
			return qs.SelectCols("a").From("r").GroupBy("a").OrderBy(qs.CountAll().Desc(), qs.Asc("a"))
		}, nil, 3},
		// DISTINCT ON keeps one row per group of the rows the empty
		// set-returning function leaves, which are none.
		{"distinct_on_set_returning_function", func() *qs.SelectBuilder {
			return qs.SelectCols("id").DistinctOn(qs.Col("a")).From("r").
				OrderBy(qs.Asc("a"), qs.Call("unnest", qs.UnsafeSQL("'{}'::bigint[]")).Asc())
		}, nil, 0},
		// A cast output takes the name of its column. Unnamed, the moved term
		// would be the "a" DISTINCT ON resolves to: ambiguous beside the
		// projected column, and two boolean groups instead of three without it.
		{"distinct_on_beside_moved_column_name", func() *qs.SelectBuilder {
			return qs.SelectCols("a", "id").DistinctOn(qs.Col("a")).From("r").
				OrderBy(qs.Asc("a"), qs.Col("a").Cast(qs.TypeText).Asc())
		}, nil, 3},
		{"distinct_on_moved_column_name", func() *qs.SelectBuilder {
			return qs.SelectCols("id").DistinctOn(qs.Col("a")).From("r").
				OrderBy(qs.Asc("a"), qs.Col("a").Cast(qs.TypeInt4).Cast(qs.TypeBool).Asc())
		}, nil, 3},
		{"bound_expression_key", func() *qs.SelectBuilder {
			return qs.SelectCols("id").From("r").OrderBy(qs.Call("abs", qs.Col("id").Sub(qs.Param(int64(4)))).Asc(), qs.Asc("r.id"))
		}, []any{int64(0), int64(4)}, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fetchedRows(t, session, tc.query()); got != tc.want {
				t.Fatalf("query returned %d rows; want %d", got, tc.want)
			}
			paged := tc.query().Limit(2).Offset(1)
			if tc.seek != nil {
				paged.SeekAfter(tc.seek[0], tc.seek[1:]...)
			}
			if got := scalarCount(t, session, qs.CountRows(paged)); got != tc.want {
				t.Fatalf("count %d; want %d", got, tc.want)
			}
		})
	}
}

// keysetFixture computes the rows createKeysetRows inserts, independently of
// PostgreSQL.
func keysetFixture() [][3]int64 {
	rows := make([][3]int64, 0, 13)
	for i := int64(1); i <= 13; i++ {
		rows = append(rows, [3]int64{i % 3, (i / 3) % 2, i})
	}
	return rows
}

// trustedDisjunction binds looser than AND, so pages and combined filters are
// wrong unless qs keeps it a unit.
func trustedDisjunction() qs.Condition { return qs.AsCondition(qs.UnsafeSQL("a = 0 OR b = 1")) }

func matchesTrustedDisjunction(row [3]int64) bool { return row[0] == 0 || row[1] == 1 }

func TestKeysetPaginationWithTrustedDisjunction(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	cases := []struct {
		name    string
		order   []qs.Order
		keys    func(row [3]int64) []any
		compare func(x, y [3]int64) int
	}{
		{"id_asc", []qs.Order{qs.Asc("r.id")},
			func(row [3]int64) []any { return []any{row[2]} },
			func(x, y [3]int64) int { return cmp.Compare(x[2], y[2]) }},
		{"id_desc", []qs.Order{qs.Desc("r.id")},
			func(row [3]int64) []any { return []any{row[2]} },
			func(x, y [3]int64) int { return cmp.Compare(y[2], x[2]) }},
		{"a_asc_id_desc", []qs.Order{qs.Asc("r.a"), qs.Desc("r.id")},
			func(row [3]int64) []any { return []any{row[0], row[2]} },
			func(x, y [3]int64) int { return cmp.Or(cmp.Compare(x[0], y[0]), cmp.Compare(y[2], x[2])) }},
	}
	for _, tc := range cases {
		var want [][3]int64
		for _, row := range keysetFixture() {
			if matchesTrustedDisjunction(row) {
				want = append(want, row)
			}
		}
		slices.SortFunc(want, tc.compare)
		page := func() *qs.SelectBuilder {
			return qs.SelectCols("a", "b", "id").From("r").Where(trustedDisjunction()).OrderBy(tc.order...)
		}
		for _, size := range []int{1, 4} {
			var got [][3]int64
			query := page().Limit(size)
			for pages := 0; ; pages++ {
				if pages == len(want)+2 {
					t.Fatalf("%s size %d: pagination did not terminate; pages so far %v", tc.name, size, got)
				}
				rows := keysetRows(t, session, query)
				if len(rows) == 0 {
					break
				}
				got = append(got, rows...)
				keys := tc.keys(rows[len(rows)-1])
				query = page().SeekAfter(keys[0], keys[1:]...).Limit(size)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s size %d: pages %v; want %v", tc.name, size, got, want)
			}
		}
	}
}

func TestTrustedConditionsComposeAsUnits(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	other := qs.Gt("id", int64(6))
	later := func(row [3]int64) bool { return row[2] > 6 }
	cases := []struct {
		name  string
		where []qs.Condition
		keep  func(row [3]int64) bool
	}{
		{"where_raw_first", []qs.Condition{trustedDisjunction(), other},
			func(row [3]int64) bool { return matchesTrustedDisjunction(row) && later(row) }},
		{"where_raw_last", []qs.Condition{other, trustedDisjunction()},
			func(row [3]int64) bool { return later(row) && matchesTrustedDisjunction(row) }},
		{"and_raw_first", []qs.Condition{qs.And(trustedDisjunction(), other)},
			func(row [3]int64) bool { return matchesTrustedDisjunction(row) && later(row) }},
		{"and_raw_last", []qs.Condition{qs.And(other, trustedDisjunction())},
			func(row [3]int64) bool { return later(row) && matchesTrustedDisjunction(row) }},
		{"or", []qs.Condition{qs.Or(trustedDisjunction(), other)},
			func(row [3]int64) bool { return matchesTrustedDisjunction(row) || later(row) }},
		{"not", []qs.Condition{qs.Not(trustedDisjunction())},
			func(row [3]int64) bool { return !matchesTrustedDisjunction(row) }},
	}
	for _, tc := range cases {
		var want [][3]int64
		for _, row := range keysetFixture() {
			if tc.keep(row) {
				want = append(want, row)
			}
		}
		got := keysetRows(t, session, qs.SelectCols("a", "b", "id").From("r").Where(tc.where...).OrderBy(qs.Asc("id")))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: rows %v; want %v", tc.name, got, want)
		}
	}
}

func compareBool(x, y bool) int {
	switch {
	case x == y:
		return 0
	case !x:
		return -1
	default:
		return 1
	}
}

// Trusted keys parenthesize their own comparisons so that a wrong parse,
// such as NOT ((a = 0) >= $1), selects wrong rows instead of failing.
func TestKeysetPaginationWithTrustedKey(t *testing.T) {
	t.Parallel()
	session := connect(t)
	createKeysetRows(t, session)
	notZero := qs.UnsafeSQL("NOT (a = 0)")
	either := qs.UnsafeSQL("(a = 0) OR (b = 1)")
	notOne := qs.UnsafeSQL("NOT (b = 1)")
	cases := []struct {
		name    string
		order   []qs.Order
		keys    func(row [3]int64) []any
		compare func(x, y [3]int64) int
	}{
		{"not_asc_id_desc", []qs.Order{notZero.Asc(), qs.Desc("r.id")},
			func(row [3]int64) []any { return []any{row[0] != 0, row[2]} },
			func(x, y [3]int64) int { return cmp.Or(compareBool(x[0] != 0, y[0] != 0), cmp.Compare(y[2], x[2])) }},
		{"or_desc_id_asc", []qs.Order{either.Desc(), qs.Asc("r.id")},
			func(row [3]int64) []any { return []any{matchesTrustedDisjunction(row), row[2]} },
			func(x, y [3]int64) int {
				return cmp.Or(compareBool(matchesTrustedDisjunction(y), matchesTrustedDisjunction(x)), cmp.Compare(x[2], y[2]))
			}},
		{"not_uniform", []qs.Order{notZero.Asc(), qs.Asc("r.id")},
			func(row [3]int64) []any { return []any{row[0] != 0, row[2]} },
			func(x, y [3]int64) int { return cmp.Or(compareBool(x[0] != 0, y[0] != 0), cmp.Compare(x[2], y[2])) }},
		{"three_keys", []qs.Order{qs.Asc("r.a"), notOne.Desc(), qs.Asc("r.id")},
			func(row [3]int64) []any { return []any{row[0], row[1] != 1, row[2]} },
			func(x, y [3]int64) int {
				return cmp.Or(cmp.Compare(x[0], y[0]), compareBool(y[1] != 1, x[1] != 1), cmp.Compare(x[2], y[2]))
			}},
	}
	for _, tc := range cases {
		want := keysetFixture()
		slices.SortFunc(want, tc.compare)
		page := func() *qs.SelectBuilder { return qs.SelectCols("a", "b", "id").From("r").OrderBy(tc.order...) }
		for _, size := range []int{1, 4} {
			var got [][3]int64
			query := page().Limit(size)
			for pages := 0; ; pages++ {
				if pages == len(want)+2 {
					t.Fatalf("%s size %d: pagination did not terminate; pages so far %v", tc.name, size, got)
				}
				rows := keysetRows(t, session, query)
				if len(rows) == 0 {
					break
				}
				got = append(got, rows...)
				keys := tc.keys(rows[len(rows)-1])
				query = page().SeekAfter(keys[0], keys[1:]...).Limit(size)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s size %d: pages %v; want %v", tc.name, size, got, want)
			}
		}
	}

	// A single boolean key has ties, so compare its cursor boundary as a set.
	boundaries := []struct {
		name   string
		order  qs.Order
		cursor bool
		keep   func(key bool) bool
	}{
		{"asc_after_true", notZero.Asc(), true, func(key bool) bool { return compareBool(key, true) > 0 }},
		{"asc_after_false", notZero.Asc(), false, func(key bool) bool { return compareBool(key, false) > 0 }},
		{"desc_after_true", notZero.Desc(), true, func(key bool) bool { return compareBool(key, true) < 0 }},
	}
	byID := func(x, y [3]int64) int { return cmp.Compare(x[2], y[2]) }
	for _, tc := range boundaries {
		var want [][3]int64
		for _, row := range keysetFixture() {
			if tc.keep(row[0] != 0) {
				want = append(want, row)
			}
		}
		got := keysetRows(t, session, qs.SelectCols("a", "b", "id").From("r").OrderBy(tc.order).SeekAfter(tc.cursor))
		slices.SortFunc(got, byID)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: rows %v; want %v", tc.name, got, want)
		}
	}
}
