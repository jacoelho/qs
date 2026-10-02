package qs

import "testing"

var benchmarkSQL string
var benchmarkArgs []any
var benchmarkQuery *SelectBuilder

func benchQuery() *SelectBuilder {
	return SelectCols("id", "name", "created_at").From("users").Where(Eq("active", true), In("id", 1, 2, 3)).OrderBy(Desc("created_at"), Desc("id")).Limit(50)
}
func BenchmarkToSQL(b *testing.B) {
	q := benchQuery()
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkSQL, benchmarkArgs, err = q.ToSQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkAppendSQLWarm(b *testing.B) {
	q := benchQuery()
	sql := make([]byte, 0, 1024)
	args := make([]any, 0, 16)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		sql, args, err = q.AppendSQL(sql[:0], args[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkArgs = args
}
func BenchmarkConstruct(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		benchmarkQuery = benchQuery()
	}
}
func BenchmarkConstructAndToSQL(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var err error
		benchmarkSQL, benchmarkArgs, err = benchQuery().ToSQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkClone(b *testing.B) {
	q := benchQuery()
	b.ReportAllocs()
	for b.Loop() {
		benchmarkQuery = q.Clone()
	}
}
func BenchmarkAppendCTEWarm(b *testing.B) {
	recent := CTE("recent", SelectCols("id").From("users").Where(Gt("created_at", 100)))
	q := Select(Col("r.id"), CountAll().Over(Window().OrderBy(Asc("r.id")))).With(recent).FromExpr(recent.Ref().As("r")).Where(In("r.id", 1, 2, 3)).Limit(20)
	sql := make([]byte, 0, 2048)
	args := make([]any, 0, 16)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		sql, args, err = q.AppendSQL(sql[:0], args[:0])
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkArgs = args
}
