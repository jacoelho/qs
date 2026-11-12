package projection_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/jacoelho/qs/projection"
)

// A present group's row is the *N its set receives, whichever loop read it; an
// absent group between present ones must not hand a later row the earlier one's N.
func TestCollectPtrGroupRowBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	p := projection.EmbedNull(nodes(), func(d **node, n *node) { *d = n })
	wantIDs := []int64{1, 0, 0, 4, 5}
	got, err := p.CollectPtr(&fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), nil), fakeRow(nil, nil), fakeRow(nil, nil), fakeRow(int64(4), nil), fakeRow(int64(5), nil),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(wantIDs) {
		t.Fatalf("read %d rows, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		n := *got[i]
		if id == 0 {
			if n != nil {
				t.Errorf("row %d: got %s, want an absent group", i, show(n))
			}
			continue
		}
		if n == nil || n.ID != id || n.Self != &n.ID {
			t.Errorf("row %d: got %s, want ID %d and Self pointing into the group row", i, show(n), id)
		}
	}
}

func TestCollectPtrReadsEveryRowIntoItsOwnValue(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		fakeRow(int64(2), "bob", "bob@example.com"),
		fakeRow(int64(3), "cal", nil),
	}}
	got, err := flatUsers().CollectPtr(rows)
	if err != nil {
		t.Fatal(err)
	}
	email := "bob@example.com"
	want := []*user{{ID: 1, Name: "ada"}, {ID: 2, Name: "bob", Email: &email}, {ID: 3, Name: "cal"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want %s", show(got), show(want))
	}
	// DeepEqual follows pointers, so it would accept one value returned three times
	// only if the rows were equal; a write shows the elements are separate.
	got[0].Name = "changed"
	if got[1].Name != "bob" || got[2].Name != "cal" {
		t.Fatalf("writing through row 0 changed another row: %s", show(got))
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times, want once", rows.closed)
	}
}

func TestCollectPtrWithoutRowsReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{}
	got, err := flatUsers().CollectPtr(rows)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v, want an empty non-nil slice", got, err)
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times, want once", rows.closed)
	}
}

// Collect grows its slice, which moves the rows already read; CollectPtr reads each
// row where it stays, so a pointer a set kept into the row is a pointer into the
// element returned. Nine rows pass several growth steps of the pointer slice.
func TestCollectPtrKeptRowPointerPointsIntoTheReturnedRow(t *testing.T) {
	t.Parallel()

	ids := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	unread := make([]func(dest ...any) error, len(ids))
	for i, id := range ids {
		unread[i] = fakeRow(id, nil)
	}
	got, err := nodes().CollectPtr(&fakeRows{unread: unread})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ids) {
		t.Fatalf("read %d rows, want %d", len(got), len(ids))
	}
	for i, id := range ids {
		if got[i].ID != id || got[i].Self != &got[i].ID {
			t.Errorf("row %d: got %s, want ID %d and Self pointing at this row's ID", i, show(got[i]), id)
		}
	}
}

// The error nearest the cause wins and comes back unchanged with a nil slice: a row's
// error, then rows.Err (where pgx also reports a failure found while closing), then
// the error from Close.
func TestCollectPtrReturnsFirstErrorWithNilSlice(t *testing.T) {
	t.Parallel()

	failed := errors.New("row failed")
	interrupted := errors.New("connection lost")
	late := errors.New("found while closing")
	closeFailed := errors.New("close failed")
	one := func() []func(dest ...any) error { return []func(dest ...any) error{fakeRow(int64(1), "ada", nil)} }
	for name, tc := range map[string]struct {
		rows       *fakeRows
		closeErr   error
		want       error
		wantUnread int
	}{
		"row error stops reading": {
			rows: &fakeRows{unread: []func(dest ...any) error{
				fakeRow(int64(1), "ada", nil), failingRow(failed), fakeRow(int64(3), "cal", nil),
			}},
			want: failed, wantUnread: 1,
		},
		"row error before rows.Err and Close": {
			rows: &fakeRows{unread: []func(dest ...any) error{failingRow(failed)}, err: interrupted}, closeErr: closeFailed, want: failed,
		},
		"rows.Err":                   {rows: &fakeRows{unread: one(), err: interrupted}, want: interrupted},
		"rows.Err before Close":      {rows: &fakeRows{unread: one(), err: interrupted}, closeErr: closeFailed, want: interrupted},
		"error found while closing":  {rows: &fakeRows{unread: one(), closeErr: late}, want: late},
		"error returned from Close":  {rows: &fakeRows{unread: one()}, closeErr: closeFailed, want: closeFailed},
		"no rows, error from Close":  {rows: &fakeRows{}, closeErr: closeFailed, want: closeFailed},
		"no rows, error in rows.Err": {rows: &fakeRows{err: interrupted}, want: interrupted},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var rows projection.Rows = tc.rows
			if tc.closeErr != nil {
				rows = errorClosingRows{fakeRows: tc.rows, closeFailed: tc.closeErr}
			}
			got, err := flatUsers().CollectPtr(rows)
			if err != tc.want || got != nil { //nolint:errorlint // returned unchanged.
				t.Fatalf("got %s, %v, want nil and %v", show(got), err, tc.want)
			}
			if len(tc.rows.unread) != tc.wantUnread {
				t.Fatalf("%d rows left unread, want %d", len(tc.rows.unread), tc.wantUnread)
			}
			if tc.rows.closed != 1 {
				t.Fatalf("rows closed %d times, want once", tc.rows.closed)
			}
		})
	}
}

func TestCollectPtrClosesRowsWhenScanPanics(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{func(...any) error { panic("scan panicked") }}}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic did not propagate")
			}
		}()
		_, _ = flatUsers().CollectPtr(rows)
	}()
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times after a panic, want once", rows.closed)
	}
}

func TestCollectPtrRefusesRowsWithoutCloseBeforeReading(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}}
	got, err := flatUsers().CollectPtr(uncloseableRows{rows})
	if !errors.Is(err, projection.ErrNoClose) || got != nil {
		t.Fatalf("got %s, %v, want nil and ErrNoClose", show(got), err)
	}
	if len(rows.unread) != 1 {
		t.Fatalf("%d rows left unread, want the one row untouched", len(rows.unread))
	}
}
