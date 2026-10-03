//go:build postgres

package integration_test

import (
	"strings"
	"testing"

	"github.com/jacoelho/qs"
)

func syntaxText(t *testing.T, session *testSession, expr qs.Expr) string {
	t.Helper()
	query, args, err := session.render(t, qs.Select(expr.Cast(qs.TypeText)))
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := session.QueryRow(session, query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestSQLSyntaxLiveSemantics(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `SET TIME ZONE 'UTC'`); err != nil {
		t.Fatal(err)
	}

	// Keep each operand visibly distinct so argument order and SQL grammar are
	// both exercised by PostgreSQL rather than only by the renderer golden.
	cases := []struct {
		name string
		expr qs.Expr
		want string
	}{
		{"substring", qs.SubstringFrom(qs.LiteralString("abcdef"), qs.LiteralInt(2), qs.LiteralInt(2)), "bc"},
		{"position", qs.Position(qs.LiteralString("bc"), qs.LiteralString("abc")), "2"},
		{"overlay", qs.Overlay(qs.LiteralString("abcdef"), qs.LiteralString("XY"), qs.LiteralInt(2), qs.LiteralInt(2)), "aXYdef"},
		{"trim_leading", qs.TrimSyntax(qs.LiteralString("xxvalue"), qs.TrimLeadingDirection, qs.LiteralString("x")), "value"},
		{"trim_trailing", qs.TrimSyntax(qs.LiteralString("valuexx"), qs.TrimTrailingDirection, qs.LiteralString("x")), "value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := syntaxText(t, session, tc.expr); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
	if session.options.PostgreSQL >= qs.PostgreSQL13 {
		t.Run("normalize_nfc", func(t *testing.T) {
			if got := syntaxText(t, session, qs.Normalize(qs.LiteralString("e\u0301"), qs.NFC)); got != "é" {
				t.Fatalf("got %q; want é", got)
			}
		})
	}
	if session.options.PostgreSQL >= qs.PostgreSQL14 {
		t.Run("substring_similar", func(t *testing.T) {
			expr := qs.SubstringSimilar(qs.LiteralString("abcdef"), qs.LiteralString(`%#"b#"%`), qs.LiteralString("#"))
			if got := syntaxText(t, session, expr); got != "b" {
				t.Fatalf("got %q; want b", got)
			}
		})
	}

	if got := syntaxText(t, session, qs.Overlaps(
		qs.LiteralString("2026-01-01").Cast(qs.TypeDate),
		qs.LiteralString("2026-01-03").Cast(qs.TypeDate),
		qs.LiteralString("2026-01-02").Cast(qs.TypeDate),
		qs.LiteralString("2026-01-04").Cast(qs.TypeDate),
	).Expr()); got != "true" {
		t.Fatalf("overlaps got %q; want true", got)
	}
	if session.options.PostgreSQL >= qs.PostgreSQL13 {
		if got := syntaxText(t, session, qs.IsNormalized(qs.LiteralString("e\u0301"), qs.NFC).Expr()); got != "false" {
			t.Fatalf("is normalized got %q; want false", got)
		}
		if got := syntaxText(t, session, qs.IsNotNormalized(qs.LiteralString("e\u0301"), qs.NFC).Expr()); got != "true" {
			t.Fatalf("is not normalized got %q; want true", got)
		}
	}

	localValue := qs.LiteralString("2026-01-02 03:04:05+02").Cast(qs.TypeTimestampTZ)
	if session.options.PostgreSQL >= qs.PostgreSQL17 {
		for _, local := range []qs.Expr{qs.AtLocal(localValue), localValue.AtLocal()} {
			if got := syntaxText(t, session, local); got != "2026-01-02 01:04:05" {
				t.Fatalf("AT LOCAL got %q; want 2026-01-02 01:04:05", got)
			}
		}
	}
	zone := qs.LiteralString("2026-01-02 03:04:05").Cast(qs.TypeTimestamp).AtTimeZone(qs.LiteralString("UTC"))
	if got := syntaxText(t, session, zone); !strings.HasPrefix(got, "2026-01-02 03:04:05") {
		t.Fatalf("AT TIME ZONE got %q", got)
	}

	if got := syntaxText(t, session, qs.CollationFor(qs.LiteralString("value").Collate("C"))); got == "" {
		t.Fatal("COLLATION FOR returned an empty collation")
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestSQLValueFunctionPrecision(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `SET TIME ZONE 'UTC'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		expr qs.Expr
	}{
		{"current_time", qs.CurrentTime(3)},
		{"current_timestamp", qs.CurrentTimestamp(3)},
		{"localtime", qs.LocalTime(3)},
		{"localtimestamp", qs.LocalTimestamp(3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := session.render(t, qs.Select(qs.Extract(qs.PartMicroseconds, tc.expr).Cast(qs.TypeInt8)))
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
		expr qs.Expr
	}{
		{"session_user", qs.SessionUser()},
		{"current_schema", qs.CurrentSchema()},
		{"current_catalog", qs.CurrentCatalog()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := syntaxText(t, session, tc.expr); got == "" {
				t.Fatalf("%s is empty", tc.name)
			}
		})
	}
}
