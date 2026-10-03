//go:build postgres

package integration_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jacoelho/qs"
)

func dropUtilityRelation(ctx context.Context, conn *pgx.Conn, name string) error {
	identifier := (pgx.Identifier{name}).Sanitize()
	_, err := conn.Exec(ctx, `DO $$
BEGIN
  BEGIN
    EXECUTE 'DROP TABLE IF EXISTS `+identifier+` CASCADE';
  EXCEPTION WHEN wrong_object_type THEN
    EXECUTE 'DROP MATERIALIZED VIEW IF EXISTS `+identifier+` CASCADE';
  END;
END $$`)
	return err
}

func TestQueryUtilitiesAgainstPostgres(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	const (
		ctasTable    = "qs_utility_ctas"
		noDataTable  = "qs_utility_nodata"
		intoTable    = "qs_utility_into"
		materialized = "qs_utility_mv"
		cursor       = "qs_utility_cursor"
		executeName  = "qs_utility_execute"
	)
	for _, name := range []string{ctasTable, noDataTable, intoTable, materialized} {
		if err := dropUtilityRelation(ctx, conn, name); err != nil {
			t.Fatal(err)
		}
	}
	ctas := qs.CreateTableAs(ctasTable, qs.Select(qs.LiteralInt(7).As("value")))
	if sql, args, err := session.render(t, ctas); err != nil {
		t.Fatal(err)
	} else if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("CTAS: %v", err)
	}
	var value int
	if err := conn.QueryRow(ctx, `SELECT "value" FROM "`+ctasTable+`"`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 7 {
		t.Fatalf("CTAS value=%d; want 7", value)
	}

	noData := qs.CreateTableAs(noDataTable, qs.Select(qs.LiteralInt(8).As("value"))).Columns("value").WithNoData()
	if sql, args, err := session.render(t, noData); err != nil {
		t.Fatal(err)
	} else if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("CTAS WITH NO DATA: %v", err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM "`+noDataTable+`"`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("WITH NO DATA count=%d; want 0", count)
	}

	into := qs.Select(qs.LiteralInt(9).As("value")).Into(intoTable)
	if sql, args, err := session.render(t, into); err != nil {
		t.Fatal(err)
	} else if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("SELECT INTO: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT "value" FROM "`+intoTable+`"`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 9 {
		t.Fatalf("SELECT INTO value=%d; want 9", value)
	}

	mview := qs.MaterializedViewAs(materialized, qs.Select(qs.LiteralInt(10).As("value")))
	if sql, args, err := session.render(t, mview); err != nil {
		t.Fatal(err)
	} else if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("materialized view: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT "value" FROM "`+materialized+`"`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 10 {
		t.Fatalf("materialized view value=%d; want 10", value)
	}

	if _, err := conn.Exec(ctx, `PREPARE "`+executeName+`"(int) AS SELECT $1 + 1`); err != nil {
		t.Fatal(err)
	}
	var executed int
	literal := qs.Execute(executeName, qs.LiteralInt(4))
	if sql, args, err := session.render(t, literal); err != nil {
		t.Fatal(err)
	} else if err := conn.QueryRow(ctx, sql, args...).Scan(&executed); err != nil {
		t.Fatalf("literal EXECUTE: %v", err)
	}
	if executed != 5 {
		t.Fatalf("literal EXECUTE result=%d; want 5", executed)
	}
	if err := conn.QueryRow(ctx, `EXECUTE "`+executeName+`" ($1)`, 6).Scan(&executed); err == nil {
		t.Fatalf("PostgreSQL unexpectedly accepted an extended-protocol EXECUTE argument")
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cursorQuery := qs.DeclareCursor(cursor, qs.Select(qs.Col("value")).From(ctasTable)).WithHold()
	sql, args, err := session.render(t, cursorQuery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("DECLARE CURSOR: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `FETCH ALL FROM "`+cursor+`"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("held cursor returned no rows: %v", rows.Err())
	}
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != 7 {
		t.Fatalf("held cursor value=%d; want 7", value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := conn.Exec(ctx, `CLOSE "`+cursor+`"`); err != nil {
		t.Fatal(err)
	}
}
