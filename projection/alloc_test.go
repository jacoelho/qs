package projection_test

import (
	"testing"
)

// TestRowsReuseWorkingMemory guards the claim that All, Collect and CollectPtr reuse their working
// memory from one row to the next. It measures the allocations a read gains between few
// and many rows, so fixed costs cancel. Bounds are upper bounds.
//
// Neither this test nor its subtests are parallel: AllocsPerRun cannot run in a
// parallel test, and mixing the two trips tparallel.
func TestRowsReuseWorkingMemory(t *testing.T) {
	const (
		few  = 1000
		many = 3000
		// growth is what Collect's result slice adds between few and many rows under
		// Go's geometric append growth; far below one allocation per row.
		growth = 16
	)
	flat := flatUsers()
	group := employees()

	collectGroup := func(rows *benchRows) (n, present int, err error) {
		out, err := group.Collect(rows)
		for i := range out {
			if out[i].Manager != nil {
				present++
			}
		}
		return len(out), present, err
	}
	alternating := func() func(dest ...any) error {
		n := 0
		return func(dest ...any) error {
			n++
			if n%2 == 0 {
				return nil
			}
			return presentScan(dest...)
		}
	}
	steady := func() func(dest ...any) error { return discardScan }
	present := func() func(dest ...any) error { return presentScan }
	none := func(int) int { return 0 }
	all := func(n int) int { return n }

	cases := []struct {
		name        string
		scan        func() func(dest ...any) error
		read        func(rows *benchRows) (n, present int, err error)
		wantPresent func(n int) int
		extra       float64
	}{
		{
			name: "nullable columns",
			scan: steady,
			read: func(rows *benchRows) (int, int, error) {
				out, err := flat.Collect(rows)
				return len(out), 0, err
			},
			wantPresent: none,
		},
		{
			// One per row: the D each pointer holds. Working memory is still reused.
			name: "nullable columns through CollectPtr",
			scan: steady,
			read: func(rows *benchRows) (int, int, error) {
				out, err := flat.CollectPtr(rows)
				return len(out), 0, err
			},
			wantPresent: none,
			extra:       2000,
		},
		{name: "absent group", scan: steady, read: collectGroup, wantPresent: none},
		{name: "present group", scan: present, read: collectGroup, wantPresent: all, extra: 2000},
		{
			// Two per row: the row, which All reads into a value of its own so that a
			// set may keep a pointer into it, and the present group's row.
			name: "present group through All",
			scan: present,
			read: func(rows *benchRows) (n, present int, err error) {
				for d, err := range group.All(rows) {
					if err != nil {
						return n, present, err
					}
					n++
					if d.Manager != nil {
						present++
					}
				}
				return n, present, nil
			},
			wantPresent: all,
			extra:       4000,
		},
		{
			// A present row costs one allocation and an absent one none, so 1000 of the
			// 2000 extra rows allocate; this is the only case where a cell that replaced
			// a handed-over one is used again.
			name:        "alternating group",
			scan:        alternating,
			read:        collectGroup,
			wantPresent: func(n int) int { return n / 2 },
			extra:       1000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			measure := func(n int) float64 {
				return testing.AllocsPerRun(10, func() {
					got, present, err := c.read(&benchRows{count: n, scan: c.scan()})
					if err != nil || got != n || present != c.wantPresent(n) {
						t.Fatalf("read %d rows with %d present groups, %v; want %d rows, %d present", got, present, err, n, c.wantPresent(n))
					}
				})
			}
			a, b := measure(few), measure(many)
			if bound := c.extra + growth; b-a > bound {
				t.Errorf("%d rows allocate %.0f, %d rows %.0f: %.0f more, want at most %.0f", few, a, many, b, b-a, bound)
			}
		})
	}
}
