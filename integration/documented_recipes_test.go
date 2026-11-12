//go:build postgres

package integration_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

// The guide and godoc recipes for syntax qs has no dedicated helper for must be
// SQL that PostgreSQL accepts, with the meaning the documentation gives them.
//
//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestDocumentedExpressionRecipes(t *testing.T) {
	t.Parallel()
	session := connect(t)
	start := qs.LiteralString("2026-01-01 00:00:00").Cast(qs.TypeTimestamp)
	doc := qs.JSONBParam(`{"quantity": 3, "tags": ["a"], "name": "x"}`)
	plain := qs.JSONParam(`{"tags": ["a"], "n": null}`)
	text := func(e qs.Expr) qs.Expr { return e.Cast(qs.TypeText) }
	for name, tc := range map[string]struct {
		expr qs.Expr
		want string
	}{
		"interval literal cast":   {text(start.Add(qs.LiteralString("1 second").Cast(qs.TypeInterval))), "2026-01-01 00:00:01"},
		"interval parameter cast": {text(start.Sub(qs.Param("7 days").Cast(qs.TypeInterval))), "2025-12-25 00:00:00"},
		"make_interval named":     {text(start.Add(qs.Call("make_interval", qs.NamedArg("secs", qs.Param(30))))), "2026-01-01 00:00:30"},
		"md5":                     {qs.Call("md5", qs.Param("abc").Cast(qs.TypeText)), "900150983cd24fb0d6963f7d28e17f72"},
		"right":                   {qs.Call("right", qs.Param("abcdef").Cast(qs.TypeText), qs.LiteralInt(4)), "cdef"},
		"trim characters":         {qs.TrimSyntax(qs.LiteralString("--a-b--"), qs.TrimBothDirection, qs.LiteralString("-")), "a-b"},
		"jsonb cast":              {text(doc.Key("quantity").Cast(qs.TypeInt4).Add(qs.LiteralInt(1))), "4"},
		"jsonb type of array":     {doc.Key("tags").TypeOf().Expr(), "array"},
		"jsonb type of number":    {doc.Key("quantity").TypeOf().Expr(), "number"},
		"jsonb type of document":  {doc.TypeOf().Expr(), "object"},
		"json type of array":      {plain.Key("tags").TypeOf().Expr(), "array"},
		"json type of null":       {plain.Key("n").TypeOf().Expr(), "null"},
		"json cast":               {plain.Key("tags").Cast(qs.TypeText), `["a"]`},
		"field cast":              {text(qs.Typed[int](qs.LiteralInt(7)).Cast(qs.TypeInt8)), "7"},
	} {
		t.Run(name, func(t *testing.T) {
			got := queryStrings(t, session, qs.Select(tc.expr))
			if !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAssignmentSliceNameOrderingLateralAndPointerRows(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `
		CREATE TABLE orders (id integer PRIMARY KEY, region text NOT NULL, note text);
		CREATE TABLE items (order_id integer NOT NULL, sku text NOT NULL, position integer NOT NULL);
		INSERT INTO items VALUES (1, 'a', 1), (1, 'b', 2), (1, 'c', 3), (3, 'z', 1);`); err != nil {
		t.Fatal(err)
	}
	for _, order := range []struct {
		id     int
		region string
		note   string
	}{{1, "eu", "first"}, {2, "us", ""}, {3, "eu", ""}} {
		assignments := []qs.Assignment{qs.Set("id", qs.Value(order.id)), qs.Set("region", qs.Value(order.region))}
		if order.note != "" {
			assignments = append(assignments, qs.Set("note", qs.Value(order.note)))
		}
		query, args, err := session.render(t, qs.InsertInto("orders").Set(assignments))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	type row struct {
		ID     int32
		Region string
		Note   *string
		SKU    *string
	}
	o := qs.Table("orders").As("o")
	newest := qs.Select(qs.Col("sku")).
		From("items").
		Where(qs.Col("items", "order_id").Eq(o.Col("id"))).
		OrderBy(qs.DescCols("position")...).
		Limit(1)
	item := qs.Lateral(qs.Subquery(newest, "i"))
	p := projection.New(
		projection.Column(o.Col("id"), func(r *row) *int32 { return &r.ID }),
		projection.Column(o.Col("region"), func(r *row) *string { return &r.Region }),
		projection.Column(o.Col("note"), func(r *row) **string { return &r.Note }),
		projection.Column(item.Col("sku"), func(r *row) **string { return &r.SKU }),
	)
	// The names are unqualified, which the join leaves unambiguous: only orders
	// has region and id.
	query, args, err := session.render(t, qs.Select(p.Expressions()...).
		From(o).
		LeftJoin(item, qs.True()).
		OrderBy(qs.AscCols("region")...).
		OrderBy(qs.DescCols("id")...))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	got, err := p.CollectPtr(rows)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	first, c, z := "first", "c", "z"
	want := []*row{
		{ID: 3, Region: "eu", SKU: &z},
		{ID: 1, Region: "eu", Note: &first, SKU: &c},
		{ID: 2, Region: "us"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\ngot  %s\nwant %s", query, describeRows(t, got), describeRows(t, want))
	}
}

func describeRows(t *testing.T, rows any) string {
	t.Helper()
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
