package qs

import (
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func TestJSONDocuments(t *testing.T) {
	t.Parallel()

	doc := JSONBCol("payload")
	runCases(t, []renderCase{
		{"key", Select(doc.Key("0").Expr()), `SELECT ("payload" -> $1)`, []any{"0"}},
		{"index", Select(doc.Index(0).Expr()), `SELECT ("payload" -> ($1)::integer)`, []any{int32(0)}},
		{"negative_index", Select(doc.Index(-1).Expr()), `SELECT ("payload" -> ($1)::integer)`, []any{int32(-1)}},
		{"text_key", Select(doc.TextKey("name").Expr()).Where(doc.TextKey("name").Eq("Ada")), `SELECT ("payload" ->> $1) WHERE (("payload" ->> $2) = $3)`, []any{"name", "name", "Ada"}},
		{"text_index", Select(doc.TextIndex(2).Expr()), `SELECT ("payload" ->> ($1)::integer)`, []any{int32(2)}},
		{"path", Select(doc.Path("items", "0").Expr()), `SELECT ("payload" #> (ARRAY[$1, $2])::text[])`, []any{"items", "0"}},
		{"path_text", Select(doc.PathText("items", "0", "name").Expr()), `SELECT ("payload" #>> (ARRAY[$1, $2, $3])::text[])`, []any{"items", "0", "name"}},
		{"empty_path", Select(doc.Path().Expr()), `SELECT ("payload" #> (ARRAY[])::text[])`, nil},
		{"contains", Select(doc.Contains(JSONBParam(`{"active":true}`)).Expr()), `SELECT ("payload" @> ($1)::jsonb)`, []any{`{"active":true}`}},
		{"contained_by", Select(doc.ContainedBy(JSONBCol("other")).Expr()), `SELECT ("payload" <@ "other")`, nil},
		{"has_key", Select(doc.HasKey("a").Expr()), `SELECT ("payload" ? $1)`, []any{"a"}},
		{"has_any", Select(doc.HasAnyKeys("a", "b").Expr()), `SELECT ("payload" ?| (ARRAY[$1, $2])::text[])`, []any{"a", "b"}},
		{"has_all", Select(doc.HasAllKeys("a", "b").Expr()), `SELECT ("payload" ?& (ARRAY[$1, $2])::text[])`, []any{"a", "b"}},
		{"delete_key", Select(doc.DeleteKey("0").Expr()), `SELECT ("payload" - $1)`, []any{"0"}},
		{"delete_index", Select(doc.DeleteIndex(0).Expr()), `SELECT ("payload" - ($1)::integer)`, []any{int32(0)}},
		{"delete_keys", Select(doc.DeleteKeys("a", "b").Expr()), `SELECT ("payload" - (ARRAY[$1, $2])::text[])`, []any{"a", "b"}},
		{"delete_path", Select(doc.DeletePath("a", "0").Expr()), `SELECT ("payload" #- (ARRAY[$1, $2])::text[])`, []any{"a", "0"}},
		{"concat", Select(doc.Concat(JSONBParam(`{"b":2}`)).Expr()), `SELECT ("payload" || ($1)::jsonb)`, []any{`{"b":2}`}},
		{"path_exists", Select(doc.PathExists("$.a").Expr()), `SELECT ("payload" @? ($1)::jsonpath)`, []any{"$.a"}},
		{"path_matches", Select(doc.PathMatches("$.a > 1").Expr()), `SELECT ("payload" @@ ($1)::jsonpath)`, []any{"$.a > 1"}},
		{"json_chain", Select(JSONCol("doc").Key("items").Index(-1).TextKey("name").As("name")), `SELECT ((("doc" -> $1) -> ($2)::integer) ->> $3) AS "name"`, []any{"items", int32(-1), "name"}},
		{"json_index", Select(JSONCol("doc").TextIndex(0).Expr()), `SELECT ("doc" ->> ($1)::integer)`, []any{int32(0)}},
		{"json_path", Select(JSONCol("doc").Path("a").Expr(), JSONCol("doc").PathText().Expr()), `SELECT ("doc" #> (ARRAY[$1])::text[]), ("doc" #>> (ARRAY[])::text[])`, []any{"a"}},
		{"encoded_json", Select(JSONParam(`{"a":1}`).As("doc")), `SELECT ($1)::json AS "doc"`, []any{`{"a":1}`}},
		{"injection_key", Select(doc.Key(`x'); DROP TABLE users; --`).Expr()), `SELECT ("payload" -> $1)`, []any{`x'); DROP TABLE users; --`}},
		{"null", Select(doc.Key("a").IsNull().Expr(), JSONCol("doc").IsNotNull().Expr()), `SELECT (("payload" -> $1) IS NULL), ("doc" IS NOT NULL)`, []any{"a"}},
		{"update", Update("events").Set(doc.Set(doc.Concat(JSONBParam(`{"active":true}`)))), `UPDATE "events" SET "payload" = ("payload" || ($1)::jsonb)`, []any{`{"active":true}`}},
	})
}

func TestJSONSelectorOwnership(t *testing.T) {
	t.Parallel()

	parts := []string{"items", "0"}
	doc := JSONBCol("doc")
	path := doc.Path(parts...)
	parts[0] = "changed"
	checkSQL(t, Select(doc.Expr(), path.Expr(), path.TextKey("name").Expr()), `SELECT "doc", ("doc" #> (ARRAY[$1, $2])::text[]), (("doc" #> (ARRAY[$3, $4])::text[]) ->> $5)`, "items", "0", "items", "0", "name")

	inner := Select(JSONBParam(`{"name":"Ada"}`).Expr())
	original := Select(JSONBExpr(Scalar(inner)).TextKey("name").Expr())
	clone := original.Clone()
	inner.Columns(Param(1))
	checkSQL(t, clone, `SELECT ((SELECT ($1)::jsonb) ->> $2)`, `{"name":"Ada"}`, "name")
	if _, _, err := original.ToSQL(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("live subquery with two columns: %v", err)
	}
}

func TestJSONValidationAndNumbering(t *testing.T) {
	t.Parallel()

	for _, expr := range []Expr{JSONDocument{}.Key("a").Expr(), JSONBDocument{}.Index(0).Expr(), Param(JSONBCol("doc"))} {
		if _, _, err := ExprToSQL(expr); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid JSON expression accepted: %v", err)
		}
	}
	if strconv.IntSize == 64 {
		for _, index := range []int64{-1<<31 - 1, 1 << 31} {
			for _, expr := range []Expr{JSONCol("doc").Index(int(index)).Expr(), JSONBCol("doc").DeleteIndex(int(index)).Expr()} {
				if _, _, err := ExprToSQL(expr); !errors.Is(err, ErrInvalid) {
					t.Fatalf("out-of-range index %d: %v", index, err)
				}
			}
		}
	}
	query := Select(JSONBCol("doc").Index(0).TextKey("name").Expr()).
		With(CTE("source", Select(JSONBParam(`[]`).Expr()).Where(Eq("id", 4))))
	buf, args, err := query.AppendSQL([]byte("prefix: "), []any{"existing"})
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != `prefix: WITH "source" AS (SELECT ($2)::jsonb WHERE ("id" = $3)) SELECT (("doc" -> ($4)::integer) ->> $5)` || !reflect.DeepEqual(args, []any{"existing", `[]`, 4, int32(0), "name"}) {
		t.Fatalf("SQL=%s args=%#v", buf, args)
	}
}

func typedJSONQuery() *SelectBuilder {
	doc := JSONBCol("payload")
	return Select(doc.PathText("items", "0", "name").As("name")).From("events").
		Where(doc.HasAllKeys("items", "active"), doc.Contains(JSONBParam(`{"active":true}`)), doc.Key("items").Index(-1).TextKey("name").Eq("Ada"))
}

func TestJSONReusableAppendAllocations(t *testing.T) {
	query := typedJSONQuery()
	checkSQL(t, query, `SELECT ("payload" #>> (ARRAY[$1, $2, $3])::text[]) AS "name" FROM "events" WHERE ("payload" ?& (ARRAY[$4, $5])::text[]) AND ("payload" @> ($6)::jsonb) AND (((("payload" -> $7) -> ($8)::integer) ->> $9) = $10)`, "items", "0", "name", "items", "active", `{"active":true}`, "items", int32(-1), "name", "Ada")
	buf, args := make([]byte, 0, 2048), make([]any, 0, 16)
	var err error
	allocations := testing.AllocsPerRun(100, func() { buf, args, err = query.AppendSQL(buf[:0], args[:0]) })
	if err != nil {
		t.Fatal(err)
	}
	if allocations != 0 {
		t.Fatalf("prebuilt JSON query with reusable storage: %g allocations", allocations)
	}
}

func BenchmarkJSONAppendSQLWarm(b *testing.B) {
	query := typedJSONQuery()
	for _, tc := range []struct {
		name  string
		style PlaceholderStyle
	}{{"Dollar", Dollar}, {"Question", Question}} {
		b.Run(tc.name, func(b *testing.B) {
			wantSQL := `SELECT ("payload" #>> (ARRAY[$1, $2, $3])::text[]) AS "name" FROM "events" WHERE ("payload" ?& (ARRAY[$4, $5])::text[]) AND ("payload" @> ($6)::jsonb) AND (((("payload" -> $7) -> ($8)::integer) ->> $9) = $10)`
			if tc.style == Question {
				wantSQL = benchmarkQuestionSQL(wantSQL, 10)
			}
			validateBenchmarkStatement(b, query, Options{PlaceholderStyle: tc.style}, wantSQL,
				"items", "0", "name", "items", "active", `{"active":true}`, "items", int32(-1), "name", "Ada")
			buf, args := make([]byte, 0, 2048), make([]any, 0, 16)
			options := Options{PlaceholderStyle: tc.style}
			b.ReportAllocs()
			for b.Loop() {
				var err error
				buf, args, err = AppendWith(buf[:0], args[:0], query, options)
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkArgs = args
		})
	}
}

func BenchmarkJSONConstruct(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchmarkQuery = typedJSONQuery()
	}
}

func BenchmarkJSONToSQL(b *testing.B) {
	query := typedJSONQuery()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkSQL, benchmarkArgs, err = query.ToSQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}
