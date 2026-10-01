//go:build postgres

package integration_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jacoelho/qx"
)

func TestSQLJSONNullAndAbsentPolicies(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)

	query := qx.Select(
		qx.JSONObject(qx.JSONPair(qx.Param("key").Cast(qx.Text), qx.NullExpr(qx.Int4))).Expr().Cast(qx.Text),
		qx.JSONObject(qx.JSONPair(qx.Param("key").Cast(qx.Text), qx.NullExpr(qx.Int4))).AbsentOnNull().Expr().Cast(qx.Text),
		qx.JSONArray(qx.NullExpr(qx.Int4)).Expr().Cast(qx.Text),
		qx.JSONArray(qx.NullExpr(qx.Int4)).NullOnNull().Expr().Cast(qx.Text),
		qx.JSONArrayAggregate(qx.NullExpr(qx.Int4)).Expr().Cast(qx.Text),
		qx.JSONArrayAggregate(qx.NullExpr(qx.Int4)).NullOnNull().Expr().Cast(qx.Text),
		qx.JSONObjectAggregate(qx.Param("key").Cast(qx.Text), qx.NullExpr(qx.Int4)).Expr().Cast(qx.Text),
		qx.JSONObjectAggregate(qx.Param("key").Cast(qx.Text), qx.NullExpr(qx.Int4)).AbsentOnNull().Expr().Cast(qx.Text),
	)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var got [8]pgtype.Text
	if err := conn.QueryRow(ctx, sql, args...).Scan(
		&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6], &got[7],
	); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	want := []string{`{"key" : null}`, `{}`, `[]`, `[null]`, `[]`, `[null]`, `{ "key" : null }`, `{  }`}
	for i, value := range got {
		if !value.Valid {
			t.Fatalf("result %d is SQL NULL; want %q", i, want[i])
		}
		if value.String != want[i] {
			t.Fatalf("result %d = %q; want %q", i, value.String, want[i])
		}
	}
}

func TestSQLJSONDuplicateKeySemantics(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)

	// JSON retains duplicate object members when uniqueness is omitted. Compare
	// the encoded text here because duplicate members are intentionally lost by
	// json.Unmarshal and jsonb canonicalization.
	duplicate := qx.Select(
		qx.JSONObject(
			qx.JSONPair(qx.Param("a").Cast(qx.Text), qx.Param(1).Cast(qx.Int4)),
			qx.JSONPair(qx.Param("a").Cast(qx.Text), qx.Param(2).Cast(qx.Int4)),
		).Expr().Cast(qx.Text),
	)
	sql, args, err := duplicate.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&text); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if text != `{"a" : 1, "a" : 2}` {
		t.Fatalf("duplicate JSON object = %q; want duplicate members preserved", text)
	}

	unique := qx.Select(qx.JSONObject(
		qx.JSONPair(qx.Param("a").Cast(qx.Text), qx.Param(1).Cast(qx.Int4)),
		qx.JSONPair(qx.Param("a").Cast(qx.Text), qx.Param(2).Cast(qx.Int4)),
	).WithUniqueKeys().Expr())
	sql, args, err = unique.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, sql, args...).Scan(&text); err == nil {
		t.Fatal("WITH UNIQUE KEYS accepted duplicate dynamic keys")
	}

	parsedUnique := qx.Select(qx.JSONParse(
		qx.JSONInputExpr(qx.Param(`{"a":1,"a":2}`)).FormatJSON(),
	).WithUniqueKeys().Expr())
	sql, args, err = parsedUnique.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var parsed string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&parsed); err == nil {
		t.Fatal("JSON(... WITH UNIQUE KEYS) accepted duplicate parsed keys")
	}

	// IS JSON reports duplicate-key validity without throwing; this is a
	// separate predicate contract from constructor/parser uniqueness errors.
	predicate := qx.Select(
		qx.IsJSON(qx.Param(`{"a":1,"a":2}`).Cast(qx.Text)).Expr(),
		qx.IsJSON(qx.Param(`{"a":1,"a":2}`).Cast(qx.Text)).WithUniqueKeys().Expr(),
	)
	sql, args, err = predicate.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var ordinary, uniqueOK bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&ordinary, &uniqueOK); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if !ordinary || uniqueOK {
		t.Fatalf("IS JSON duplicate semantics = ordinary %t unique %t; want true false", ordinary, uniqueOK)
	}
}

func TestSQLJSONArrayAggregateOrderingAndFilter(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qx_json_constructor_values(v int, keep boolean);
		INSERT INTO qx_json_constructor_values VALUES (3, true), (1, false), (5, true), (2, false)`); err != nil {
		t.Fatal(err)
	}

	array := qx.JSONArrayAggregate(qx.Col("v")).
		OrderBy(qx.Desc("v")).
		Filter(qx.Eq("keep", true)).
		Expr().Cast(qx.Text)
	if got := queryStrings(t, ctx, conn, qx.Select(array).From("qx_json_constructor_values")); !reflect.DeepEqual(got, []string{`[5, 3]`}) {
		t.Fatalf("ordered filtered array aggregate = %#v; want [5, 3]", got)
	}

	object := qx.JSONObjectAggregate(qx.Col("v"), qx.Col("v")).
		Filter(qx.Eq("keep", true)).
		Expr().Cast(qx.Text)
	got := queryStrings(t, ctx, conn, qx.Select(object).From("qx_json_constructor_values"))
	if len(got) != 1 {
		t.Fatalf("object aggregate rows = %#v; want one row", got)
	}
	var members map[string]int
	if err := json.Unmarshal([]byte(got[0]), &members); err != nil {
		t.Fatalf("object aggregate %q: %v", got[0], err)
	}
	if want := map[string]int{"3": 3, "5": 5}; !reflect.DeepEqual(members, want) {
		t.Fatalf("filtered object aggregate = %#v; want %#v", members, want)
	}
}

func TestSQLJSONParseScalarAndSerialize(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)

	// JSON() parses a formatted value structurally, while JSON_SCALAR() treats
	// ordinary text as one JSON string. JSON_SERIALIZE(FORMAT JSON) preserves
	// the source's meaningful serialized bytes.
	query := qx.Select(
		qx.JSONParse(qx.JSONInputExpr(qx.Param(` [2,1] `).Cast(qx.Text)).FormatJSON()).Expr().Cast(qx.Text),
		qx.JSONScalar(qx.Param(` [2,1] `).Cast(qx.Text)).Cast(qx.Text),
		qx.JSONSerialize(qx.JSONInputExpr(qx.Param(` { "a" : 1 } `).Cast(qx.Text)).FormatJSON()).Returning(qx.Text).Expr(),
	)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var parsed, scalar, serialized string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&parsed, &scalar, &serialized); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	var parsedValue any
	if err := json.Unmarshal([]byte(parsed), &parsedValue); err != nil {
		t.Fatalf("parsed JSON %q: %v", parsed, err)
	}
	if want := []any{float64(2), float64(1)}; !reflect.DeepEqual(parsedValue, want) {
		t.Fatalf("parsed JSON = %#v; want %#v", parsedValue, want)
	}
	if scalar != `" [2,1] "` {
		t.Fatalf("JSON_SCALAR text = %q; want quoted scalar", scalar)
	}
	if serialized != ` { "a" : 1 } ` {
		t.Fatalf("JSON_SERIALIZE text = %q; want source bytes preserved", serialized)
	}
}
