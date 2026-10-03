//go:build postgres

package integration_test

import (
	"encoding/json"
	"testing"

	"github.com/jacoelho/qs"
)

func TestWindowShorthandLiveSemantics(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `
		CREATE TEMP TABLE qs_window_fidelity(id integer PRIMARY KEY, v integer);
		INSERT INTO qs_window_fidelity VALUES (1, 1), (2, 1), (3, 3);`); err != nil {
		t.Fatal(err)
	}

	rowsFrame := qs.Window().OrderBy(qs.Asc("v"), qs.Asc("id")).Rows(qs.Preceding(1))
	rangeFrame := qs.Window().OrderBy(qs.Asc("v")).Range(qs.Preceding(1))
	groupsFrame := qs.Window().OrderBy(qs.Asc("v")).Groups(qs.Preceding(1))
	query := qs.Select(
		qs.Col("id"),
		qs.Sum(qs.Col("v")).Over(rowsFrame),
		qs.Sum(qs.Col("v")).Over(rangeFrame),
		qs.Sum(qs.Col("v")).Over(groupsFrame),
	).From("qs_window_fidelity").OrderBy(qs.Asc("id"))
	sql, args, err := session.render(t, query)
	if err != nil {
		t.Fatal(err)
	}
	result, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer result.Close()
	want := [][4]int64{{1, 1, 2, 2}, {2, 2, 2, 2}, {3, 4, 3, 5}}
	rowsSeen := 0
	for result.Next() {
		i := rowsSeen
		if i >= len(want) {
			t.Fatal("window query returned too many rows")
		}
		var got [4]int64
		if err := result.Scan(&got[0], &got[1], &got[2], &got[3]); err != nil {
			t.Fatal(err)
		}
		if got != want[i] {
			t.Fatalf("row %d = %v; want %v", i, got, want[i])
		}
		rowsSeen++
	}
	if err := result.Err(); err != nil {
		t.Fatal(err)
	}
	if rowsSeen != len(want) {
		t.Fatalf("window query returned %d rows; want %d", rowsSeen, len(want))
	}
}

func TestExplainBareSerializationLive(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	requireVersion(t, session, qs.PostgreSQL17)
	query := qs.Explain(qs.Select(qs.Param("payload"))).Analyze(true).Serialize().Format(qs.ExplainJSON)
	sql, args, err := session.render(t, query)
	if err != nil {
		t.Fatal(err)
	}
	var plan []byte
	if err := conn.QueryRow(ctx, sql, args...).Scan(&plan); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if len(plan) == 0 || !json.Valid(plan) {
		t.Fatalf("bare SERIALIZE returned invalid JSON: %q", plan)
	}
}
