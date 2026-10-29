package qs_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

var (
	architectureStatement  qs.Statement
	architectureProjection projection.Projection[string]
	architectureMetadata   []string
)

func BenchmarkArchitectureProjection(b *testing.B) {
	left := designColumns()
	right := projection.New(
		projection.Named(qs.Col("name"), "name"), projection.Named(qs.Col("email"), "email"),
		projection.Named(qs.Col("active"), "active"), projection.Named(qs.Col("payload"), "payload"),
		projection.Named(qs.Col("queue"), "queue"), projection.Named(qs.Col("locked_at"), "locked_at"),
		projection.Named(qs.Col("updated_at"), "updated_at"), projection.Named(qs.Col("version"), "version"),
	)
	const wantSQL = `SELECT "id" AS "id", "tenant_id" AS "tenant_id", "kind" AS "kind", "state" AS "state", "attempt" AS "attempt", "created_at" AS "created_at", "scheduled_at" AS "scheduled_at", "priority" AS "priority", "name" AS "name", "email" AS "email", "active" AS "active", "payload" AS "payload", "queue" AS "queue", "locked_at" AS "locked_at", "updated_at" AS "updated_at", "version" AS "version"`
	wantMetadata := []string{"id", "tenant_id", "kind", "state", "attempt", "created_at", "scheduled_at", "priority", "name", "email", "active", "payload", "queue", "locked_at", "updated_at", "version"}
	p := projection.Concat(left, right)
	designCheck(b, qs.Select(p.Expressions()...), wantSQL, nil)
	if !reflect.DeepEqual(p.Metadata(), wantMetadata) {
		b.Fatal("composed projection metadata changed")
	}
	b.Run("Concat", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			architectureProjection = projection.Concat(left, right)
		}
	})
	b.Run("ConcatAndSelect", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p := projection.Concat(left, right)
			architectureStatement = qs.Select(p.Expressions()...)
			architectureMetadata = p.Metadata()
		}
	})
}
