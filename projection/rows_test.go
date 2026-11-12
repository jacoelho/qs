package projection_test

import (
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

var _ projection.Rows = (*sql.Rows)(nil)

// fakeRows serves one scan function per row, then reports err. unread is what a
// caller that stopped early left behind. Its Close has the pgx.Rows signature, and
// like pgx it may only learn of a failure while closing: closeErr surfaces through Err
// once closed.
type fakeRows struct {
	unread   []func(dest ...any) error
	current  func(dest ...any) error
	err      error
	closeErr error
	closed   int
}

func (r *fakeRows) Close() {
	r.closed++
	if r.err == nil {
		r.err = r.closeErr
	}
}

// errorClosingRows has the *sql.Rows signature of Close.
type errorClosingRows struct {
	*fakeRows
	closeFailed error
}

func (r errorClosingRows) Close() error {
	r.fakeRows.Close()
	return r.closeFailed
}

// uncloseableRows has only the methods of projection.Rows.
type uncloseableRows struct{ rows *fakeRows }

func (r uncloseableRows) Next() bool             { return r.rows.Next() }
func (r uncloseableRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r uncloseableRows) Err() error             { return r.rows.Err() }

func (r *fakeRows) Next() bool {
	if len(r.unread) == 0 {
		return false
	}
	r.current, r.unread = r.unread[0], r.unread[1:]
	return true
}

func (r *fakeRows) Scan(dest ...any) error { return r.current(dest...) }
func (r *fakeRows) Err() error             { return r.err }

func failingRow(err error) func(dest ...any) error {
	return func(...any) error { return err }
}

func flatUsers() projection.Projection[user] { return users(qs.Table("users").As("u"), "") }

func TestCollectReadsEveryRowInOrder(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		fakeRow(int64(2), "bob", "bob@example.com"),
		fakeRow(int64(3), "cal", nil),
	}}
	got, err := flatUsers().Collect(rows)
	if err != nil {
		t.Fatal(err)
	}
	email := "bob@example.com"
	want := []user{{ID: 1, Name: "ada"}, {ID: 2, Name: "bob", Email: &email}, {ID: 3, Name: "cal"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCollectWithoutRowsReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{}
	got, err := flatUsers().Collect(rows)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v, want an empty non-nil slice", got, err)
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times, want once", rows.closed)
	}
}

func TestCollectReturnsRowErrorAndStopsReading(t *testing.T) {
	t.Parallel()

	failed := errors.New("row failed")
	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		failingRow(failed),
		fakeRow(int64(3), "cal", nil),
	}}
	got, err := flatUsers().Collect(rows)
	if err != failed || got != nil { //nolint:errorlint // the row's error is returned unchanged.
		t.Fatalf("got %+v, %v, want nil and the row's error", got, err)
	}
	if len(rows.unread) != 1 {
		t.Fatalf("%d rows left unread, want 1: reading continued past the failure", len(rows.unread))
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times after a failed row, want once", rows.closed)
	}
}

func TestCollectReturnsRowsErr(t *testing.T) {
	t.Parallel()

	interrupted := errors.New("connection lost")
	rows := &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}, err: interrupted}
	got, err := flatUsers().Collect(rows)
	if err != interrupted || got != nil { //nolint:errorlint // rows.Err is returned unchanged.
		t.Fatalf("got %+v, %v, want nil and rows.Err", got, err)
	}
}

func TestAllYieldsRowsErrAfterTheRows(t *testing.T) {
	t.Parallel()

	interrupted := errors.New("connection lost")
	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		fakeRow(int64(2), "bob", nil),
	}, err: interrupted}
	var ids []int64
	var errs []error
	for u, err := range flatUsers().All(rows) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ids = append(ids, u.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2}) || len(errs) != 1 || errs[0] != interrupted { //nolint:errorlint // unchanged.
		t.Fatalf("ids %v, errors %v, want [1 2] then rows.Err once", ids, errs)
	}
}

// A loop that keeps ranging after an error must not receive later rows.
func TestAllEndsAfterRowError(t *testing.T) {
	t.Parallel()

	failed := errors.New("row failed")
	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		failingRow(failed),
		fakeRow(int64(3), "cal", nil),
	}, err: errors.New("rows.Err must not replace the row's error")}
	var ids []int64
	var errs []error
	for u, err := range flatUsers().All(rows) {
		if err != nil {
			if u != (user{}) {
				t.Errorf("error yielded with %+v, want the zero value", u)
			}
			errs = append(errs, err)
			continue
		}
		ids = append(ids, u.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1}) || len(errs) != 1 || errs[0] != failed { //nolint:errorlint // unchanged.
		t.Fatalf("ids %v, errors %v, want [1] then the row's error once", ids, errs)
	}
	if len(rows.unread) != 1 {
		t.Fatalf("%d rows left unread, want 1", len(rows.unread))
	}
}

func TestAllStopsReadingWhenLoopBreaks(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil),
		fakeRow(int64(2), "bob", nil),
		fakeRow(int64(3), "cal", nil),
	}}
	for u, err := range flatUsers().All(rows) {
		if err != nil || u.ID != 1 {
			t.Fatalf("got %+v, %v", u, err)
		}
		break
	}
	if len(rows.unread) != 2 {
		t.Fatalf("%d rows left unread, want 2: the iterator read ahead", len(rows.unread))
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times after break, want once", rows.closed)
	}
}

// pgx reports a failure found while closing through Err, so Err is read after Close.
func TestCollectReturnsErrorFoundWhileClosing(t *testing.T) {
	t.Parallel()

	late := errors.New("found while closing")
	rows := &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}, closeErr: late}
	got, err := flatUsers().Collect(rows)
	if err != late || got != nil { //nolint:errorlint // unchanged.
		t.Fatalf("got %+v, %v, want nil and the error Err reports after Close", got, err)
	}
}

func TestCollectReturnsCloseError(t *testing.T) {
	t.Parallel()

	closeFailed := errors.New("close failed")
	rows := errorClosingRows{fakeRows: &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}}, closeFailed: closeFailed}
	got, err := flatUsers().Collect(rows)
	if err != closeFailed || got != nil { //nolint:errorlint // unchanged.
		t.Fatalf("got %+v, %v, want nil and the error from Close", got, err)
	}
}

// The error nearest the cause wins: a row's error, then rows.Err, then Close.
func TestCollectPrefersEarlierErrorToCloseError(t *testing.T) {
	t.Parallel()

	failed := errors.New("row failed")
	interrupted := errors.New("connection lost")
	closeFailed := errors.New("close failed")
	for name, tc := range map[string]struct {
		rows *fakeRows
		want error
	}{
		"row error": {&fakeRows{unread: []func(dest ...any) error{failingRow(failed)}, err: interrupted}, failed},
		"rows.Err":  {&fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}, err: interrupted}, interrupted},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := flatUsers().Collect(
				errorClosingRows{fakeRows: tc.rows, closeFailed: closeFailed},
			)
			if err != tc.want || got != nil { //nolint:errorlint // unchanged.
				t.Fatalf("got %+v, %v, want nil and %v", got, err, tc.want)
			}
			if tc.rows.closed != 1 {
				t.Fatalf("rows closed %d times, want once", tc.rows.closed)
			}
		})
	}
}

func TestCollectClosesRowsWhenDestPanics(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{func(...any) error { panic("scan panicked") }}}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic did not propagate")
			}
		}()
		_, _ = flatUsers().Collect(rows)
	}()
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times after a panic, want once", rows.closed)
	}
}

func TestRowsWithoutCloseAreRefusedBeforeReading(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}}
	got, err := flatUsers().Collect(uncloseableRows{rows})
	if !errors.Is(err, projection.ErrNoClose) || got != nil {
		t.Fatalf("Collect: got %+v, %v, want nil and ErrNoClose", got, err)
	}
	yielded := 0
	for u, err := range flatUsers().All(uncloseableRows{rows}) {
		yielded++
		if !errors.Is(err, projection.ErrNoClose) || u != (user{}) {
			t.Fatalf("All: got %+v, %v, want the zero value and ErrNoClose", u, err)
		}
	}
	if yielded != 1 || len(rows.unread) != 1 {
		t.Fatalf("All yielded %d times with %d rows unread, want one error and the row unread", yielded, len(rows.unread))
	}
}

func TestAllClosesRowsWhenExhausted(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}}
	for _, err := range flatUsers().All(rows) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times, want once", rows.closed)
	}
}

func TestAllYieldsCloseErrorAfterTheRows(t *testing.T) {
	t.Parallel()

	closeFailed := errors.New("close failed")
	rows := errorClosingRows{fakeRows: &fakeRows{unread: []func(dest ...any) error{fakeRow(int64(1), "ada", nil)}}, closeFailed: closeFailed}
	var ids []int64
	var errs []error
	for u, err := range flatUsers().All(rows) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ids = append(ids, u.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1}) || len(errs) != 1 || errs[0] != closeFailed { //nolint:errorlint // unchanged.
		t.Fatalf("ids %v, errors %v, want [1] then the error from Close once", ids, errs)
	}
}

func TestAllClosesRowsAfterRowError(t *testing.T) {
	t.Parallel()

	rows := &fakeRows{unread: []func(dest ...any) error{failingRow(errors.New("row failed")), fakeRow(int64(2), "bob", nil)}}
	for range flatUsers().All(rows) {
	}
	if rows.closed != 1 {
		t.Fatalf("rows closed %d times after a failed row, want once", rows.closed)
	}
}

func TestScanIntoKeepsUndeclaredFields(t *testing.T) {
	t.Parallel()

	type document struct {
		ID     int64
		Title  string
		Tenant string
	}
	p := projection.New(
		projection.Column(qs.Col("id"), func(d *document) *int64 { return &d.ID }),
		projection.Column(qs.Col("title"), func(d *document) *string { return &d.Title }),
	)
	got := document{ID: 99, Title: "draft", Tenant: "acme"}
	if err := p.ScanInto(&got, fakeRow(int64(7), "final")); err != nil {
		t.Fatal(err)
	}
	if want := (document{ID: 7, Title: "final", Tenant: "acme"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A constructor's nested value must be read into, not replaced.
func TestScanIntoReadsIntoPreparedNestedValue(t *testing.T) {
	t.Parallel()

	type feed struct {
		ID     int64
		Origin string
	}
	type entry struct {
		ID   int64
		Feed *feed
	}
	p := projection.New(
		projection.Column(qs.Col("e", "id"), func(e *entry) *int64 { return &e.ID }),
		projection.Embed(
			projection.Column(qs.Col("f", "id"), func(f *feed) *int64 { return &f.ID }),
			func(e *entry) *feed {
				if e.Feed == nil {
					e.Feed = new(feed)
				}
				return e.Feed
			},
		),
	)
	prepared := &feed{Origin: "constructor"}
	got := entry{Feed: prepared}
	if err := p.ScanInto(&got, fakeRow(int64(1), int64(2))); err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 || got.Feed != prepared || *prepared != (feed{ID: 2, Origin: "constructor"}) {
		t.Fatalf("got %+v with feed %+v, want the prepared feed filled", got, *got.Feed)
	}
}

// Reuse must not carry a nullable column or an optional group over from the last row.
func TestScanIntoOverwritesPreviousRow(t *testing.T) {
	t.Parallel()

	p := employees()
	var got employee
	if err := p.ScanInto(&got, fakeRow(int64(1), "ada", "ada@example.com", int64(2), "bob", nil)); err != nil {
		t.Fatal(err)
	}
	if got.Employee.Email == nil || got.Manager == nil {
		t.Fatalf("first row: got %+v", got)
	}
	if err := p.ScanInto(&got, fakeRow(int64(3), "cal", nil, nil, nil, nil)); err != nil {
		t.Fatal(err)
	}
	if want := (employee{Employee: user{ID: 3, Name: "cal"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("second row: got %+v, want %+v", got, want)
	}
}

func TestScanIntoReturnsScanErrorUnchanged(t *testing.T) {
	t.Parallel()

	failed := errors.New("scan failed")
	var got user
	if err := flatUsers().ScanInto(&got, failingRow(failed)); err != failed { //nolint:errorlint // unchanged.
		t.Fatalf("err = %v, want the scan error", err)
	}
}

func TestScanIntoMapSetRunsOnlyIfConvertSucceeds(t *testing.T) {
	t.Parallel()

	type label struct{ Text, Note string }
	refused := errors.New("refused")
	p := projection.Map(
		projection.Column(qs.Col("name"), func(s *string) *string { return s }),
		func(name string) (string, error) {
			if name == "" {
				return "partial", refused
			}
			return name, nil
		},
		func(l *label, text string) { l.Text = text },
	)
	got := label{Text: "old", Note: "kept"}
	if err := p.ScanInto(&got, fakeRow("")); err != refused { //nolint:errorlint // The contract is the unchanged error value.
		t.Fatalf("err = %v, want the convert error unchanged", err)
	}
	if want := (label{Text: "old", Note: "kept"}); got != want {
		t.Fatalf("after failed convert: got %+v, want %+v", got, want)
	}
	if err := p.ScanInto(&got, fakeRow("ada")); err != nil {
		t.Fatal(err)
	}
	if want := (label{Text: "ada", Note: "kept"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A value that must not be copied cannot be returned by Scan.
func TestScanIntoReadsUncopyableValue(t *testing.T) {
	t.Parallel()

	type guarded struct {
		mu sync.Mutex
		ID int64
	}
	p := projection.Column(qs.Col("id"), func(g *guarded) *int64 { return &g.ID })
	var got guarded
	got.mu.Lock()
	defer got.mu.Unlock()
	if err := p.ScanInto(&got, fakeRow(int64(5))); err != nil || got.ID != 5 {
		t.Fatalf("got ID %d, %v, want 5", got.ID, err)
	}
}

func TestScanIntoNilDestinationPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	_ = flatUsers().ScanInto(nil, fakeRow(int64(1), "ada", nil))
}
