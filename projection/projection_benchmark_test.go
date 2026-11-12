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
	// A separate row keeps the other cases' sizes comparable across changes.
	type titled struct {
		ID    int64
		Name  string
		Title string
	}
	zeroed := projection.New(
		projection.Column(qs.Col("id"), func(r *titled) *int64 { return &r.ID }),
		projection.Column(qs.Col("name"), func(r *titled) *string { return &r.Name }),
		projection.ColumnOrZero(qs.Col("title"), func(r *titled) *string { return &r.Title }),
	)
	mapped := projection.Map(
		required,
		func(f flat) (string, error) { return f.Name, nil },
		func(d *string, v string) { *d = v },
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
	b.Run("ColumnOrZero", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = zeroed.Scan(discardScan)
		}
	})
	b.Run("Map", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = mapped.Scan(discardScan)
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
		benchSelect = qs.Select(p.Expressions()...)
	}
}

var (
	benchID     = int64(1)
	benchName   = "ada"
	benchSelect qs.SelectBuilder
)

// benchRows serves count rows through scan without allocating.
type benchRows struct {
	count int
	scan  func(dest ...any) error
}

func (r *benchRows) Next() bool {
	if r.count == 0 {
		return false
	}
	r.count--
	return true
}

func (r *benchRows) Scan(dest ...any) error { return r.scan(dest...) }
func (r *benchRows) Err() error             { return nil }
func (r *benchRows) Close()                 {}

func presentScan(dest ...any) error {
	for _, d := range dest {
		switch target := d.(type) {
		case **int64:
			*target = &benchID
		case **string:
			*target = &benchName
		}
	}
	return nil
}

// BenchmarkCollect measures a 1000-row result with stub rows, excluding the driver.
// Collect's own slice growth is included in every case.
func BenchmarkCollect(b *testing.B) {
	const count = 1000
	run := func(name string, collect func(rows *benchRows) (int, error), scan func(dest ...any) error) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				n, err := collect(&benchRows{count: count, scan: scan})
				if err != nil || n != count {
					b.Fatalf("read %d rows, %v", n, err)
				}
			}
		})
	}
	flat := users(qs.Table("users").As("u"), "")
	collectFlat := func(rows *benchRows) (int, error) {
		out, err := flat.Collect(rows)
		return len(out), err
	}
	group := employees()
	collectGroup := func(rows *benchRows) (int, error) {
		out, err := group.Collect(rows)
		return len(out), err
	}
	run("Nullable", collectFlat, discardScan)
	run("GroupPresent", collectGroup, presentScan)
	run("GroupAbsent", collectGroup, discardScan)
	run("All", func(rows *benchRows) (int, error) {
		n := 0
		for _, err := range group.All(rows) {
			if err != nil {
				return n, err
			}
			n++
		}
		return n, nil
	}, presentScan)
}
