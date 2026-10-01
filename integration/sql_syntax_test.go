//go:build postgres

package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jacoelho/qx"
)

func syntaxText(t *testing.T, ctx context.Context, conn *pgx.Conn, expr qx.Expr) string {
	t.Helper()
	query, args, err := qx.Select(expr.Cast(qx.Text)).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := conn.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

func TestSQLSyntaxLiveSemantics(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `SET TIME ZONE 'UTC'`); err != nil {
		t.Fatal(err)
	}

	// Keep each operand visibly distinct so argument order and SQL grammar are
	// both exercised by PostgreSQL rather than only by the renderer golden.
	cases := []struct {
		name string
		expr qx.Expr
		want string
	}{
		{"substring", qx.SubstringFrom(qx.LiteralString("abcdef"), qx.LiteralInt(2), qx.LiteralInt(2)), "bc"},
		{"position", qx.Position(qx.LiteralString("bc"), qx.LiteralString("abc")), "2"},
		{"normalize_nfc", qx.Normalize(qx.LiteralString("e\u0301"), qx.NFC), "é"},
		{"overlay", qx.Overlay(qx.LiteralString("abcdef"), qx.LiteralString("XY"), qx.LiteralInt(2), qx.LiteralInt(2)), "aXYdef"},
		{"trim_leading", qx.TrimSyntax(qx.LiteralString("xxvalue"), qx.TrimLeadingDirection, qx.LiteralString("x")), "value"},
		{"trim_trailing", qx.TrimSyntax(qx.LiteralString("valuexx"), qx.TrimTrailingDirection, qx.LiteralString("x")), "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := syntaxText(t, ctx, conn, tc.expr); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}

	if got := syntaxText(t, ctx, conn, qx.Overlaps(
		qx.LiteralString("2026-01-01").Cast(qx.Date),
		qx.LiteralString("2026-01-03").Cast(qx.Date),
		qx.LiteralString("2026-01-02").Cast(qx.Date),
		qx.LiteralString("2026-01-04").Cast(qx.Date),
	).Expr()); got != "true" {
		t.Fatalf("overlaps got %q; want true", got)
	}
	if got := syntaxText(t, ctx, conn, qx.IsNormalized(qx.LiteralString("e\u0301"), qx.NFC).Expr()); got != "false" {
		t.Fatalf("is normalized got %q; want false", got)
	}
	if got := syntaxText(t, ctx, conn, qx.IsNotNormalized(qx.LiteralString("e\u0301"), qx.NFC).Expr()); got != "true" {
		t.Fatalf("is not normalized got %q; want true", got)
	}

	local := qx.AtLocal(qx.LiteralString("2026-01-02 03:04:05+02").Cast(qx.TimestampTZ))
	if got := syntaxText(t, ctx, conn, local); got != "2026-01-02 01:04:05" {
		t.Fatalf("AT LOCAL got %q; want 2026-01-02 01:04:05", got)
	}
	zone := qx.LiteralString("2026-01-02 03:04:05").Cast(qx.Timestamp).AtTimeZone(qx.LiteralString("UTC"))
	if got := syntaxText(t, ctx, conn, zone); !strings.HasPrefix(got, "2026-01-02 03:04:05") {
		t.Fatalf("AT TIME ZONE got %q", got)
	}

	if got := syntaxText(t, ctx, conn, qx.CollationFor(qx.LiteralString("value").Collate("C"))); got == "" {
		t.Fatal("COLLATION FOR returned an empty collation")
	}
}

func TestSQLValueFunctionPrecision(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `SET TIME ZONE 'UTC'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		expr qx.Expr
	}{
		{"current_time", qx.CurrentTime(3)},
		{"current_timestamp", qx.CurrentTimestamp(3)},
		{"localtime", qx.LocalTime(3)},
		{"localtimestamp", qx.LocalTimestamp(3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := qx.Select(qx.Extract(qx.PartMicroseconds, tc.expr).Cast(qx.Int8)).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			var microseconds int64
			if err := conn.QueryRow(ctx, sql, args...).Scan(&microseconds); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if microseconds < 0 || microseconds >= 60000000 || microseconds%1000 != 0 {
				t.Fatalf("%s microseconds=%d; want millisecond precision", tc.name, microseconds)
			}
		})
	}

	for _, tc := range []struct {
		name string
		expr qx.Expr
	}{
		{"session_user", qx.SessionUser()},
		{"current_schema", qx.CurrentSchema()},
		{"current_catalog", qx.CurrentCatalog()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := syntaxText(t, ctx, conn, tc.expr); got == "" {
				t.Fatalf("%s is empty", tc.name)
			}
		})
	}
}
