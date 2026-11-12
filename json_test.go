package qs

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func TestJSONDocuments(t *testing.T) {
	t.Parallel()

	doc := JSONBExpr(Col("payload"))
	runCases(t, []renderCase{
		{"key", Select(doc.Key("0").Expr()), `SELECT ("payload" -> E'0')`, nil},
		{"index", Select(doc.Index(0).Expr()), `SELECT ("payload" -> 0)`, nil},
		{"negative_index", Select(doc.Index(-1).Expr()), `SELECT ("payload" -> -1)`, nil},
		{"text_key", Select(doc.TextKey("name").Expr()).Where(doc.TextKey("name").Eq("Ada")), `SELECT ("payload" ->> E'name') WHERE (("payload" ->> E'name') = $1)`, []any{"Ada"}},
		{
			"text_key_grouped",
			Select(doc.TextKey("kind").Expr(), CountAll()).
				From("events").
				GroupBy(doc.TextKey("kind").Expr()),
			`SELECT ("payload" ->> E'kind'), count(*) FROM "events" GROUP BY ("payload" ->> E'kind')`,
			nil,
		},
		{"text_index", Select(doc.TextIndex(2).Expr()), `SELECT ("payload" ->> 2)`, nil},
		{"path", Select(doc.Path("items", "0").Expr()), `SELECT ("payload" #> (ARRAY[E'items', E'0'])::text[])`, nil},
		{"path_text", Select(doc.PathText("items", "0", "name").Expr()), `SELECT ("payload" #>> (ARRAY[E'items', E'0', E'name'])::text[])`, nil},
		{"empty_path", Select(doc.Path().Expr()), `SELECT ("payload" #> (ARRAY[])::text[])`, nil},
		{"contains", Select(doc.Contains(JSONBParam(`{"active":true}`)).Expr()), `SELECT ("payload" @> ($1)::jsonb)`, []any{`{"active":true}`}},
		{"contained_by", Select(doc.ContainedBy(JSONBExpr(Col("other"))).Expr()), `SELECT ("payload" <@ "other")`, nil},
		{"has_key", Select(doc.HasKey("a").Expr()), `SELECT ("payload" ? E'a')`, nil},
		{"has_any", Select(doc.HasAnyKeys("a", "b").Expr()), `SELECT ("payload" ?| (ARRAY[E'a', E'b'])::text[])`, nil},
		{"has_all", Select(doc.HasAllKeys("a", "b").Expr()), `SELECT ("payload" ?& (ARRAY[E'a', E'b'])::text[])`, nil},
		{"delete_key", Select(doc.DeleteKey("0").Expr()), `SELECT ("payload" - E'0')`, nil},
		{"delete_index", Select(doc.DeleteIndex(0).Expr()), `SELECT ("payload" - 0)`, nil},
		{"delete_negative_index", Select(doc.DeleteIndex(-1).Expr()), `SELECT ("payload" - -1)`, nil},
		{"delete_keys", Select(doc.DeleteKeys("a", "b").Expr()), `SELECT ("payload" - (ARRAY[E'a', E'b'])::text[])`, nil},
		{"delete_path", Select(doc.DeletePath("a", "0").Expr()), `SELECT ("payload" #- (ARRAY[E'a', E'0'])::text[])`, nil},
		{"concat", Select(doc.Concat(JSONBParam(`{"b":2}`)).Expr()), `SELECT ("payload" || ($1)::jsonb)`, []any{`{"b":2}`}},
		{"path_exists", Select(doc.PathExists("$.a").Expr()), `SELECT ("payload" @? (E'$.a')::jsonpath)`, nil},
		{"path_matches", Select(doc.PathMatches("$.a > 1").Expr()), `SELECT ("payload" @@ (E'$.a > 1')::jsonpath)`, nil},
		{"json_chain", Select(JSONExpr(Col("doc")).Key("items").Index(-1).TextKey("name").As("name")), `SELECT ((("doc" -> E'items') -> -1) ->> E'name') AS "name"`, nil},
		{"json_index", Select(JSONExpr(Col("doc")).TextIndex(0).Expr()), `SELECT ("doc" ->> 0)`, nil},
		{
			"json_path",
			Select(JSONExpr(Col("doc")).Path("a").Expr(), JSONExpr(Col("doc")).PathText().Expr()),
			`SELECT ("doc" #> (ARRAY[E'a'])::text[]), ("doc" #>> (ARRAY[])::text[])`,
			nil,
		},
		{"cast", Select(doc.Key("n").Cast(TypeInt4), JSONExpr(Col("doc")).Key("n").Cast(TypeText)),
			`SELECT (("payload" -> E'n'))::integer, (("doc" -> E'n'))::text`, nil},
		{"json_type_of", Select(JSONExpr(Col("doc")).Key("a").TypeOf().Expr(), JSONTypeOf(Param("[]").Cast(TypeJSON))),
			`SELECT json_typeof(("doc" -> E'a')), json_typeof(($1)::json)`, []any{"[]"}},
		{"type_of", Select(doc.Key("a").TypeOf().As("kind")).Where(doc.TypeOf().Eq("object")),
			`SELECT jsonb_typeof(("payload" -> E'a')) AS "kind" WHERE (jsonb_typeof("payload") = $1)`, []any{"object"}},
		{
			"document_as_case_result",
			Select(Case().When(doc.Key("a").TypeOf().Eq("array"), doc.Key("a").Expr()).Else(doc.Expr()).End()),
			`SELECT CASE WHEN (jsonb_typeof(("payload" -> E'a')) = $1) THEN ("payload" -> E'a') ELSE "payload" END`,
			[]any{"array"},
		},
		{"encoded_json", Select(JSONParam(`{"a":1}`).As("doc")), `SELECT ($1)::json AS "doc"`, []any{`{"a":1}`}},
		{"quote_in_key", Select(doc.Key(`x'); DROP TABLE users; --`).Expr()), `SELECT ("payload" -> E'x''); DROP TABLE users; --')`, nil},
		{"backslash_in_key", Select(doc.Key(`a\'b`).Expr()), `SELECT ("payload" -> E'a\\''b')`, nil},
		{"bound_key", Select(JSONGet(doc.Expr(), Param("k"))), `SELECT ("payload" -> $1)`, []any{"k"}},
		{"null", Select(doc.Key("a").IsNull().Expr(), JSONExpr(Col("doc")).IsNotNull().Expr()), `SELECT (("payload" -> E'a') IS NULL), ("doc" IS NOT NULL)`, nil},
		{
			"update",
			Update("events").Set(doc.Set(doc.Concat(JSONBParam(`{"active":true}`)))).AllRows(),
			`UPDATE "events" SET "payload" = ("payload" || ($1)::jsonb)`,
			[]any{`{"active":true}`},
		},
	})
}

func TestJSONSelectorOwnership(t *testing.T) {
	t.Parallel()

	parts := []string{"items", "0"}
	doc := JSONBExpr(Col("doc"))
	path := doc.Path(parts...)
	parts[0] = "changed"
	checkSQL(t, Select(doc.Expr(), path.Expr(), path.TextKey("name").Expr()), `SELECT "doc", ("doc" #> (ARRAY[E'items', E'0'])::text[]), (("doc" #> (ARRAY[E'items', E'0'])::text[]) ->> E'name')`)

	inner := Select(JSONBParam(`{"name":"Ada"}`).Expr())
	original := Select(JSONBExpr(Scalar(inner)).TextKey("name").Expr())
	checkSQL(t, original, `SELECT ((SELECT ($1)::jsonb) ->> E'name')`, `{"name":"Ada"}`)
	checkError(t, Select(JSONBExpr(Scalar(inner.Columns(Param(1)))).TextKey("name").Expr()), ErrInvalid)
}

func TestJSONValidationAndNumbering(t *testing.T) {
	t.Parallel()

	for _, expr := range []Expr{
		JSONDocument{}.Key("a").Expr(), JSONBDocument{}.Index(0).Expr(), Param(JSONBExpr(Col("doc"))),
		JSONBExpr(Col("doc")).Key("a\x00b").Expr(), JSONBExpr(Col("doc")).Path("a", "\xff").Expr(),
	} {
		if _, _, err := ExprToSQL(expr); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid JSON expression accepted: %v", err)
		}
	}
	if strconv.IntSize == 64 {
		for _, index := range []int64{-1<<31 - 1, 1 << 31} {
			for _, expr := range []Expr{JSONExpr(Col("doc")).Index(int(index)).Expr(), JSONBExpr(Col("doc")).DeleteIndex(int(index)).Expr()} {
				if _, _, err := ExprToSQL(expr); !errors.Is(err, ErrInvalid) {
					t.Fatalf("out-of-range index %d: %v", index, err)
				}
			}
		}
	}
	query := Select(JSONBExpr(Col("doc")).Index(0).TextKey("name").Expr()).
		With(CTE("source", Select(JSONBParam(`[]`).Expr()).Where(Col("id").Eq(Param(4)))))
	buf, args, err := query.AppendSQL([]byte("prefix: "), []any{"existing"})
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != `prefix: WITH "source" AS (SELECT ($2)::jsonb WHERE ("id" = $3)) SELECT (("doc" -> 0) ->> E'name')` || !reflect.DeepEqual(args, []any{"existing", `[]`, 4}) {
		t.Fatalf("SQL=%s args=%#v", buf, args)
	}
}

func typedJSONQuery() SelectBuilder {
	doc := JSONBExpr(Col("payload"))
	return Select(doc.PathText("items", "0", "name").As("name")).
		From("events").
		Where(
			doc.HasAllKeys("items", "active"),
			doc.Contains(JSONBParam(`{"active":true}`)),
			doc.Key("items").Index(-1).TextKey("name").Eq("Ada"),
		)
}

func TestJSONReusableAppendAllocations(t *testing.T) {
	query := typedJSONQuery()
	checkSQL(t, query, `SELECT ("payload" #>> (ARRAY[E'items', E'0', E'name'])::text[]) AS "name" FROM "events" WHERE ("payload" ?& (ARRAY[E'items', E'active'])::text[]) AND ("payload" @> ($1)::jsonb) AND (((("payload" -> E'items') -> -1) ->> E'name') = $2)`, `{"active":true}`, "Ada")
	buf, args := make([]byte, 0, 2048), make([]any, 0, 16)
	var err error
	allocations := testing.AllocsPerRun(100, func() {
		buf, args, err = query.AppendSQL(
			buf[:0],
			args[:0],
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocations != 0 {
		t.Fatalf("prebuilt JSON query with reusable storage: %g allocations", allocations)
	}
}
