//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jacoelho/qx"
)

func TestJoinFamiliesAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	a := qx.ValuesExpr(qx.LiteralInt(1)).RowExpr(qx.LiteralInt(2)).As("a", "id")
	b := qx.ValuesExpr(qx.LiteralInt(1)).RowExpr(qx.LiteralInt(2)).RowExpr(qx.LiteralInt(3)).As("b", "id")
	for _, tc := range []struct {
		name string
		join qx.Relation
		want [][2]int
	}{
		{"cross", qx.CrossJoin(a, b), [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}, {2, 3}}},
		{"on", qx.InnerJoin(a, b).On(qx.Col("a.id").EqExpr(qx.Col("b.id"))), [][2]int{{1, 1}, {2, 2}}},
		{"using", qx.InnerJoin(a, b).Using("id"), [][2]int{{1, 1}, {2, 2}}},
		{"natural", qx.NaturalJoin(a, b), [][2]int{{1, 1}, {2, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := qx.SelectCols("a.id", "b.id").FromExpr(tc.join).OrderBy(qx.Asc("a.id"), qx.Asc("b.id")).ToSQL()
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
	sql, args, err := qx.Select(
		qx.Param(input).Cast(qx.NamedType("pg_catalog", "varchar")),
		qx.Param(input).Cast(qx.Varchar(3)),
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
	table := qx.JSONTable(qx.JSONBParam(`[{"a":null},{}]`).Expr(), "$[*]",
		qx.JSONOrdinality("n"), qx.JSONExistsColumn("has_a", qx.Bool).Path("$.a"),
	).As("j")
	sql, args, err := qx.SelectCols("has_a").FromExpr(table).OrderBy(qx.Asc("n")).ToSQL()
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

func TestRowMembershipAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	source := qx.ValuesExpr(qx.LiteralInt(1), qx.LiteralInt(2)).RowExpr(qx.LiteralInt(3), qx.LiteralInt(4))
	empty := qx.Select(qx.LiteralInt(1), qx.LiteralInt(2)).Where(qx.False())
	match := qx.Tuple(qx.LiteralInt(1), qx.LiteralInt(2))
	swapped := qx.Tuple(qx.LiteralInt(2), qx.LiteralInt(1))
	missing := qx.Tuple(qx.LiteralInt(5), qx.LiteralInt(6))
	withNull := qx.Tuple(qx.LiteralInt(1), qx.NullLiteral())
	for _, tc := range []struct {
		name      string
		condition qx.Condition
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
			sql, args, err := qx.Select(tc.condition.Expr()).ToSQL()
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
