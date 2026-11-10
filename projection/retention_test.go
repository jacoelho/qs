package projection_test

import (
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

type node struct {
	ID   int64
	Self *int64
}

func nodes() projection.Projection[node] {
	return projection.New(
		projection.Column(qs.Col("id"), func(n *node) *int64 { return &n.ID }),
		projection.ColumnNull(qs.Col("note"), func(n *node, _ *string) { n.Self = &n.ID }),
	)
}

// readBoth reads rows through Collect and All, asserting only on what the whole read
// returned: a value shared between rows looks right while its row is current.
func readBoth[D any](t *testing.T, p projection.Projection[D], rows ...[]any) (collected, ranged []D) {
	t.Helper()
	result := func() *fakeRows {
		unread := make([]func(dest ...any) error, len(rows))
		for i, values := range rows {
			unread[i] = fakeRow(values...)
		}
		return &fakeRows{unread: unread}
	}
	collected, err := p.Collect(result())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for d, err := range p.All(result()) {
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		ranged = append(ranged, d)
	}
	return collected, ranged
}

func TestKeptRowPointerIsNotWrittenByLaterRows(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	rows := make([][]any, len(ids))
	for i, id := range ids {
		rows[i] = []any{id, nil}
	}
	collected, ranged := readBoth(t, nodes(), rows...)
	for name, got := range map[string][]node{"Collect": collected, "All": ranged} {
		if len(got) != len(ids) {
			t.Fatalf("%s: read %d rows, want %d", name, len(got), len(ids))
		}
		for i, want := range ids {
			if got[i].ID != want || got[i].Self == nil || *got[i].Self != want {
				t.Errorf("%s: row %d: got %s, want ID and *Self %d", name, i, show(got[i]), want)
			}
		}
	}

	got, err := nodes().Scan(fakeRow(int64(7), nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 7 || got.Self == nil || *got.Self != 7 {
		t.Errorf("Scan: got %s, want ID and *Self 7", show(got))
	}
}

func TestGroupRowBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	p := projection.EmbedNull(nodes(), func(d **node, n *node) { *d = n })
	collected, ranged := readBoth(t, p,
		[]any{int64(1), nil}, []any{nil, nil}, []any{nil, nil}, []any{int64(4), nil}, []any{int64(5), nil})
	wantIDs := []int64{1, 0, 0, 4, 5}
	for name, got := range map[string][]*node{"Collect": collected, "All": ranged} {
		if len(got) != len(wantIDs) {
			t.Fatalf("%s: read %d rows, want %d", name, len(got), len(wantIDs))
		}
		for i, id := range wantIDs {
			if id == 0 {
				if got[i] != nil {
					t.Errorf("%s: row %d: got %s, want nil", name, i, show(got[i]))
				}
				continue
			}
			if got[i] == nil || got[i].ID != id || got[i].Self != &got[i].ID {
				t.Errorf("%s: row %d: got %s, want ID %d and Self pointing into the row", name, i, show(got[i]), id)
			}
		}
	}
}

func TestScanIntoKeptRowPointerIsTheCallersValue(t *testing.T) {
	t.Parallel()

	var n node
	if err := nodes().ScanInto(&n, fakeRow(int64(7), nil)); err != nil {
		t.Fatal(err)
	}
	if n.Self != &n.ID || *n.Self != 7 {
		t.Errorf("got %s, want Self pointing at the caller's ID holding 7", show(n))
	}
}

func TestAbsentGroupRowReuseSharesNothing(t *testing.T) {
	t.Parallel()

	type name struct{ Value string }
	type person struct{ Name *name }
	p := projection.EmbedNull(
		projection.Embed(
			projection.Column(qs.Col("name"), func(n *name) *string { return &n.Value }),
			func(p *person) *name {
				if p.Name == nil {
					p.Name = new(name)
				}
				return p.Name
			}),
		func(d **person, p *person) { *d = p })
	checkRowsRead(t, p,
		[]*person{nil, {Name: &name{"ada"}}, nil, {Name: &name{"bob"}}, {Name: &name{"cal"}}},
		[]any{nil}, []any{"ada"}, []any{nil}, []any{"bob"}, []any{"cal"})
}
