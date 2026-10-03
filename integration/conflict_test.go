//go:build postgres

package integration_test

import (
	"testing"

	"github.com/jacoelho/qs"
)

func TestUpsertLiteralDottedColumn(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE settings (id integer PRIMARY KEY, "a.b" text);
		INSERT INTO settings VALUES (42, 'old')`); err != nil {
		t.Fatal(err)
	}
	statement := qs.InsertInto("settings").Columns("id", "a.b").Values(qs.Value(42), qs.Value("new")).
		OnConflict(qs.ConflictColumns("id").DoUpdateSlice(qs.SetAllExcluded("a.b"))).
		Returning(qs.Ident("a.b"))
	sql, args, err := statement.ToSQL()
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
