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

func BenchmarkRowMembership(b *testing.B) {
	row := Row(Col("a"), Col("b"))
	rows := []RowExpr{Row(Param(1), Param(2)), Row(Param(3), Param(4))}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCondition = row.In(rows...)
	}
}

func BenchmarkConditionExtension(b *testing.B) {
	base := Col("a").Eq(Param(1))
	added := []Condition{Col("b").Eq(Param(2)), Col("c").Eq(Param(3))}
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCondition = base.And(added...)
	}
}

func BenchmarkRelationColumn(b *testing.B) {
	base := Table("schema", "table")
	b.ReportAllocs()
	for b.Loop() {
		benchmarkExpr = base.Col("value")
	}
}
