package projection_test

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

type user struct {
	ID    int64
	Name  string
	Email *string
}

type employee struct {
	Employee user
	Manager  *user
}

func users(r qs.Relation, prefix string) projection.Projection[user] {
	return projection.New(
		projection.Column(r.Col("id").As(prefix+"id"), func(u *user) *int64 { return &u.ID }),
		projection.Column(r.Col("name").As(prefix+"name"), func(u *user) *string { return &u.Name }),
		projection.ColumnNull(r.Col("email").As(prefix+"email"), func(u *user, v *string) { u.Email = v }),
	)
}

func employees() projection.Projection[employee] {
	return projection.New(
		projection.Embed(users(qs.Table("users").As("e"), ""), func(r *employee) *user { return &r.Employee }),
		projection.EmbedNull(users(qs.Table("users").As("m"), "manager_"), func(r *employee, u *user) { r.Manager = u }),
	)
}

// fakeRow reads values as a driver does: a Scanner receives NULL itself, a pointer
// destination is allocated only for non-NULL, and NULL fails where T cannot hold it.
func fakeRow(values ...any) func(dest ...any) error {
	return func(dest ...any) error {
		if len(dest) != len(values) {
			return fmt.Errorf("fake row has %d columns, got %d destinations", len(values), len(dest))
		}
		for i, value := range values {
			if err := assign(dest[i], value); err != nil {
				return fmt.Errorf("column %d: %w", i, err)
			}
		}
		return nil
	}
}

func assign(dest, value any) error {
	if s, ok := dest.(interface{ Scan(src any) error }); ok {
		return s.Scan(value)
	}
	target := reflect.ValueOf(dest).Elem()
	if value == nil {
		switch target.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			target.SetZero()
			return nil
		default:
			return fmt.Errorf("cannot scan NULL into %T", dest)
		}
	}
	if target.Kind() == reflect.Pointer {
		target.Set(reflect.New(target.Type().Elem()))
		return assign(target.Interface(), value)
	}
	target.Set(reflect.ValueOf(value))
	return nil
}

func TestScanSelfJoin(t *testing.T) {
	t.Parallel()

	p := employees()
	tests := []struct {
		name   string
		values []any
		want   employee
	}{
		{
			name:   "manager present",
			values: []any{int64(1), "ada", nil, int64(2), "bob", "b@example.com"},
			want: employee{
				Employee: user{ID: 1, Name: "ada"},
				Manager:  &user{ID: 2, Name: "bob", Email: new("b@example.com")},
			},
		},
		{
			name:   "manager absent",
			values: []any{int64(1), "ada", "a@example.com", nil, nil, nil},
			want:   employee{Employee: user{ID: 1, Name: "ada", Email: new("a@example.com")}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := p.Scan(fakeRow(test.values...))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestErrNullIdentifiesColumn(t *testing.T) {
	t.Parallel()

	type wide struct {
		ID            int64
		First, Second *user
	}
	p := projection.New(
		projection.Column(qs.Col("id"), func(w *wide) *int64 { return &w.ID }),
		projection.EmbedNull(users(qs.Table("users").As("a"), "a_"), func(w *wide, u *user) { w.First = u }),
		projection.EmbedNull(users(qs.Table("users").As("b"), "b_"), func(w *wide, u *user) { w.Second = u }),
	)
	got, err := p.Scan(fakeRow(int64(1), int64(9), "a", nil, int64(2), nil, "x"))
	if !errors.Is(err, projection.ErrNull) {
		t.Fatalf("err = %v, want ErrNull", err)
	}
	const position = 5
	column, _, renderErr := qs.ExprToSQL(p.Expressions()[position])
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	if !strings.Contains(err.Error(), fmt.Sprint("column ", position)) || !strings.Contains(err.Error(), column) {
		t.Fatalf("err = %v, want position %d and %s", err, position, column)
	}
	if !reflect.DeepEqual(got, wide{}) {
		t.Fatalf("got %+v, want zero value", got)
	}
}

func TestErrNullKeepsRenderError(t *testing.T) {
	t.Parallel()

	type row struct {
		Value   int64
		Scanner sql.NullString
	}
	invalid := qs.Expr{}
	_, _, renderErr := qs.ExprToSQL(invalid)
	if renderErr == nil {
		t.Fatal("zero Expr rendered")
	}
	tests := map[string]projection.Projection[*row]{
		"value": projection.EmbedNull(projection.New(
			projection.Column(invalid, func(r *row) *int64 { return &r.Value }),
			projection.Column(qs.Col("present"), func(r *row) *sql.NullString { return &r.Scanner }),
		), func(d **row, r *row) { *d = r }),
		"scanner": projection.EmbedNull(projection.New(
			projection.Column(invalid, func(r *row) *sql.NullString { return &r.Scanner }),
			projection.Column(qs.Col("present"), func(r *row) *int64 { return &r.Value }),
		), func(d **row, r *row) { *d = r }),
	}
	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			present := any("x")
			if name == "scanner" {
				present = int64(1)
			}
			_, err := p.Scan(fakeRow(nil, present))
			if !errors.Is(err, projection.ErrNull) {
				t.Fatalf("err = %v, want ErrNull", err)
			}
			if !strings.Contains(err.Error(), renderErr.Error()) {
				t.Fatalf("err = %v, want render error %v", err, renderErr)
			}
		})
	}
}

func TestErrNullPositionThroughMap(t *testing.T) {
	t.Parallel()

	type row struct {
		ID   int64
		Name *string
	}
	names := projection.Map(users(qs.Table("users").As("m"), "m_"), func(u user) (string, error) { return u.Name, nil })
	p := projection.New(
		projection.Column(qs.Col("id"), func(r *row) *int64 { return &r.ID }),
		projection.EmbedNull(names, func(r *row, v *string) { r.Name = v }),
	)
	_, err := p.Scan(fakeRow(int64(1), int64(2), nil, nil))
	if !errors.Is(err, projection.ErrNull) || !strings.Contains(err.Error(), "column 2") {
		t.Fatalf("err = %v, want ErrNull at column 2", err)
	}
}

func TestColumnNullProvesPresence(t *testing.T) {
	t.Parallel()

	_, err := employees().Scan(fakeRow(int64(1), "ada", nil, nil, nil, "orphan@example.com"))
	if !errors.Is(err, projection.ErrNull) {
		t.Fatalf("err = %v, want ErrNull", err)
	}
}

func TestNestedGroups(t *testing.T) {
	t.Parallel()

	type team struct {
		ID   int64
		Lead *user
	}
	type row struct{ Team *team }
	p := projection.EmbedNull(projection.New(
		projection.Column(qs.Col("t.id"), func(t *team) *int64 { return &t.ID }),
		projection.EmbedNull(users(qs.Table("users").As("l"), "lead_"), func(t *team, u *user) { t.Lead = u }),
	), func(r *row, t *team) { r.Team = t })

	got, err := p.Scan(fakeRow(int64(1), nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Team == nil || got.Team.ID != 1 || got.Team.Lead != nil {
		t.Fatalf("absent inner group: %+v", got.Team)
	}
	got, err = p.Scan(fakeRow(nil, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Team != nil {
		t.Fatalf("absent outer group: %+v", got.Team)
	}
	got, err = p.Scan(fakeRow(int64(1), int64(2), "lead", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Team == nil || got.Team.Lead == nil || got.Team.Lead.Name != "lead" {
		t.Fatalf("present inner group: %+v", got.Team)
	}
}

func testNullOnlyInGroup[T any](t *testing.T) {
	t.Helper()
	t.Parallel()

	type row struct {
		ID    int64
		Value T
	}
	flat := projection.New(
		projection.Column(qs.Col("id"), func(r *row) *int64 { return &r.ID }),
		projection.Column(qs.Col("value"), func(r *row) *T { return &r.Value }),
	)
	if _, err := flat.Scan(fakeRow(int64(1), nil)); err != nil {
		t.Fatalf("flat: %v", err)
	}
	group := projection.EmbedNull(flat, func(*struct{}, *row) {})
	if _, err := group.Scan(fakeRow(int64(1), nil)); !errors.Is(err, projection.ErrNull) {
		t.Fatalf("group: err = %v, want ErrNull", err)
	}
}

func TestFlatPointerColumnAcceptsNull(t *testing.T) {
	t.Parallel()

	type row struct{ Value *string }
	p := projection.Column(qs.Col("value"), func(r *row) **string { return &r.Value })
	got, err := p.Scan(fakeRow(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != nil {
		t.Fatalf("got %v, want nil", *got.Value)
	}
}

func TestColumnAcceptsNullOnlyOutsideGroup(t *testing.T) {
	t.Parallel()

	t.Run("bytes", testNullOnlyInGroup[[]byte])
	t.Run("scanner", testNullOnlyInGroup[sql.NullString])
}

func TestMap(t *testing.T) {
	t.Parallel()

	inner := projection.Column(qs.Col("name"), func(s *string) *string { return s })
	upper := projection.Map(inner, func(s string) (string, error) { return strings.ToUpper(s), nil })
	got, err := upper.Scan(fakeRow("ada"))
	if err != nil || got != "ADA" {
		t.Fatalf("got %q, %v", got, err)
	}

	failure := errors.New("bad value")
	failing := projection.Map(inner, func(string) (string, error) { return "partial", failure })
	got, err = failing.Scan(fakeRow("ada"))
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want convert error", err)
	}
	if got != "" {
		t.Fatalf("got %q, want zero value", got)
	}
}

func TestMapDoesNotRunForAbsentGroup(t *testing.T) {
	t.Parallel()

	inner := projection.Column(qs.Col("name"), func(s *string) *string { return s })
	mapped := projection.Map(inner, func(string) (string, error) { return "", errors.New("convert ran") })
	p := projection.EmbedNull(mapped, func(d **string, v *string) { *d = v })
	got, err := p.Scan(fakeRow(nil))
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestScanErrorReturnedUnchanged(t *testing.T) {
	t.Parallel()

	failure := errors.New("connection lost")
	got, err := employees().Scan(func(dest ...any) error {
		if err := fakeRow(int64(1), "ada", nil, nil, nil, nil)(dest...); err != nil {
			return err
		}
		return failure
	})
	if err != failure { //nolint:errorlint // The contract is the unchanged error value.
		t.Fatalf("err = %v, want unchanged scan error", err)
	}
	if !reflect.DeepEqual(got, employee{}) {
		t.Fatalf("got %+v, want zero value", got)
	}
}

func TestDestRunsOncePerScan(t *testing.T) {
	t.Parallel()

	type field struct {
		Value   int64
		Scanner sql.NullString
	}
	type row struct{ Field field }
	tests := map[string]func(calls *int) func() error{
		"flat": func(calls *int) func() error {
			p := projection.Column(qs.Col("a"), func(f *field) *int64 { *calls++; return &f.Value })
			return func() error { _, err := p.Scan(fakeRow(int64(1))); return err }
		},
		"group value": func(calls *int) func() error {
			p := projection.EmbedNull(
				projection.Column(qs.Col("a"), func(f *field) *int64 { *calls++; return &f.Value }),
				func(*struct{}, *field) {},
			)
			return func() error { _, err := p.Scan(fakeRow(int64(1))); return err }
		},
		"group scanner": func(calls *int) func() error {
			p := projection.EmbedNull(
				projection.Column(qs.Col("a"), func(f *field) *sql.NullString { *calls++; return &f.Scanner }),
				func(*struct{}, *field) {},
			)
			return func() error { _, err := p.Scan(fakeRow("x")); return err }
		},
		"Embed in group": func(calls *int) func() error {
			p := projection.EmbedNull(
				projection.Embed(
					projection.Column(qs.Col("a"), func(f *field) *int64 { return &f.Value }),
					func(r *row) *field { *calls++; return &r.Field },
				),
				func(*struct{}, *row) {},
			)
			return func() error { _, err := p.Scan(fakeRow(int64(1))); return err }
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls int
			scan := build(&calls)
			for range 2 {
				before := calls
				if err := scan(); err != nil {
					t.Fatal(err)
				}
				if got := calls - before; got != 1 {
					t.Fatalf("dest ran %d times in one Scan, want 1", got)
				}
			}
		})
	}
}

// The shape each read hands the driver is what ties a declaration to the documented
// hand-written read.
func TestDriverReceivesDocumentedDestination(t *testing.T) {
	t.Parallel()

	type row struct {
		Value   string
		Scanner sql.NullString
	}
	var returned *string
	tests := map[string]struct {
		scan  func(scan func(...any) error) error
		check func(t *testing.T, dest any)
	}{
		"flat Column gets dest's address": {
			scan: func(scan func(...any) error) error {
				_, err := projection.Column(qs.Col("a"), func(r *row) *string {
					returned = &r.Value
					return returned
				}).Scan(scan)
				return err
			},
			check: func(t *testing.T, dest any) {
				t.Helper()
				if dest != any(returned) {
					t.Fatalf("got %T %p, want the address dest returned", dest, dest)
				}
			},
		},
		"ColumnNull gets a pointer to a nil pointer": {
			scan: func(scan func(...any) error) error {
				_, err := projection.ColumnNull(qs.Col("a"), func(*row, *string) {}).Scan(scan)
				return err
			},
			check: func(t *testing.T, dest any) {
				t.Helper()
				if v, ok := dest.(**string); !ok || *v != nil {
					t.Fatalf("got %T, want nil **string", dest)
				}
			},
		},
		"group Column gets a pointer to a nil pointer": {
			scan: func(scan func(...any) error) error {
				_, err := projection.EmbedNull(
					projection.Column(qs.Col("a"), func(r *row) *string { return &r.Value }),
					func(*struct{}, *row) {},
				).Scan(scan)
				return err
			},
			check: func(t *testing.T, dest any) {
				t.Helper()
				if v, ok := dest.(**string); !ok || *v != nil {
					t.Fatalf("got %T, want nil **string", dest)
				}
			},
		},
		"group Scanner gets a Scanner withholding NULL": {
			scan: func(scan func(...any) error) error {
				_, err := projection.EmbedNull(
					projection.Column(qs.Col("a"), func(r *row) *sql.NullString { return &r.Scanner }),
					func(*struct{}, *row) {},
				).Scan(scan)
				return err
			},
			check: func(t *testing.T, dest any) {
				t.Helper()
				if _, native := dest.(*sql.NullString); native {
					t.Fatal("got the native Scanner")
				}
				if _, ok := dest.(sql.Scanner); !ok {
					t.Fatalf("got %T, want a Scanner", dest)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := tc.scan(func(dest ...any) error {
				if len(dest) != 1 {
					t.Fatalf("got %d destinations", len(dest))
				}
				tc.check(t, dest[0])
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEmbedDestRunsOncePerRow(t *testing.T) {
	t.Parallel()

	type pair struct{ A, B string }
	type row struct{ Pair *pair }
	inner := projection.New(
		projection.Column(qs.Col("a"), func(p *pair) *string { return &p.A }),
		projection.Column(qs.Col("b"), func(p *pair) *string { return &p.B }),
	)
	p := projection.Embed(inner, func(r *row) *pair {
		if r.Pair == nil {
			r.Pair = new(pair)
		}
		return r.Pair
	})
	got, err := p.Scan(fakeRow("x", "y"))
	if err != nil {
		t.Fatal(err)
	}
	if *got.Pair != (pair{A: "x", B: "y"}) {
		t.Fatalf("got %+v", got.Pair)
	}
}

func TestEmbedInsideGroupWithAllocatingDest(t *testing.T) {
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
			},
		),
		func(d **person, p *person) { *d = p },
	)
	got, err := p.Scan(fakeRow("ada"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name.Value != "ada" {
		t.Fatalf("got %+v", got)
	}
}

// intoField writes through to a field, the Scanner shape Column allows.
type intoField struct{ target *int64 }

func (s *intoField) Scan(src any) error {
	value, ok := src.(int64)
	if !ok {
		return fmt.Errorf("unexpected source %v", src)
	}
	*s.target = value
	return nil
}

func TestWriteThroughScannerInGroup(t *testing.T) {
	t.Parallel()

	type group struct{ A, B int64 }
	p := projection.EmbedNull(projection.New(
		projection.Column(qs.Col("a"), func(g *group) *intoField { return &intoField{target: &g.A} }),
		projection.Column(qs.Col("b"), func(g *group) *int64 { return &g.B }),
	), func(d **group, g *group) { *d = g })

	got, err := p.Scan(fakeRow(int64(7), int64(8)))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != (group{A: 7, B: 8}) {
		t.Fatalf("present: %+v", got)
	}
	got, err = p.Scan(fakeRow(nil, nil))
	if err != nil || got != nil {
		t.Fatalf("absent: %+v, %v", got, err)
	}
	if _, err = p.Scan(fakeRow(nil, int64(8))); !errors.Is(err, projection.ErrNull) {
		t.Fatalf("partial: err = %v, want ErrNull", err)
	}
}

func TestDeclarationPanics(t *testing.T) {
	t.Parallel()

	type field struct {
		Value *int64
		Other int64
	}
	type nested struct{ Field field }
	type pointerToField struct{ Field *field }
	inner := projection.Column(qs.Col("a"), func(f *field) *int64 { return &f.Other })
	tests := map[string]func(){
		"Column nil dest":    func() { projection.Column[field, int64](qs.Col("a"), nil) },
		"ColumnNull nil set": func() { projection.ColumnNull[field, int64](qs.Col("a"), nil) },
		"Embed nil dest":     func() { projection.Embed[struct{}, field](inner, nil) },
		"EmbedNull nil set":  func() { projection.EmbedNull[struct{}, field](inner, nil) },
		"Map nil convert":    func() { projection.Map[field, int64](inner, nil) },
		"ColumnNull borrowed": func() {
			projection.ColumnNull(qs.Col("a"), func(*field, *sql.RawBytes) {})
		},
		"ColumnNull pointer": func() {
			projection.ColumnNull(qs.Col("a"), func(*field, **string) {})
		},
		"group Column pointer": func() {
			projection.EmbedNull(projection.Column(qs.Col("a"), func(f *field) **int64 { return &f.Value }), func(*struct{}, *field) {})
		},
		"group Column pointer through Embed": func() {
			pointer := projection.Column(qs.Col("a"), func(f *field) **int64 { return &f.Value })
			projection.EmbedNull(projection.Embed(pointer, func(f *nested) *field { return &f.Field }), func(*struct{}, *nested) {})
		},
		"group Column pointer through Map": func() {
			pointer := projection.Column(qs.Col("a"), func(f *field) **int64 { return &f.Value })
			projection.EmbedNull(
				projection.Map(pointer, func(f field) (field, error) { return f, nil }),
				func(*struct{}, *field) {},
			)
		},
		"Column dest returns nil": func() {
			projection.Column(qs.Col("a"), func(f *field) *int64 { return f.Value })
		},
		"Embed dest returns nil": func() {
			projection.Embed(inner, func(p *pointerToField) *field { return p.Field })
		},
		"Column dest needs another accessor's allocation": func() {
			projection.Column(qs.Col("a"), func(p *pointerToField) *int64 { return &p.Field.Other })
		},
		"group Column borrowed": func() {
			borrowed := projection.Column(qs.Col("a"), func(f *rawField) *sql.RawBytes { return &f.Raw })
			projection.EmbedNull(borrowed, func(*struct{}, *rawField) {})
		},
		"group initialised target": func() {
			initialised := projection.Column(qs.Col("a"), func(f *initialisedField) *wrapper {
				f.Wrapper = wrapper{target: &f.Value}
				return &f.Wrapper
			})
			projection.EmbedNull(initialised, func(*struct{}, *initialisedField) {})
		},
		"group unstable dest": func() {
			projection.EmbedNull(projection.Column(qs.Col("a"), func(*field) *int64 { return new(int64) }), func(*struct{}, *field) {})
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

type rawField struct{ Raw sql.RawBytes }

// wrapper is not a Scanner, so a driver allocates a fresh one and loses target.
type wrapper struct{ target *int64 }

type initialisedField struct {
	Wrapper wrapper
	Value   int64
}

func TestDeclarationAllowsDriverFillableTargets(t *testing.T) {
	t.Parallel()

	type fields struct{ Values []any }
	flat := projection.Column(qs.Col("a"), func(f *rawField) *sql.RawBytes { return &f.Raw })
	if len(flat.Expressions()) != 1 {
		t.Fatal("flat borrowed column not declared")
	}
	// Group-only checks: outside a group the driver reads the returned address.
	fresh := projection.Column(qs.Col("a"), func(*fields) *int64 { return new(int64) })
	initialised := projection.Column(qs.Col("a"), func(f *initialisedField) *wrapper {
		f.Wrapper = wrapper{target: &f.Value}
		return &f.Wrapper
	})
	if len(fresh.Expressions()) != 1 || len(initialised.Expressions()) != 1 {
		t.Fatal("flat write-through column not declared")
	}
	group := projection.EmbedNull(
		projection.Column(qs.Col("a"), func(f *fields) *[]any { return &f.Values }),
		func(d **fields, f *fields) { *d = f },
	)
	got, err := group.Scan(fakeRow([]any{"x"}))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Values) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestScansAreIndependent(t *testing.T) {
	t.Parallel()

	shared := users(qs.Table("users").As("u"), "")
	type row struct{ A, B *user }
	p := projection.New(
		projection.EmbedNull(shared, func(r *row, u *user) { r.A = u }),
		projection.EmbedNull(shared, func(r *row, u *user) { r.B = u }),
	)
	first, err := p.Scan(fakeRow(int64(1), "a", nil, int64(2), "b", nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(fakeRow(int64(3), "c", nil, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if first.A.ID != 1 || first.B.ID != 2 || second.A.ID != 3 || second.B != nil {
		t.Fatalf("first %+v %+v, second %+v %+v", first.A, first.B, second.A, second.B)
	}
	if first.A == second.A {
		t.Fatal("scans share a group value")
	}
}

func TestConcurrentScan(t *testing.T) {
	t.Parallel()

	p := employees()
	var group sync.WaitGroup
	for id := range int64(8) {
		group.Go(func() {
			got, err := p.Scan(fakeRow(id, "ada", nil, id+100, "bob", nil))
			if err != nil {
				t.Error(err)
				return
			}
			if got.Employee.ID != id || got.Manager.ID != id+100 {
				t.Errorf("id %d: got %+v %+v", id, got.Employee, got.Manager)
			}
		})
	}
	group.Wait()
}

func TestOutputInSelectAndReturning(t *testing.T) {
	t.Parallel()

	p := projection.New(
		projection.Column(qs.Col("d.id").As("id"), func(u *user) *int64 { return &u.ID }),
		projection.Column(qs.Param("draft").As("status"), func(u *user) *string { return &u.Name }),
	)
	checkSQL(t, qs.Select(p.Expressions()...).FromExpr(qs.Table("documents").As("d")),
		`SELECT "d"."id" AS "id", $1 AS "status" FROM "documents" AS "d"`, "draft")
	checkSQL(t, qs.UpdateTable(qs.Table("documents").As("d")).
		Set(qs.SetNull("archived_at")).Where(qs.Eq("d.id", "doc-1")).Returning(p.Expressions()...),
		`UPDATE "documents" AS "d" SET "archived_at" = NULL WHERE ("d"."id" = $1) RETURNING "d"."id" AS "id", $2 AS "status"`,
		"doc-1", "draft")
}

func TestExpressionsAreOwned(t *testing.T) {
	t.Parallel()

	p := projection.Column(qs.Col("id"), func(u *user) *int64 { return &u.ID })
	p.Expressions()[0] = qs.Col("changed")
	checkSQL(t, qs.Select(p.Expressions()...), `SELECT "id"`)
}

func TestNestedQueriesStayLive(t *testing.T) {
	t.Parallel()

	child := qs.SelectCols("id").From("documents")
	p := projection.Column(qs.Scalar(child), func(u *user) *int64 { return &u.ID })
	extracted := p.Expressions()
	child.Where(qs.Eq("status", "draft"))
	want := `SELECT (SELECT "id" FROM "documents" WHERE ("status" = $1))`
	checkSQL(t, qs.Select(p.Expressions()...), want, "draft")
	checkSQL(t, qs.Select(extracted...), want, "draft")
}

func TestEmptyProjection(t *testing.T) {
	t.Parallel()

	var zero projection.Projection[user]
	for _, p := range []projection.Projection[user]{zero, projection.New[user](), projection.New(zero, zero)} {
		if len(p.Expressions()) != 0 {
			t.Fatal("empty projection has content")
		}
		if _, _, err := qs.Select(p.Expressions()...).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("empty SELECT error = %v, want ErrInvalid", err)
		}
		checkSQL(t, qs.Update("documents").Set(qs.SetNull("archived_at")).Returning(p.Expressions()...),
			`UPDATE "documents" SET "archived_at" = NULL`)
		if got, err := p.Scan(fakeRow()); err != nil || !reflect.DeepEqual(got, user{}) {
			t.Fatalf("got %+v, %v", got, err)
		}
	}

	group := projection.EmbedNull(zero, func(d **user, u *user) { *d = u })
	if got, err := group.Scan(fakeRow()); err != nil || got != nil {
		t.Fatalf("empty group: got %+v, %v", got, err)
	}
}

func TestInvalidExpressionFailsRendering(t *testing.T) {
	t.Parallel()

	for _, expression := range []qs.Expr{{}, qs.Col("id").As(""), qs.Col("id").As("bad\x00alias")} {
		p := projection.Column(expression, func(u *user) *int64 { return &u.ID })
		if _, _, err := qs.Select(p.Expressions()...).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("render error = %v, want ErrInvalid", err)
		}
	}
}

func checkSQL(tb testing.TB, statement qs.Statement, want string, wantArgs ...any) {
	tb.Helper()
	text, args, err := qs.ToSQL(statement)
	if err != nil {
		tb.Fatalf("render: %v", err)
	}
	if text != want || !slices.Equal(args, wantArgs) {
		tb.Fatalf("SQL = %s args=%v, want %s args=%v", text, args, want, wantArgs)
	}
}
