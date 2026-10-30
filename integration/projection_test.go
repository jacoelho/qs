//go:build postgres

package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

// Inline data, not schema tables: a database/sql pool would lose a connection's
// search_path.
const projectionUsers = `WITH "users"("id", "name", "email", "manager_id") AS (VALUES
	(1::bigint, 'ada'::text, NULL::text, NULL::bigint),
	(2::bigint, 'bob'::text, 'bob@example.com'::text, 1::bigint),
	(3::bigint, NULL::text, 'broken@example.com'::text, NULL::bigint),
	(4::bigint, 'cal'::text, NULL::text, 3::bigint)) `

type projectionUser struct {
	ID    int64
	Name  string
	Email *string
}

type projectionRow struct {
	Employee projectionUser
	Manager  *projectionUser
}

func projectionUsersOf(r qs.Relation, prefix string) projection.Projection[projectionUser] {
	return projection.New(
		projection.Column(r.Col("id").As(prefix+"id"), func(u *projectionUser) *int64 { return &u.ID }),
		projection.Column(r.Col("name").As(prefix+"name"), func(u *projectionUser) *string { return &u.Name }),
		projection.ColumnNull(r.Col("email").As(prefix+"email"), func(u *projectionUser, v *string) { u.Email = v }),
	)
}

// projectionRows is the shape pgx.Rows and *sql.Rows share once Close is normalised.
type projectionRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }

type projectionDriver struct {
	name  string
	query func(ctx context.Context, text string, args ...any) (projectionRows, error)
}

// projectionDrivers covers both drivers under pgx's default and simple protocols,
// which decode through different formats. It shares one pgx connection, so subtests
// must be serial and close rows before the next query.
func projectionDrivers(t *testing.T) []projectionDriver {
	t.Helper()
	session := connect(t)
	pgxDriver := func(name string, extra ...any) projectionDriver {
		return projectionDriver{name, func(ctx context.Context, text string, args ...any) (projectionRows, error) {
			return session.Query(ctx, text, append(slices.Clip(extra), args...)...)
		}}
	}
	sqlDriver := func(name string, mode pgx.QueryExecMode) projectionDriver {
		config := session.Config()
		config.DefaultQueryExecMode = mode
		db := stdlib.OpenDB(*config)
		t.Cleanup(func() { _ = db.Close() })
		return projectionDriver{name, func(ctx context.Context, text string, args ...any) (projectionRows, error) {
			rows, err := db.QueryContext(ctx, text, args...)
			if err != nil {
				return nil, err
			}
			return sqlRows{rows}, nil
		}}
	}
	return []projectionDriver{
		pgxDriver("pgx"),
		pgxDriver("pgx simple", pgx.QueryExecModeSimpleProtocol),
		sqlDriver("database/sql", session.Config().DefaultQueryExecMode),
		sqlDriver("database/sql simple", pgx.QueryExecModeSimpleProtocol),
	}
}

func scanAll[D any](ctx context.Context, d projectionDriver, p projection.Projection[D], text string, args ...any) ([]D, error) {
	rows, err := d.query(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []D
	for rows.Next() {
		row, err := p.Scan(rows.Scan)
		if err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func TestProjectionSelfJoin(t *testing.T) {
	employee := qs.Table("users").As("employee")
	manager := qs.Table("users").As("manager")
	p := projection.New(
		projection.Embed(projectionUsersOf(employee, ""), func(r *projectionRow) *projectionUser { return &r.Employee }),
		projection.EmbedNull(projectionUsersOf(manager, "manager_"), func(r *projectionRow, u *projectionUser) { r.Manager = u }),
	)
	join := qs.LeftJoin(employee, manager).On(employee.Col("manager_id").EqExpr(manager.Col("id")))
	valid, _, err := qs.Select(p.Expressions()...).FromExpr(join).
		Where(employee.Col("id").Lte(2)).OrderBy(employee.Col("id").Asc()).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	partial, _, err := qs.Select(p.Expressions()...).FromExpr(join).Where(employee.Col("id").Eq(4)).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	ada := projectionUser{ID: 1, Name: "ada"}
	email := "bob@example.com"
	want := []projectionRow{
		{Employee: ada},
		{Employee: projectionUser{ID: 2, Name: "bob", Email: &email}, Manager: &ada},
	}
	for _, d := range projectionDrivers(t) {
		t.Run(d.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			got, err := scanAll(ctx, d, p, projectionUsers+valid, int64(2))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			if _, err := scanAll(ctx, d, p, projectionUsers+partial, int64(4)); !errors.Is(err, projection.ErrNull) {
				t.Fatalf("partial NULL: err = %v, want ErrNull", err)
			}
			again, err := scanAll(ctx, d, p, projectionUsers+valid, int64(2))
			if err != nil || len(again) != len(want) {
				t.Fatalf("connection after ErrNull: %d rows, %v", len(again), err)
			}
		})
	}
}

type projectionIntoField struct{ target *int64 }

func (s *projectionIntoField) Scan(src any) error {
	value, ok := src.(int64)
	if !ok {
		return fmt.Errorf("unexpected source %T", src)
	}
	*s.target = value
	return nil
}

func TestProjectionWriteThroughScannerInGroup(t *testing.T) {
	type group struct{ A, B int64 }
	p := projection.EmbedNull(projection.New(
		projection.Column(qs.Col("a"), func(g *group) *projectionIntoField { return &projectionIntoField{target: &g.A} }),
		projection.Column(qs.Col("b"), func(g *group) *int64 { return &g.B }),
	), func(d **group, g *group) { *d = g })
	for _, d := range projectionDrivers(t) {
		t.Run(d.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			got, err := scanAll(ctx, d, p, `SELECT * FROM (VALUES (7::bigint, 8::bigint), (NULL, NULL)) AS t`)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0] == nil || *got[0] != (group{A: 7, B: 8}) || got[1] != nil {
				t.Fatalf("got %+v", got)
			}
			if _, err := scanAll(ctx, d, p, `SELECT NULL::bigint, 8::bigint`); !errors.Is(err, projection.ErrNull) {
				t.Fatalf("partial NULL: err = %v, want ErrNull", err)
			}
		})
	}
}

type readKind string

const (
	readValue readKind = "value"
	readNull  readKind = "NULL"
	readError readKind = "error"
	readPanic readKind = "panic"
)

type readOutcome[T any] struct {
	kind  readKind
	value T
}

// scanOnly is the documented read of a Scanner in a group: Scan(any), NULL withheld.
type scanOnly[T any] struct {
	target *T
	null   bool
}

func (s *scanOnly[T]) Scan(src any) error {
	if src == nil {
		s.null = true
		return nil
	}
	target, ok := any(s.target).(sql.Scanner)
	if !ok {
		return fmt.Errorf("%T is not a Scanner", s.target)
	}
	return target.Scan(src)
}

// readOneRow runs read on the first row. Only pgx panics can be recovered: a panic
// under database/sql hangs in Rows.Close, so entries are vetted before joining the
// matrix and Multirange stays out of it.
func readOneRow[T any](ctx context.Context, d projectionDriver, text string, read func(rows projectionRows) readOutcome[T]) (out readOutcome[T]) {
	defer func() {
		if recover() != nil {
			out = readOutcome[T]{kind: readPanic}
		}
	}()
	rows, err := d.query(ctx, text)
	if err != nil {
		return readOutcome[T]{kind: readError}
	}
	defer rows.Close()
	if !rows.Next() {
		return readOutcome[T]{kind: readError}
	}
	return read(rows)
}

// handWrittenPointerRead is the read ColumnNull and group Columns document: no
// projection code is involved.
func handWrittenPointerRead[T any](ctx context.Context, d projectionDriver, text string) readOutcome[T] {
	return readOneRow(ctx, d, text, func(rows projectionRows) readOutcome[T] {
		var v *T
		switch err := rows.Scan(&v); {
		case err != nil:
			return readOutcome[T]{kind: readError}
		case v == nil:
			return readOutcome[T]{kind: readNull}
		}
		return readOutcome[T]{kind: readValue, value: *v}
	})
}

func handWrittenScannerRead[T any](ctx context.Context, d projectionDriver, text string) readOutcome[T] {
	return readOneRow(ctx, d, text, func(rows projectionRows) readOutcome[T] {
		var v T
		s := scanOnly[T]{target: &v}
		switch err := rows.Scan(&s); {
		case err != nil:
			return readOutcome[T]{kind: readError}
		case s.null:
			return readOutcome[T]{kind: readNull}
		}
		return readOutcome[T]{kind: readValue, value: v}
	})
}

// projectedRead scans p, whose result is nil for NULL and for an absent group.
func projectedRead[D, T any](ctx context.Context, d projectionDriver, p projection.Projection[D], text string, value func(D) *T) readOutcome[T] {
	return readOneRow(ctx, d, text, func(rows projectionRows) readOutcome[T] {
		row, err := p.Scan(rows.Scan)
		switch {
		case errors.Is(err, projection.ErrNull):
			return readOutcome[T]{kind: readNull}
		case err != nil:
			return readOutcome[T]{kind: readError}
		}
		if v := value(row); v != nil {
			return readOutcome[T]{kind: readValue, value: *v}
		}
		return readOutcome[T]{kind: readNull}
	})
}

type mixedRow[T any] struct {
	ID    int64
	Value T
}

// checkAgainstHandWritten compares each declaration with the read it documents, so
// spelling, codec and protocol differences need no list of their own. requireValue
// guards against both sides failing for an unrelated reason.
func checkAgainstHandWritten[T any](t *testing.T, drivers []projectionDriver, text string, requireValue bool) {
	t.Helper()
	label := func(d projectionDriver, shape string) string {
		return fmt.Sprintf("%s: %s: %T from %s", d.name, shape, *new(T), text)
	}
	value := projection.Column(qs.Col("x"), func(v *T) *T { return v })
	nullable := projection.ColumnNull(qs.Col("x"), func(d **T, v *T) { *d = v })
	sole := projection.EmbedNull(value, func(d **T, v *T) { *d = v })
	mixed := projection.EmbedNull(projection.New(
		projection.Column(qs.Col("id"), func(r *mixedRow[T]) *int64 { return &r.ID }),
		projection.Column(qs.Col("x"), func(r *mixedRow[T]) *T { return &r.Value }),
	), func(d **mixedRow[T], r *mixedRow[T]) { *d = r })
	mixedText := `SELECT 1::bigint, q.* FROM (` + text + `) AS q`

	_, isScanner := any(new(T)).(sql.Scanner)
	for _, d := range drivers {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		pointer := handWrittenPointerRead[T](ctx, d, text)
		group := pointer
		if isScanner {
			group = handWrittenScannerRead[T](ctx, d, text)
		}
		if requireValue && (pointer.kind != readValue || group.kind != readValue) {
			t.Errorf("%s: hand-written reads: %v, %v", label(d, "oracle"), pointer.kind, group.kind)
		}
		got := map[string]readOutcome[T]{
			"ColumnNull": projectedRead(ctx, d, nullable, text, func(v *T) *T { return v }),
			"sole group": projectedRead(ctx, d, sole, text, func(v *T) *T { return v }),
			"mixed group": projectedRead(ctx, d, mixed, mixedText, func(r *mixedRow[T]) *T {
				if r == nil {
					return nil
				}
				return &r.Value
			}),
		}
		cancel()
		for shape, outcome := range got {
			want := group
			if shape == "ColumnNull" {
				want = pointer
			}
			if !reflect.DeepEqual(outcome, want) {
				t.Errorf("%s: got %+v, want %+v", label(d, shape), outcome, want)
			}
		}
	}
}

func TestProjectionReadsAsHandWritten(t *testing.T) {
	drivers := projectionDrivers(t)
	present := func(check func(*testing.T, []projectionDriver, string, bool), texts ...string) {
		t.Helper()
		for _, text := range texts {
			check(t, drivers, text, true)
		}
	}
	jsonValues := []string{
		`SELECT 'null'::jsonb`, `SELECT 'null'::json`, `SELECT ' null '::json`,
		`SELECT '{"a":null}'::jsonb`, `SELECT '"x"'::jsonb`, `SELECT '[1]'::json`,
		`SELECT '{"a":1}'::jsonb`,
	}
	present(checkAgainstHandWritten[string], `SELECT ''::text`, `SELECT 'x'::text`)
	present(checkAgainstHandWritten[[]byte], `SELECT 'ab'::bytea`, `SELECT ''::bytea`)
	present(checkAgainstHandWritten[sql.NullString], `SELECT 'x'::text`, `SELECT 'ab'::bytea`)
	present(checkAgainstHandWritten[pgtype.Text], `SELECT 'x'::text`, `SELECT 'ab'::bytea`)
	present(checkAgainstHandWritten[pgtype.Numeric], `SELECT 'NaN'::numeric`, `SELECT 'Infinity'::numeric`, `SELECT 1.50::numeric`)
	present(checkAgainstHandWritten[pgtype.Timestamptz], `SELECT 'infinity'::timestamptz`)
	present(checkAgainstHandWritten[pgtype.Date], `SELECT 'infinity'::date`)
	present(checkAgainstHandWritten[time.Time], `SELECT '2026-01-02 03:04:05+00'::timestamptz`)
	present(checkAgainstHandWritten[pgtype.UUID], `SELECT '00010203-0405-0607-0809-0a0b0c0d0e0f'::uuid`)
	present(checkAgainstHandWritten[[16]byte], `SELECT '00010203-0405-0607-0809-0a0b0c0d0e0f'::uuid`)
	present(checkAgainstHandWritten[netip.Addr], `SELECT '1.2.3.4'::inet`)
	present(checkAgainstHandWritten[pgtype.Point], `SELECT '(1,2)'::point`)
	present(checkAgainstHandWritten[pgtype.Interval], `SELECT '1 day'::interval`)
	present(checkAgainstHandWritten[pgtype.Bits], `SELECT B'101'::bit(3)`)
	present(checkAgainstHandWritten[pgtype.Range[pgtype.Int4]], `SELECT '[1,2)'::int4range`)
	present(checkAgainstHandWritten[[]string], `SELECT array['a']::text[]`, `SELECT '{}'::text[]`)
	present(checkAgainstHandWritten[pgtype.FlatArray[string]], `SELECT array['a']::text[]`, `SELECT '{}'::text[]`)

	// Whether JSON null is a value depends on driver and T, which the oracle decides.
	jsonAny := func(check func(*testing.T, []projectionDriver, string, bool)) {
		t.Helper()
		for _, text := range jsonValues {
			check(t, drivers, text, false)
		}
	}
	jsonAny(checkAgainstHandWritten[string])
	jsonAny(checkAgainstHandWritten[[]byte])
	jsonAny(checkAgainstHandWritten[sql.NullString])
	jsonAny(checkAgainstHandWritten[map[string]any])
	jsonAny(checkAgainstHandWritten[json.RawMessage])
	jsonAny(checkAgainstHandWritten[any])

	// NULL and failures compare as outcomes, so no value is required.
	checkAgainstHandWritten[string](t, drivers, `SELECT NULL::text`, false)
	checkAgainstHandWritten[map[string]any](t, drivers, `SELECT NULL::jsonb`, false)
	checkAgainstHandWritten[sql.NullString](t, drivers, `SELECT NULL::text`, false)
	checkAgainstHandWritten[[]string](t, drivers, `SELECT array['a', NULL]::text[]`, false)
	checkAgainstHandWritten[time.Duration](t, drivers, `SELECT '1 hour'::interval`, false)
	checkAgainstHandWritten[pgtype.Text](t, drivers, `SELECT 5::int4`, false)
	checkAgainstHandWritten[pgtype.Numeric](t, drivers, `SELECT 5::int4`, false)
	checkAgainstHandWritten[pgtype.Int8](t, drivers, `SELECT 5.0::numeric`, false)

	// A dead connection would pass the outcome comparisons above as matching errors.
	checkAgainstHandWritten[int64](t, drivers, `SELECT 1::bigint`, true)
}

// A Scanner sees the driver's source, so JSON null stays distinct from SQL NULL.
func TestProjectionScannerKeepsJSONNullPresent(t *testing.T) {
	type row struct {
		ID      int64
		Payload sql.NullString
	}
	payload := projection.Column(qs.Col("payload"), func(r *row) *sql.NullString { return &r.Payload })
	sole := projection.EmbedNull(payload, func(d **row, r *row) { *d = r })
	mixed := projection.EmbedNull(projection.New(
		projection.Column(qs.Col("id"), func(r *row) *int64 { return &r.ID }),
		payload,
	), func(d **row, r *row) { *d = r })
	tests := []struct{ literal, want string }{
		{`'null'::jsonb`, "null"},
		{`'null'::json`, "null"},
		{`' null '::json`, " null "},
	}
	for _, d := range projectionDrivers(t) {
		t.Run(d.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			for _, tc := range tests {
				want := sql.NullString{String: tc.want, Valid: true}
				got, err := scanAll(ctx, d, sole, `SELECT `+tc.literal)
				if err != nil {
					t.Fatalf("%s: %v", tc.literal, err)
				}
				if len(got) != 1 || got[0] == nil || got[0].Payload != want {
					t.Fatalf("%s sole column: %+v", tc.literal, got)
				}
				got, err = scanAll(ctx, d, mixed, `SELECT 1::bigint, `+tc.literal)
				if err != nil {
					t.Fatalf("%s: %v", tc.literal, err)
				}
				if len(got) != 1 || got[0] == nil || got[0].ID != 1 || got[0].Payload != want {
					t.Fatalf("%s mixed: %+v", tc.literal, got)
				}
			}
		})
	}
}

type projectionMultirange = pgtype.Multirange[pgtype.Range[pgtype.Int4]]

// Multirange reads flat; only the read through a nil pointer is unusable. It is declared
// here and never scanned through a pointer, which would hang database/sql.
func TestProjectionReadsMultirangeFlat(t *testing.T) {
	p := projection.Column(qs.Col("x"), func(v *projectionMultirange) *projectionMultirange { return v })
	for _, d := range projectionDrivers(t) {
		t.Run(d.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			got, err := scanAll(ctx, d, p, `SELECT '{[1,2)}'::int4multirange`)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || len(got[0]) != 1 {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

// pgx names and %#v output are not visible from the projection module.
func TestProjectionRejectsTargetsTheDriverCannotFill(t *testing.T) {
	type row struct {
		A int32
		B string
		C pgtype.CompositeFields
		D pgtype.DriverBytes
		M projectionMultirange
	}
	tests := map[string]func(){
		"ColumnNull borrowed": func() {
			projection.ColumnNull(qs.Col("a"), func(*row, *pgtype.DriverBytes) {})
		},
		"ColumnNull multirange": func() {
			projection.ColumnNull(qs.Col("a"), func(*row, *projectionMultirange) {})
		},
		"group multirange": func() {
			multirange := projection.Column(qs.Col("a"), func(r *row) *projectionMultirange { return &r.M })
			projection.EmbedNull(multirange, func(*struct{}, *row) {})
		},
		"ColumnNull composite": func() {
			projection.ColumnNull(qs.Col("a"), func(*row, *pgtype.CompositeFields) {})
		},
		"group borrowed": func() {
			borrowed := projection.Column(qs.Col("a"), func(r *row) *pgtype.DriverBytes { return &r.D })
			projection.EmbedNull(borrowed, func(*struct{}, *row) {})
		},
		"group composite": func() {
			composite := projection.Column(qs.Col("a"), func(r *row) *pgtype.CompositeFields {
				r.C = pgtype.CompositeFields{&r.A, &r.B}
				return &r.C
			})
			projection.EmbedNull(composite, func(*struct{}, *row) {})
		},
	}
	for name, declare := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			declare()
		})
	}
}
