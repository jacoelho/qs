//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
)

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestPostgreSQLZeroColumnSelect(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	cases := []struct {
		name  string
		query qs.Statement
		rows  int
	}{
		{"bare", qs.SelectNoColumns(), 1},
		{"where_false", qs.SelectNoColumns().Where(qs.False()), 0},
		{"two_row_from", qs.SelectNoColumns().FromExpr(qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).As("v", "value")), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := tc.query.ToSQL()
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
	ctx, conn := connect(t)
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
			sql, args, err := query.ToSQL()
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
	ctx, conn := connect(t)
	outer := qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).As("o", "n")
	lateral := qs.Lateral(qs.Derived(
		qs.Select(qs.LiteralInt(1)).Where(qs.Col("o.n").EqExpr(qs.LiteralInt(1))),
	))
	query := qs.Select(qs.Col("o.n").Cast(qs.TypeText)).
		FromExpr(outer).
		CrossJoinExpr(lateral).
		OrderBy(qs.Asc("o.n"))
	got := queryStrings(t, ctx, conn, query)
	want := []string{"1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
