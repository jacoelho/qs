package projection_test

import (
	"slices"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

func benchmarkColumns(names ...string) projection.Projection[string] {
	outputs := make([]projection.Column[string], len(names))
	for i, name := range names {
		outputs[i] = projection.Named(qs.Col(name), name)
	}
	return projection.New(outputs...)
}

// BenchmarkProjection compares owned extraction with appending into reused
// storage, and measures composing two projections into one SELECT list.
// b.Loop keeps the results of calls in the loop body alive.
func BenchmarkProjection(b *testing.B) {
	left := benchmarkColumns("id", "tenant_id", "kind", "state", "attempt", "created_at", "scheduled_at", "priority")
	right := benchmarkColumns("name", "email", "active", "payload", "queue", "locked_at", "updated_at", "version")
	composed := projection.Concat(left, right)
	checkSQL(b, qs.Select(composed.Expressions()...), `SELECT "id" AS "id", "tenant_id" AS "tenant_id", "kind" AS "kind", "state" AS "state", "attempt" AS "attempt", "created_at" AS "created_at", "scheduled_at" AS "scheduled_at", "priority" AS "priority", "name" AS "name", "email" AS "email", "active" AS "active", "payload" AS "payload", "queue" AS "queue", "locked_at" AS "locked_at", "updated_at" AS "updated_at", "version" AS "version"`)
	if want := []string{"id", "tenant_id", "kind", "state", "attempt", "created_at", "scheduled_at", "priority", "name", "email", "active", "payload", "queue", "locked_at", "updated_at", "version"}; !slices.Equal(composed.Metadata(), want) {
		b.Fatalf("metadata = %v, want %v", composed.Metadata(), want)
	}

	b.Run("Owned", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			left.Expressions()
			left.Metadata()
		}
	})
	b.Run("AppendNil", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			left.AppendExpressions(nil)
			left.AppendMetadata(nil)
		}
	})
	b.Run("AppendWarm", func(b *testing.B) {
		expressions := make([]qs.Expr, 0, 8)
		metadata := make([]string, 0, 8)
		b.ReportAllocs()
		for b.Loop() {
			expressions = left.AppendExpressions(expressions[:0])
			metadata = left.AppendMetadata(metadata[:0])
		}
	})
	b.Run("Concat", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			projection.Concat(left, right)
		}
	})
	b.Run("ConcatAndSelect", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p := projection.Concat(left, right)
			qs.Select(p.Expressions()...)
			p.Metadata()
		}
	})
}
