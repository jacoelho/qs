package projection_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

// show prints v with the values behind its pointers, which %+v does not.
func show(v any) string {
	if encoded, err := json.Marshal(v); err == nil {
		return string(encoded)
	}
	return fmt.Sprintf("%+v", v)
}

// checkRowsRead reads rows through Scan, Collect and All and compares each read with
// want, after the whole read finished, so a value shared between two results shows as a
// wrong row. Scan, Collect and All share one read path, so none is the reference for
// the others; want is written by hand.
func checkRowsRead[D any](t *testing.T, p projection.Projection[D], want []D, rows ...[]any) {
	t.Helper()

	compare := func(name string, got []D) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: read %d rows, want %d", name, len(got), len(want))
			return
		}
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("%s: row %d: got %s, want %s", name, i, show(got[i]), show(want[i]))
				return
			}
		}
	}
	result := func() *fakeRows {
		unread := make([]func(dest ...any) error, len(rows))
		for i, values := range rows {
			unread[i] = fakeRow(values...)
		}
		return &fakeRows{unread: unread}
	}

	var scanned []D
	for i, values := range rows {
		d, err := p.Scan(fakeRow(values...))
		if err != nil {
			t.Fatalf("row %d: Scan: %v", i, err)
		}
		scanned = append(scanned, d)
	}
	compare("Scan", scanned)

	collected, err := p.Collect(result())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	compare("Collect", collected)

	var ranged []D
	for d, err := range p.All(result()) {
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		ranged = append(ranged, d)
	}
	compare("All", ranged)
}

func TestRowsOfOneResultDoNotShareState(t *testing.T) {
	t.Parallel()

	t.Run("optional group", func(t *testing.T) {
		t.Parallel()
		checkRowsRead(t, employees(), []employee{
			{Employee: user{1, "ada", new("ada@example.com")}, Manager: &user{10, "bob", new("bob@example.com")}},
			{Employee: user{2, "cal", nil}},
			{Employee: user{3, "dan", nil}},
			{Employee: user{4, "eve", new("eve@example.com")}, Manager: &user{11, "fay", nil}},
			{Employee: user{5, "gus", nil}, Manager: &user{12, "hal", new("hal@example.com")}},
		},
			[]any{int64(1), "ada", "ada@example.com", int64(10), "bob", "bob@example.com"},
			[]any{int64(2), "cal", nil, nil, nil, nil},
			[]any{int64(3), "dan", nil, nil, nil, nil},
			[]any{int64(4), "eve", "eve@example.com", int64(11), "fay", nil},
			[]any{int64(5), "gus", nil, int64(12), "hal", "hal@example.com"},
		)
	})
	t.Run("nested groups", func(t *testing.T) {
		t.Parallel()
		type team struct {
			ID   int64
			Lead *user
		}
		type row struct{ Team *team }
		p := projection.EmbedNull(projection.New(
			projection.Column(qs.Col("t", "id"), func(t *team) *int64 { return &t.ID }),
			projection.EmbedNull(users(qs.Table("users").As("l"), "lead_"), func(t *team, u *user) { t.Lead = u }),
		), func(r *row, t *team) { r.Team = t })
		checkRowsRead(t, p, []row{
			{Team: &team{ID: 1}},
			{Team: &team{ID: 2, Lead: &user{20, "ada", new("ada@example.com")}}},
			{},
			{Team: &team{ID: 3, Lead: &user{30, "bob", nil}}},
			{Team: &team{ID: 4}},
		},
			[]any{int64(1), nil, nil, nil},
			[]any{int64(2), int64(20), "ada", "ada@example.com"},
			[]any{nil, nil, nil, nil},
			[]any{int64(3), int64(30), "bob", nil},
			[]any{int64(4), nil, nil, nil},
		)
	})
	t.Run("optional group under an Embed", func(t *testing.T) {
		t.Parallel()
		type listed struct {
			Row   employee
			Total int64
		}
		p := projection.New(
			projection.Embed(employees(), func(l *listed) *employee { return &l.Row }),
			projection.Column(qs.Col("total"), func(l *listed) *int64 { return &l.Total }),
		)
		checkRowsRead(t, p, []listed{
			{Row: employee{Employee: user{1, "ada", nil}, Manager: &user{10, "bob", nil}}, Total: 2},
			{Row: employee{Employee: user{2, "cal", nil}}, Total: 2},
		},
			[]any{int64(1), "ada", nil, int64(10), "bob", nil, int64(2)},
			[]any{int64(2), "cal", nil, nil, nil, nil, int64(2)},
		)
	})
	t.Run("zero and nullable columns", func(t *testing.T) {
		t.Parallel()
		checkRowsRead(t, profiles(), []profile{
			{ID: 1, Nickname: "ada", Logins: sql.NullInt64{Int64: 7, Valid: true}},
			{ID: 2},
			{ID: 3, Nickname: "cal"},
		},
			[]any{int64(1), "ada", int64(7)},
			[]any{int64(2), nil, nil},
			[]any{int64(3), "cal", nil},
		)
	})
	t.Run("Map over an allocating Embed", func(t *testing.T) {
		t.Parallel()
		type holder struct{ User *user }
		inner := projection.Embed(users(qs.Table("users").As("u"), ""), func(h *holder) *user {
			if h.User == nil {
				h.User = new(user)
			}
			return h.User
		})
		p := projection.Map(inner, func(h holder) (*user, error) { return h.User, nil }, func(d **user, u *user) { *d = u })
		checkRowsRead(t, p, []*user{
			{1, "ada", new("ada@example.com")},
			{2, "bob", nil},
			{3, "cal", new("cal@example.com")},
		},
			[]any{int64(1), "ada", "ada@example.com"},
			[]any{int64(2), "bob", nil},
			[]any{int64(3), "cal", "cal@example.com"},
		)
	})
	t.Run("Map whose result points back into its input", func(t *testing.T) {
		t.Parallel()
		type parent struct {
			ID    int64
			Child *child
		}
		inner := projection.New(
			projection.Column(qs.Col("id"), func(p *parent) *int64 { return &p.ID }),
			projection.EmbedNull(
				projection.Column(qs.Col("child_id"), func(c *child) *int64 { return &c.ID }),
				func(p *parent, c *child) {
					p.Child = c
					if c != nil {
						c.Parent = &p.ID
					}
				},
			),
		)
		p := projection.Map(inner, func(p parent) (*child, error) { return p.Child, nil }, func(d **child, c *child) { *d = c })
		checkRowsRead(t, p, []*child{
			{ID: 10, Parent: new(int64(1))},
			nil,
			{ID: 30, Parent: new(int64(3))},
			{ID: 40, Parent: new(int64(4))},
		},
			[]any{int64(1), int64(10)},
			[]any{int64(2), nil},
			[]any{int64(3), int64(30)},
			[]any{int64(4), int64(40)},
		)
	})
	t.Run("Scanner column in a group", func(t *testing.T) {
		t.Parallel()
		type group struct {
			A int64
			B sql.NullString
		}
		p := projection.EmbedNull(projection.New(
			projection.Column(qs.Col("a"), func(g *group) *intoField { return &intoField{target: &g.A} }),
			projection.Column(qs.Col("b"), func(g *group) *sql.NullString { return &g.B }),
		), func(d **group, g *group) { *d = g })
		checkRowsRead(t, p, []*group{
			{A: 7, B: sql.NullString{String: "x", Valid: true}},
			nil,
			{A: 9, B: sql.NullString{String: "y", Valid: true}},
		},
			[]any{int64(7), "x"},
			[]any{nil, nil},
			[]any{int64(9), "y"},
		)
	})
}

// A cell reused from a present row must not make a later NULL look read.
func TestErrNullAfterEarlierRows(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil, int64(10), "bob", nil),
		fakeRow(int64(2), "cal", nil, int64(11), nil, nil),
	}}
	got, err := employees().Collect(rows)
	if !errors.Is(err, projection.ErrNull) || !strings.Contains(err.Error(), "column 4") || got != nil {
		t.Fatalf("got %+v, %v, want nil and ErrNull at column 4", got, err)
	}
}

// child keeps a pointer into the row it was read beside.
type child struct {
	ID     int64
	Parent *int64
}
