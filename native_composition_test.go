package qs

import (
	"reflect"
	"testing"
)

func nativeCompositionQuery() *SelectBuilder {
	child := Select(Param("seed").Cast(Text))
	items := XMLTable(Param("/root/item"), Param("<root/>").Cast(XML),
		XMLColumn("value", Text).Path(Param("value")).Default(Scalar(child)),
	).Namespaces(XMLNamespace(Param("urn:qs").Cast(Text), "n")).As("x")
	return Select(
		JSONArrayAggregate(Ident("x", "value")).OrderBy(Param(7).Asc()).
			Filter(Ident("x", "value").Ne("excluded")).
			Over(Window().PartitionBy(Param("p"))).Expr(),
		Ident("x", "record").Field("key.name"),
		SubstringFrom(Param("abcd"), Param(2), Param(1)),
		Scalar(child),
	).FromExpr(items).Where(Ident("x", "value").Eq("keep"))
}

func TestAppendWithNativeComposition(t *testing.T) {
	query := nativeCompositionQuery()
	wantArgs := []any{7, "excluded", "p", "abcd", 2, 1, "seed", "urn:qs", "/root/item", "<root/>", "value", "seed", "keep"}
	cases := []struct {
		name  string
		style PlaceholderStyle
		sql   string
	}{
		{"dollar", Dollar, `SELECT JSON_ARRAYAGG("x"."value" ORDER BY $1 ASC) FILTER (WHERE ("x"."value" <> $2)) OVER (PARTITION BY $3), ("x"."record")."key.name", SUBSTRING($4 FROM $5 FOR $6), (SELECT ($7)::text) FROM XMLTABLE(XMLNAMESPACES(($8)::text AS "n"), ($9) PASSING (($10)::xml) COLUMNS "value" text PATH $11 DEFAULT (SELECT ($12)::text)) AS "x" WHERE ("x"."value" = $13)`},
		{"question", Question, `SELECT JSON_ARRAYAGG("x"."value" ORDER BY ? ASC) FILTER (WHERE ("x"."value" <> ?)) OVER (PARTITION BY ?), ("x"."record")."key.name", SUBSTRING(? FROM ? FOR ?), (SELECT (?)::text) FROM XMLTABLE(XMLNAMESPACES((?)::text AS "n"), (?) PASSING ((?)::xml) COLUMNS "value" text PATH ? DEFAULT (SELECT (?)::text)) AS "x" WHERE ("x"."value" = ?)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := Options{PlaceholderStyle: tc.style}
			buf, args, err := AppendWith(make([]byte, 0, 2048), make([]any, 0, 32), query, options)
			if err != nil || string(buf) != tc.sql || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("got %s %#v %v; want %s %#v", buf, args, err, tc.sql, wantArgs)
			}
			allocations := testing.AllocsPerRun(100, func() {
				buf, args, err = AppendWith(buf[:0], args[:0], query, options)
			})
			if err != nil || allocations != 0 {
				t.Fatalf("warm rendering allocated %g times: %v", allocations, err)
			}
			if string(buf) != tc.sql || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("reuse changed SQL or arguments: %s %#v", buf, args)
			}
		})
	}
}

func BenchmarkAppendWithNativeComposition(b *testing.B) {
	query := nativeCompositionQuery()
	for _, tc := range []struct {
		name  string
		style PlaceholderStyle
	}{{"dollar", Dollar}, {"question", Question}} {
		b.Run(tc.name, func(b *testing.B) {
			buf, args := make([]byte, 0, 2048), make([]any, 0, 32)
			options := Options{PlaceholderStyle: tc.style}
			b.ReportAllocs()
			for b.Loop() {
				var err error
				buf, args, err = AppendWith(buf[:0], args[:0], query, options)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkConstructNativeComposition(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchmarkQuery = nativeCompositionQuery()
	}
}

func BenchmarkToSQLNativeComposition(b *testing.B) {
	query := nativeCompositionQuery()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkSQL, benchmarkArgs, err = query.ToSQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}
