//go:build postgres

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jacoelho/qs"
)

func TestSuccessiveIndirectionAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `BEGIN;
		CREATE TEMP TABLE qs_grouping_setup (dummy integer);
		CREATE DOMAIN pg_temp.qs_inner_array AS integer[];
		CREATE TYPE pg_temp.qs_grouped_pair AS (a integer, b integer);
		CREATE TEMP TABLE qs_grouped_indirection (
			nested pg_temp.qs_inner_array[], bounds box, pairs pg_temp.qs_grouped_pair[]);
		INSERT INTO qs_grouped_indirection VALUES (
			ARRAY[ARRAY[42,99]::pg_temp.qs_inner_array],
			'((2,3),(0,1))'::box,
			ARRAY[ROW(7,8)::pg_temp.qs_grouped_pair]);`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(cleanupCtx, "ROLLBACK")
	})
	one, two, zero := qs.LiteralInt(1), qs.LiteralInt(2), qs.LiteralInt(0)
	nested, bounds, pairs := qs.Col("nested"), qs.Col("bounds"), qs.Col("pairs")
	query := qs.Select(
		nested.Index(one).Parenthesized().Index(two),
		nested.Index(one).Index(two).IsNull().Expr(),
		bounds.Index(zero).Parenthesized().Index(one),
		bounds.Index(zero).Index(one).IsNull().Expr(),
		pairs.Index(one).Parenthesized().Field("b"),
	).From("qs_grouped_indirection")
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var element, field int
	var y float64
	var flatArrayNull, flatBoxNull bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&element, &flatArrayNull, &y, &flatBoxNull, &field); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if element != 99 || !flatArrayNull || y != 3 || !flatBoxNull || field != 8 {
		t.Fatalf("element=%d flatArrayNull=%v y=%v flatBoxNull=%v field=%d", element, flatArrayNull, y, flatBoxNull, field)
	}

	expanded := qs.Select(pairs.Index(one).Parenthesized().Fields().Parenthesized()).From("qs_grouped_indirection")
	sql, args, err = expanded.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var first, second int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&first, &second); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if first != 7 || second != 8 {
		t.Fatalf("expanded pair=(%d,%d); want (7,8)", first, second)
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestFunctionRelationCastsAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	sum := qs.LiteralInt(1).Add(qs.LiteralInt(2)).Cast(qs.Int4)
	for _, tc := range []struct {
		name     string
		relation qs.Relation
	}{
		{"direct", qs.TableFunc(sum).As("c", "v")},
		{"rows_from", qs.RowsFrom(qs.Function(sum)).As("c", "v")},
		{"nested_parameter_cast", qs.TableFunc(qs.Param(3).Cast(qs.Int4).Cast(qs.Int8)).As("c", "v")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := qs.Select(qs.Col("v")).FromExpr(tc.relation).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := conn.Query(ctx, sql, args...)
			if err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			defer rows.Close()
			var value int
			if !rows.Next() {
				t.Fatalf("missing cast row: %v", rows.Err())
			}
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			if value != 3 || rows.Next() || rows.Err() != nil {
				t.Fatalf("value=%d, expected one row containing 3; error=%v", value, rows.Err())
			}
		})
	}
}
