package projection_test

import (
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

func discardScan(...any) error { return nil }

func fillScan(dest ...any) error {
	id, name := int64(1), "ada"
	for _, d := range dest {
		switch target := d.(type) {
		case **int64:
			*target = &id
		case **string:
			*target = &name
		}
	}
	return nil
}

// BenchmarkScan measures per-row cost with a stub scan, excluding the driver.
func BenchmarkScan(b *testing.B) {
	type flat struct {
		ID         int64
		Name       string
		Email      *string
		Department *string
	}
	required := projection.New(
		projection.Column(qs.Col("id"), func(f *flat) *int64 { return &f.ID }),
		projection.Column(qs.Col("name"), func(f *flat) *string { return &f.Name }),
		projection.Column(qs.Col("email"), func(f *flat) **string { return &f.Email }),
	)
	nullable := projection.New(
		projection.Column(qs.Col("id"), func(f *flat) *int64 { return &f.ID }),
		projection.Column(qs.Col("name"), func(f *flat) *string { return &f.Name }),
		projection.ColumnNull(qs.Col("email"), func(f *flat, v *string) { f.Email = v }),
	)
	group := employees()

	b.Run("Column", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = required.Scan(discardScan)
		}
	})
	b.Run("ColumnNull", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = nullable.Scan(discardScan)
		}
	})
	b.Run("GroupPresent", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = group.Scan(fillScan)
		}
	})
	b.Run("GroupAbsent", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = group.Scan(discardScan)
		}
	})
}

func BenchmarkCompose(b *testing.B) {
	left := users(qs.Table("users").As("e"), "")
	right := users(qs.Table("users").As("m"), "manager_")
	b.ReportAllocs()
	for b.Loop() {
		p := projection.New(left, right)
		qs.Select(p.Expressions()...)
	}
}
