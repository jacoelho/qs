//go:build postgres

package integration_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/jacoelho/qs"
)

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestPostgreSQLZeroColumnSelect(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	cases := []struct {
		name  string
		query qs.Statement
		rows  int
	}{
		{"bare", qs.SelectNoColumns(), 1},
		{"where_false", qs.SelectNoColumns().Where(qs.False()), 0},
		{
			"two_row_from",
			qs.SelectNoColumns().From(
				qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).As("v", "value"),
			),
			2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := session.render(t, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			result, err := conn.Query(ctx, sql, args...)
			if err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			defer result.Close()
			if fields := result.FieldDescriptions(); len(fields) != 0 {
				t.Fatalf("got %d field descriptions; want zero", len(fields))
			}
			count := 0
			for result.Next() {
				count++
			}
			if err := result.Err(); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if count != tc.rows {
				t.Fatalf("got %d rows; want %d", count, tc.rows)
			}
		})
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestPostgreSQLNumericRadixValues(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	requireVersion(t, session, 16)
	cases := []struct {
		name, token, want string
	}{
		{"hex", "0xff", "255"},
		{"octal", "0o755", "493"},
		{"binary", "0b101010", "42"},
		{"huge_hex", "0x10000000000000000", "18446744073709551616"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := qs.Select(qs.LiteralNumeric(tc.token).Cast(qs.TypeText))
			sql, args, err := session.render(t, query)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := conn.QueryRow(ctx, sql, args...).Scan(&got); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if got != tc.want {
				t.Fatalf("%s: got %q; want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestPostgreSQLUnaliasedLateralCorrelation(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, 16)
	outer := qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).As("o", "n")
	lateral := qs.Lateral(qs.Derived(
		qs.Select(qs.LiteralInt(1)).Where(qs.Col("o", "n").Eq(qs.LiteralInt(1))),
	))
	query := qs.Select(qs.Col("o", "n").Cast(qs.TypeText)).
		From(outer).
		CrossJoin(lateral).
		OrderBy(qs.Col("o", "n").Asc())
	got := queryStrings(t, session, query)
	want := []string{"1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestLimitWithTiesKeepsRowsTiedWithTheLast(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE scores (name text NOT NULL, points integer NOT NULL);
		INSERT INTO scores VALUES ('a', 3), ('b', 2), ('c', 2), ('d', 1)`); err != nil {
		t.Fatal(err)
	}
	ranked := qs.Select("name").From("scores").OrderBy(qs.Col("points").Desc())
	for _, tc := range []struct {
		name  string
		query qs.SelectBuilder
		want  []string
	}{
		{"limit_stops_inside_the_tie", ranked.Limit(2), nil},
		{"ties_extend_the_limit", ranked.LimitWithTies(2), []string{"a", "b", "c"}},
		{"ties_after_offset", ranked.Offset(1).LimitWithTies(1), []string{"b", "c"}},
		{"ties_with_row_lock", ranked.LimitWithTies(2).Lock(qs.ForUpdate()), []string{"a", "b", "c"}},
	} {
		got := queryStrings(t, session, tc.query)
		slices.Sort(got)
		if tc.want == nil {
			// Which of the tied rows LIMIT keeps is unspecified.
			if len(got) != 2 || got[0] != "a" {
				t.Fatalf("%s: got %v; want a and one of b, c", tc.name, got)
			}
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: got %v; want %v", tc.name, got, tc.want)
		}
	}
}
