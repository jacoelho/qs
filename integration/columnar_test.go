//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
)

func TestInsertFromArraysPairsColumnsWithArrays(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE qs_people (id integer PRIMARY KEY, first text, last text)`); err != nil {
		t.Fatal(err)
	}
	// The arrays are passed in an order other than the table's, and both text
	// columns have distinct values, so a pairing by position would swap them.
	insert := qs.InsertInto("qs_people").FromArrays(
		qs.NamedArray("last", qs.ArrayParam([]string{"Lovelace", "Hopper"}, qs.TypeText)),
		qs.NamedArray("id", qs.ArrayParam([]int32{1, 2}, qs.TypeInt4)),
		qs.NamedArray("first", qs.ArrayParam([]string{"Ada", "Grace"}, qs.TypeText)),
	)
	sql, args, err := session.render(t, insert)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := session.Exec(session, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if tag.RowsAffected() != 2 {
		t.Fatalf("%d rows inserted; want 2", tag.RowsAffected())
	}
	got := queryStrings(t, session, qs.Select(
		qs.Concat(qs.Col("id"), qs.LiteralString(":"), qs.Col("first"), qs.LiteralString(" "), qs.Col("last")),
	).From("qs_people").OrderBy(qs.Col("id").Asc()))
	if want := []string{"1:Ada Lovelace", "2:Grace Hopper"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q; want %q", got, want)
	}

	empty := qs.InsertInto("qs_people").FromArrays(
		qs.NamedArray("id", qs.ArrayParam([]int32(nil), qs.TypeInt4)),
		qs.NamedArray("first", qs.ArrayParam([]string(nil), qs.TypeText)),
	)
	sql, args, err = session.render(t, empty)
	if err != nil {
		t.Fatal(err)
	}
	if tag, err = session.Exec(session, sql, args...); err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("%s: %d rows, %v; want no rows", sql, tag.RowsAffected(), err)
	}
}

// The projection is written in an order other than the table's, and both text
// columns hold distinct values, so targets taken by position would swap them.
func TestInsertFromNamedPairsTargetsWithExpressions(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE qs_steps (position bigint, name text, kind text, source text)`); err != nil {
		t.Fatal(err)
	}
	read := qs.Select(qs.Concat(
		qs.Col("source"), qs.LiteralString(" "), qs.Col("position"), qs.LiteralString(":"),
		qs.Col("name"), qs.LiteralString("/"), qs.Col("kind"),
	)).From("qs_steps").OrderBy(qs.Col("source").Asc(), qs.Col("position").Asc())

	u := qs.UnnestAs("u",
		qs.NamedArray("name", qs.ArrayParam([]string{"build", "ship"}, qs.TypeText)),
		qs.NamedArray("kind", qs.ArrayParam([]string{"ci", "cd"}, qs.TypeText)),
	).WithOrdinality()
	arrays := qs.InsertInto("qs_steps").FromNamed(qs.Select(
		u.Col("kind"), qs.LiteralString("arrays").As("source"), u.Col("ordinality").As("position"), u.Col("name"),
	).FromExpr(u))

	e := qs.TableFunc(qs.JSONBArrayElements(
		qs.Param(`[{"name":"lint","kind":"ci"},{"name":"tag","kind":"cd"}]`).Cast(qs.TypeJSONB),
	)).WithOrdinality().As("e", "item", "position")
	item := qs.JSONBExpr(e.Col("item"))
	elements := qs.InsertInto("qs_steps").FromNamed(qs.Select(
		item.TextKey("kind").As("kind"), e.Col("position"), qs.LiteralString("json").As("source"), item.TextKey("name").As("name"),
	).FromExpr(e))

	for _, insert := range []qs.Statement{arrays, elements} {
		sql, args, err := session.render(t, insert)
		if err != nil {
			t.Fatal(err)
		}
		if tag, err := session.Exec(session, sql, args...); err != nil || tag.RowsAffected() != 2 {
			t.Fatalf("%s: %d rows, %v; want 2 rows", sql, tag.RowsAffected(), err)
		}
	}
	want := []string{"arrays 1:build/ci", "arrays 2:ship/cd", "json 1:lint/ci", "json 2:tag/cd"}
	if got := queryStrings(t, session, read); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestUnnestAsNamesColumnsAndOrdinality(t *testing.T) {
	t.Parallel()
	session := connect(t)
	u := qs.UnnestAs("u",
		qs.NamedArray("kind", qs.ArrayParam([]string{"b", "a", "c"}, qs.TypeText)),
		qs.NamedArray("qty", qs.ArrayParam([]int32{20, 10, 30}, qs.TypeInt4)),
	).WithOrdinality()
	got := queryStrings(t, session, qs.Select(
		qs.Concat(u.Col("ordinality"), qs.LiteralString(":"), u.Col("kind"), qs.LiteralString("="), u.Col("qty")),
	).FromExpr(u).OrderBy(u.Col("kind").Asc()))
	if want := []string{"2:a=10", "1:b=20", "3:c=30"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestFieldEqAnyMatchesArrayMembers(t *testing.T) {
	t.Parallel()
	session := connect(t)
	v := qs.TableFunc(qs.GenerateSeries(qs.LiteralInt(1), qs.LiteralInt(3))).As("v", "n")
	n := qs.Typed[int32](v.Col("n"))
	for _, tc := range []struct {
		name   string
		values []int32
		want   []string
	}{
		{"some", []int32{3, 1, 7}, []string{"1", "3"}},
		{"empty", []int32{}, nil},
		{"nil", nil, nil},
	} {
		got := queryStrings(t, session, qs.Select(n.Expr().Cast(qs.TypeText)).FromExpr(v).
			Where(n.EqAny(tc.values, qs.TypeInt4)).OrderBy(n.Asc()))
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: got %q; want %q", tc.name, got, tc.want)
		}
	}
}

// A selector is the same expression at each use, so PostgreSQL accepts it in
// GROUP BY and ORDER BY beside the select list. Two bound parameters holding
// the same key are different expressions and the statement is rejected.
func TestJSONSelectorsGroupAndOrder(t *testing.T) {
	t.Parallel()
	session := connect(t)
	if _, err := session.Exec(session, `CREATE TEMP TABLE qs_json_groups (payload jsonb);
		INSERT INTO qs_json_groups VALUES
			('{"kind":"a","tags":["x","y"],"it''s":{"n":1}}'),
			('{"kind":"b","tags":["z"]}'),
			('{"kind":"a","tags":["w","last"]}')`); err != nil {
		t.Fatal(err)
	}
	doc := qs.JSONBExpr(qs.Col("payload"))
	kind := func() qs.Field[string] { return doc.TextKey("kind") }
	got := queryStrings(t, session, qs.Select(
		qs.Concat(kind().Expr(), qs.LiteralString("="), qs.CountAll()),
	).From("qs_json_groups").GroupByExpr(kind().Expr()).OrderBy(kind().Asc()))
	if want := []string{"a=2", "b=1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped: got %q; want %q", got, want)
	}

	last := func() qs.Field[string] { return doc.Key("tags").TextIndex(-1) }
	got = queryStrings(t, session, qs.Select(last().Expr()).Distinct().
		From("qs_json_groups").OrderBy(last().Asc()))
	if want := []string{"last", "y", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("distinct: got %q; want %q", got, want)
	}

	got = queryStrings(t, session, qs.Select(doc.Path("it's", "n").Expr().Cast(qs.TypeText)).
		From("qs_json_groups").Where(doc.HasKey("it's"), doc.PathExists(`$."it's".n ? (@ == 1)`)))
	if want := []string{"1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("quoted key: got %q; want %q", got, want)
	}

	// A selector chosen at run time is bound; the index needs its cast.
	got = queryStrings(t, session, qs.Select(
		qs.JSONGetText(qs.JSONGet(qs.Col("payload"), qs.Param("tags")), qs.Param(int32(1)).Cast(qs.TypeInt4)),
	).From("qs_json_groups").Where(doc.TextKey("kind").Eq("a"), doc.Key("tags").TextIndex(1).IsNotNull()).
		OrderBy(qs.Ordinal(1).Asc()))
	if want := []string{"last", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bound selectors: got %q; want %q", got, want)
	}

	bound := qs.Select(qs.JSONGetText(qs.Col("payload"), qs.Param("kind")), qs.CountAll()).
		From("qs_json_groups").GroupByExpr(qs.JSONGetText(qs.Col("payload"), qs.Param("kind")))
	sql, args, err := session.render(t, bound)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := session.Query(session, sql, args...)
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	if err == nil {
		t.Fatalf("%s: grouping by a second bound key succeeded; the literal selectors would be unnecessary", sql)
	}
}

func TestJSONKeyBuildsObjectMembers(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, 16)
	object := qs.JSONObject(
		qs.JSONKey("id", qs.Param(7).Cast(qs.TypeInt4)),
		qs.JSONKey("it's", qs.FormatJSON(qs.Param(`{"a":1}`).Cast(qs.TypeText))),
	).Returning(qs.TypeJSONB)
	got := queryStrings(t, session, qs.Select(object.Expr().Cast(qs.TypeText)))
	if want := []string{`{"id": 7, "it's": {"a": 1}}`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q; want %q", got, want)
	}
}
