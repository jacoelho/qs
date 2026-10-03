//go:build postgres

package integration_test

import (
	"testing"

	"github.com/jacoelho/qs"
)

func TestUpsertLiteralDottedColumn(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE settings (id integer PRIMARY KEY, "a.b" text);
		INSERT INTO settings VALUES (42, 'old')`); err != nil {
		t.Fatal(err)
	}
	statement := qs.InsertInto("settings").Columns("id", "a.b").Values(qs.Value(42), qs.Value("new")).
		OnConflict(qs.ConflictColumns("id").DoUpdateSlice(qs.SetAllExcluded("a.b"))).
		Returning(qs.Ident("a.b"))
	sql, args, err := session.render(t, statement)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "new" {
		t.Fatalf("upserted literal column: got %q, want %q", value, "new")
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestLiteralRelationColumnIdentity(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qs_literal_relation_columns ("a.b" text, "*" text);
		INSERT INTO qs_literal_relation_columns VALUES ('dotted', 'star')`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		relation qs.Relation
		column   qs.Expr
		want     string
	}{
		{"table", qs.Table("qs_literal_relation_columns"), qs.Table("qs_literal_relation_columns").Col("a.b"), "dotted"},
		{"table_ident", qs.TableIdent("qs_literal_relation_columns"), qs.TableIdent("qs_literal_relation_columns").Col("a.b"), "dotted"},
		{"alias", qs.Table("qs_literal_relation_columns").As("r"), qs.Table("qs_literal_relation_columns").As("r").Col("a.b"), "dotted"},
		{"literal_star_column", qs.Table("qs_literal_relation_columns"), qs.Table("qs_literal_relation_columns").Col("*"), "star"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := qs.Select(tc.column.Cast(qs.TypeText)).FromExpr(tc.relation)
			sql, args, err := session.render(t, query)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := conn.QueryRow(ctx, sql, args...).Scan(&got); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
			if got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}
