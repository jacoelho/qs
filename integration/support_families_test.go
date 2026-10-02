//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jacoelho/qs"
)

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestJoinFamiliesAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	a := qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).As("a", "id")
	b := qs.ValuesExpr(qs.LiteralInt(1)).RowExpr(qs.LiteralInt(2)).RowExpr(qs.LiteralInt(3)).As("b", "id")
	for _, tc := range []struct {
		name string
		join qs.Relation
		want [][2]int
	}{
		{"cross", qs.CrossJoin(a, b), [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}, {2, 3}}},
		{"on", qs.InnerJoin(a, b).On(qs.Col("a.id").EqExpr(qs.Col("b.id"))), [][2]int{{1, 1}, {2, 2}}},
		{"using", qs.InnerJoin(a, b).Using("id"), [][2]int{{1, 1}, {2, 2}}},
		{"natural", qs.NaturalJoin(a, b), [][2]int{{1, 1}, {2, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := qs.SelectCols("a.id", "b.id").FromExpr(tc.join).OrderBy(qs.Asc("a.id"), qs.Asc("b.id")).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := conn.Query(ctx, sql, args...)
			if err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			defer rows.Close()
			var got [][2]int
			for rows.Next() {
				var pair [2]int
				if err := rows.Scan(&pair[0], &pair[1]); err != nil {
					t.Fatal(err)
				}
				got = append(got, pair)
				if len(got) > len(tc.want) {
					t.Fatalf("unexpected extra join row: %v", got)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rows=%v; want %v", got, tc.want)
			}
		})
	}
}

func TestUnboundedVarcharAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	const input = "abcdef'; ? $1"
	sql, args, err := qs.Select(
		qs.Param(input).Cast(qs.TypeNamed("pg_catalog", "varchar")),
		qs.Param(input).Cast(qs.TypeVarchar(3)),
	).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var unbounded, bounded string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&unbounded, &bounded); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if unbounded != input || bounded != "abc" {
		t.Fatalf("unbounded=%q bounded=%q; want %q and abc", unbounded, bounded, input)
	}
}

func TestJSONTableExistsAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	table := qs.JSONTable(qs.JSONBParam(`[{"a":null},{}]`).Expr(), "$[*]",
		qs.JSONOrdinality("n"), qs.JSONExistsColumn("has_a", qs.TypeBool).Path("$.a"),
	).As("j")
	sql, args, err := qs.SelectCols("has_a").FromExpr(table).OrderBy(qs.Asc("n")).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var got []bool
	for rows.Next() {
		var exists bool
		if err := rows.Scan(&exists); err != nil {
			t.Fatal(err)
		}
		got = append(got, exists)
		if len(got) > 2 {
			t.Fatal("unexpected extra JSON_TABLE row")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []bool{true, false}) {
		t.Fatalf("key existence=%v; want [true false]", got)
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestRowMembershipAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	source := qs.ValuesExpr(qs.LiteralInt(1), qs.LiteralInt(2)).RowExpr(qs.LiteralInt(3), qs.LiteralInt(4))
	empty := qs.Select(qs.LiteralInt(1), qs.LiteralInt(2)).Where(qs.False())
	match := qs.Tuple(qs.LiteralInt(1), qs.LiteralInt(2))
	swapped := qs.Tuple(qs.LiteralInt(2), qs.LiteralInt(1))
	missing := qs.Tuple(qs.LiteralInt(5), qs.LiteralInt(6))
	withNull := qs.Tuple(qs.LiteralInt(1), qs.NullLiteral())
	for _, tc := range []struct {
		name      string
		condition qs.Condition
		want      pgtype.Bool
	}{
		{"match", match.InQuery(source), pgtype.Bool{Bool: true, Valid: true}},
		{"match_not_in", match.NotInQuery(source), pgtype.Bool{Bool: false, Valid: true}},
		{"column_order", swapped.InQuery(source), pgtype.Bool{Bool: false, Valid: true}},
		{"missing_not_in", missing.NotInQuery(source), pgtype.Bool{Bool: true, Valid: true}},
		{"null_in", withNull.InQuery(source), pgtype.Bool{}},
		{"null_not_in", withNull.NotInQuery(source), pgtype.Bool{}},
		{"null_in_empty", withNull.InQuery(empty), pgtype.Bool{Bool: false, Valid: true}},
		{"null_not_in_empty", withNull.NotInQuery(empty), pgtype.Bool{Bool: true, Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := qs.Select(tc.condition.Expr()).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			var got pgtype.Bool
			if err := conn.QueryRow(ctx, sql, args...).Scan(&got); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if got != tc.want {
				t.Fatalf("membership=%+v; want %+v", got, tc.want)
			}
		})
	}
}
