//go:build postgres

package integration_test

import (
	"encoding/json"
	"testing"

	"github.com/jacoelho/qx"
)

func TestWindowShorthandLiveSemantics(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `
		CREATE TEMP TABLE qx_window_fidelity(id integer PRIMARY KEY, v integer);
		INSERT INTO qx_window_fidelity VALUES (1, 1), (2, 1), (3, 3);`); err != nil {
		t.Fatal(err)
	}

	rowsFrame := qx.Window().OrderBy(qx.Asc("v"), qx.Asc("id")).Rows(qx.Preceding(1))
	rangeFrame := qx.Window().OrderBy(qx.Asc("v")).Range(qx.Preceding(1))
	groupsFrame := qx.Window().OrderBy(qx.Asc("v")).Groups(qx.Preceding(1))
	query := qx.Select(
		qx.Col("id"),
		qx.Sum(qx.Col("v")).Over(rowsFrame),
		qx.Sum(qx.Col("v")).Over(rangeFrame),
		qx.Sum(qx.Col("v")).Over(groupsFrame),
	).From("qx_window_fidelity").OrderBy(qx.Asc("id"))
	sql, args, err := query.ToSQL()
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
	ctx, conn := connect(t)
	query := qx.Explain(qx.Select(qx.Param("payload"))).Analyze(true).Serialize().Format(qx.ExplainJSON)
	sql, args, err := query.ToSQL()
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
