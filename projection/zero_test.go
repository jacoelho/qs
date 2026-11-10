package projection_test

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

type profile struct {
	ID       int64
	Nickname string
	Logins   sql.NullInt64
}

func profiles() projection.Projection[profile] {
	return projection.New(
		projection.Column(qs.Col("id"), func(p *profile) *int64 { return &p.ID }),
		projection.ColumnOrZero(qs.Col("nickname"), func(p *profile) *string { return &p.Nickname }),
		projection.ColumnOrZero(qs.Col("logins"), func(p *profile) *sql.NullInt64 { return &p.Logins }),
	)
}

func TestColumnOrZeroReadsValueOrZero(t *testing.T) {
	t.Parallel()

	got, err := profiles().Scan(fakeRow(int64(1), "ada", int64(7)))
	want := profile{ID: 1, Nickname: "ada", Logins: sql.NullInt64{Int64: 7, Valid: true}}
	if err != nil || got != want {
		t.Fatalf("values: got %+v, %v, want %+v", got, err, want)
	}
	got, err = profiles().Scan(fakeRow(int64(2), nil, nil))
	if want := (profile{ID: 2}); err != nil || got != want {
		t.Fatalf("NULLs: got %+v, %v, want %+v", got, err, want)
	}
}

func TestColumnOrZeroInGroup(t *testing.T) {
	t.Parallel()

	p := projection.EmbedNull(profiles(), func(d **profile, v *profile) { *d = v })

	got, err := p.Scan(fakeRow(int64(1), nil, nil))
	if want := (profile{ID: 1}); err != nil || got == nil || *got != want {
		t.Fatalf("present with NULL columns: got %+v, %v, want %+v", got, err, want)
	}
	got, err = p.Scan(fakeRow(nil, nil, nil))
	if err != nil || got != nil {
		t.Fatalf("all NULL: got %+v, %v, want an absent group", got, err)
	}
	// A value in a ColumnOrZero proves presence, so the NULL required column is an error.
	_, err = p.Scan(fakeRow(nil, "orphan", nil))
	if !errors.Is(err, projection.ErrNull) || !strings.Contains(err.Error(), "column 0") {
		t.Fatalf("err = %v, want ErrNull at column 0", err)
	}
}

// A reused destination must not keep the previous row's value on NULL.
func TestColumnOrZeroOverwritesPreviousRow(t *testing.T) {
	t.Parallel()

	type account struct{ Profile profile }
	p := projection.Embed(profiles(), func(a *account) *profile { return &a.Profile })
	var got account
	if err := p.ScanInto(&got, fakeRow(int64(1), "ada", int64(7))); err != nil {
		t.Fatal(err)
	}
	if got.Profile.Nickname != "ada" || !got.Profile.Logins.Valid {
		t.Fatalf("first row: got %+v", got.Profile)
	}
	if err := p.ScanInto(&got, fakeRow(int64(2), nil, nil)); err != nil {
		t.Fatal(err)
	}
	if want := (profile{ID: 2}); got.Profile != want {
		t.Fatalf("second row: got %+v, want %+v", got.Profile, want)
	}
}

// An Embed dest that allocates its pointer is not an initialised target.
func TestColumnOrZeroBehindAllocatingEmbed(t *testing.T) {
	t.Parallel()

	type account struct{ Profile *profile }
	p := projection.Embed(profiles(), func(a *account) *profile {
		if a.Profile == nil {
			a.Profile = new(profile)
		}
		return a.Profile
	})
	got, err := p.Scan(fakeRow(int64(1), nil, int64(7)))
	want := profile{ID: 1, Logins: sql.NullInt64{Int64: 7, Valid: true}}
	if err != nil || got.Profile == nil || *got.Profile != want {
		t.Fatalf("got %+v, %v, want %+v", got.Profile, err, want)
	}
}

// A set declared before a ColumnOrZero reads it, as it reads a Column.
func TestSetSeesColumnOrZero(t *testing.T) {
	t.Parallel()

	type badge struct {
		Label string
		Name  string
	}
	p := projection.New(
		projection.ColumnNull(qs.Col("rank"), func(b *badge, v *int64) {
			if v != nil {
				b.Label = fmt.Sprintf("%s#%d", b.Name, *v)
			}
		}),
		projection.ColumnOrZero(qs.Col("name"), func(b *badge) *string { return &b.Name }),
	)
	row := fakeRow(int64(3), "ada")
	want := badge{Label: "ada#3", Name: "ada"}

	flat, err := p.Scan(row)
	if err != nil || flat != want {
		t.Fatalf("flat: got %+v, %v, want %+v", flat, err, want)
	}
	grouped, err := projection.EmbedNull(p, func(d **badge, b *badge) { *d = b }).Scan(row)
	if err != nil || grouped == nil || *grouped != want {
		t.Fatalf("group: got %+v, %v, want %+v", grouped, err, want)
	}
}

// A convert reads the ColumnOrZero columns of its input.
func TestMapSeesColumnOrZero(t *testing.T) {
	t.Parallel()

	p := projection.Map(profiles(), func(p profile) (string, error) {
		return fmt.Sprintf("%d:%s", p.ID, p.Nickname), nil
	}, func(d *string, v string) { *d = v })
	got, err := p.Scan(fakeRow(int64(1), "ada", nil))
	if err != nil || got != "1:ada" {
		t.Fatalf("got %q, %v, want %q", got, err, "1:ada")
	}
}

// maskedWrapper prints the same whether or not it holds a target.
type maskedWrapper struct{ target *int64 }

func (maskedWrapper) GoString() string { return "maskedWrapper{}" }

func TestColumnOrZeroDeclarationPanics(t *testing.T) {
	t.Parallel()

	type row struct {
		Value   int64
		Wrapper wrapper
		Masked  maskedWrapper
		Raw     sql.RawBytes
		Pointer *string
	}
	tests := map[string]func(){
		"nil dest":         func() { projection.ColumnOrZero[row, int64](qs.Col("a"), nil) },
		"dest returns nil": func() { projection.ColumnOrZero(qs.Col("a"), func(*row) *int64 { return nil }) },
		"pointer T":        func() { projection.ColumnOrZero(qs.Col("a"), func(r *row) **string { return &r.Pointer }) },
		"borrowed T":       func() { projection.ColumnOrZero(qs.Col("a"), func(r *row) *sql.RawBytes { return &r.Raw }) },
		"unstable address": func() { projection.ColumnOrZero(qs.Col("a"), func(*row) *int64 { return new(int64) }) },
		"initialised target": func() {
			projection.ColumnOrZero(qs.Col("a"), func(r *row) *wrapper {
				r.Wrapper = wrapper{target: &r.Value}
				return &r.Wrapper
			})
		},
		"initialised target that prints as zero": func() {
			projection.ColumnOrZero(qs.Col("a"), func(r *row) *maskedWrapper {
				r.Masked = maskedWrapper{target: &r.Value}
				return &r.Masked
			})
		},
		"initialised by Embed": func() {
			inner := projection.ColumnOrZero(qs.Col("a"), func(r *row) *wrapper { return &r.Wrapper })
			projection.Embed(inner, func(o *struct{ Row row }) *row {
				o.Row.Wrapper = wrapper{target: &o.Row.Value}
				return &o.Row
			})
		},
		"initialised by an outer Embed": func() {
			inner := projection.ColumnOrZero(qs.Col("a"), func(r *row) *wrapper { return &r.Wrapper })
			type middle struct{ Row row }
			embedded := projection.Embed(inner, func(m *middle) *row { return &m.Row })
			projection.Embed(embedded, func(o *struct{ Middle middle }) *middle {
				o.Middle.Row.Wrapper = wrapper{target: &o.Middle.Row.Value}
				return &o.Middle
			})
		},
	}
	for name, declare := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			declare()
		})
	}
}

// A set that does nothing reads a join key no field holds, and the key alone decides
// presence when every other column is nullable.
func TestGroupPresenceFromKeyWithoutField(t *testing.T) {
	t.Parallel()

	type human struct{ Nickname string }
	p := projection.EmbedNull(projection.New(
		projection.ColumnNull(qs.Col("h", "user_id"), func(*human, *int64) {}),
		projection.ColumnOrZero(qs.Col("h", "nickname"), func(h *human) *string { return &h.Nickname }),
	), func(d **human, h *human) { *d = h })

	got, err := p.Scan(fakeRow(int64(9), nil))
	if err != nil || got == nil || *got != (human{}) {
		t.Fatalf("key present: got %+v, %v, want a present zero group", got, err)
	}
	got, err = p.Scan(fakeRow(nil, nil))
	if err != nil || got != nil {
		t.Fatalf("key NULL: got %+v, %v, want an absent group", got, err)
	}
}

// The scan function may append destinations for output selected after Expressions.
func TestScanFunctionAppendsDestinations(t *testing.T) {
	t.Parallel()

	var total int64
	withTotal := func(scan func(dest ...any) error) func(dest ...any) error {
		return func(dest ...any) error { return scan(append(dest, &total)...) }
	}
	got, err := employees().Scan(withTotal(fakeRow(int64(1), "ada", nil, int64(2), "bob", nil, int64(42))))
	if err != nil {
		t.Fatal(err)
	}
	if got.Employee.ID != 1 || got.Manager == nil || got.Manager.ID != 2 || total != 42 {
		t.Fatalf("got %+v with manager %+v and total %d, want ids 1 and 2 and total 42", got.Employee, got.Manager, total)
	}
}

// totalRows reads a trailing count beside each row a projection reads.
type totalRows struct {
	projection.Rows
	total *int64
}

func (r totalRows) Scan(dest ...any) error { return r.Rows.Scan(append(dest, r.total)...) }

func TestCollectThroughRowsThatAppendDestinations(t *testing.T) {
	t.Parallel()

	total := int64(-1)
	rows := &fakeRows{unread: []func(dest ...any) error{
		fakeRow(int64(1), "ada", nil, int64(2)),
		fakeRow(int64(2), "bob", nil, int64(2)),
	}}
	got, err := flatUsers().Collect(totalRows{Rows: rows, total: &total})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 || total != 2 {
		t.Fatalf("got %+v with total %d, want users 1 and 2 and total 2", got, total)
	}

	total = -1
	if _, err := flatUsers().Collect(totalRows{Rows: &fakeRows{}, total: &total}); err != nil || total != -1 {
		t.Fatalf("no rows: total %d, %v, want it left at -1", total, err)
	}
}
