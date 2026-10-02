package qs

import "testing"

var (
	benchmarkWindow    WindowSpec
	benchmarkCondition Condition
	benchmarkExpr      Expr
)

func BenchmarkImmutableWindowExtension(b *testing.B) {
	base := Window().PartitionBy(Param(1))
	added := []Expr{Param(2), Param(3)}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkWindow = base.PartitionBy(added...)
	}
}

func BenchmarkRowValues(b *testing.B) {
	row := Row(Col("a"), Col("b"))
	values := []any{1, "value"}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCondition = row.EqValues(values...)
	}
}

func BenchmarkRowMembership(b *testing.B) {
	row := Row(Col("a"), Col("b"))
	rows := []RowExpr{Row(Param(1), Param(2)), Row(Param(3), Param(4))}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCondition = row.In(rows...)
	}
}

func BenchmarkConditionExtension(b *testing.B) {
	base := Eq("a", 1)
	added := []Condition{Eq("b", 2), Eq("c", 3)}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCondition = base.And(added...)
	}
}

func BenchmarkXMLForestExpr(b *testing.B) {
	values := []Expr{Param(1).As("a"), Param(2).As("b")}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExpr = XMLForestExpr(values...)
	}
}

func BenchmarkImmutableXMLCapture(b *testing.B) {
	base := XMLElement("root", Param("content")).Attributes(XMLAttr("id", Param(1)))
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExpr = base.Expr()
	}
}

func BenchmarkJSONArrayExtension(b *testing.B) {
	base := JSONArray(Param(1))
	values := []JSONInputValue{Param(2), JSONInputExpr(Param(3)).FormatJSON()}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExpr = base.Values(values...).Expr()
	}
}

func BenchmarkRelationColumn(b *testing.B) {
	base := TableIdent("schema", "table")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExpr = base.Col("value")
	}
}
