package projection

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jacoelho/qs"
)

// ErrNull reports NULL in a Column inside an EmbedNull group with at least one
// non-NULL column. Scan wraps it with the column's Expressions index and SQL.
// NULL means the driver left the destination nil: see EmbedNull for JSON null.
var ErrNull = errors.New("projection: NULL in required column of present group")

func columnLabel(position int, expression qs.Expr) string {
	sql, _, err := qs.ExprToSQL(expression)
	if err != nil {
		return fmt.Sprintf("column %d (unrendered: %v)", position, err)
	}
	return fmt.Sprintf("column %d (%s)", position, sql)
}

func nullError(position int, expression qs.Expr) error {
	return fmt.Errorf("projection: %s: %w", columnLabel(position, expression), ErrNull)
}

// cell is one post-scan step. null reports presence to an enclosing group.
type cell interface {
	null() bool
	finish() error
}

// reader passes by value: a pointer through part func values would escape.
type reader struct {
	dests   []any
	cells   []cell
	inGroup bool
	// declaring marks EmbedNull's declaration pass, the only place group checks run.
	declaring bool
}

type part[D any] func(d *D, r reader) reader

// Projection is an ordered, immutable list of result columns bound to fields of D.
// The zero Projection is empty.
type Projection[D any] struct {
	expressions []qs.Expr
	parts       []part[D]
	cells       int // cells Scan appends outside a group
}

// scanner mirrors database/sql.Scanner without importing it.
type scanner interface{ Scan(src any) error }

// requiredCell lets a group tolerate NULL in a Column until presence is known.
type requiredCell[T any] struct {
	value      *T
	dest       *T
	expression qs.Expr
	position   int
}

func (c *requiredCell[T]) null() bool { return c.value == nil }
func (c *requiredCell[T]) finish() error {
	if c.value == nil {
		return nullError(c.position, c.expression)
	}
	*c.dest = *c.value
	return nil
}

// scannerCell is requiredCell for a Scanner destination. A **T would make the
// driver allocate a zero T and lose the instance dest returned, which may hold a
// write-through target. NULL stops here because a group decides what it means.
type scannerCell struct {
	target     scanner
	expression qs.Expr
	position   int
	isNull     bool
}

func (c *scannerCell) Scan(src any) error {
	if src == nil {
		c.isNull = true
		return nil
	}
	return c.target.Scan(src)
}
func (c *scannerCell) null() bool { return c.isNull }
func (c *scannerCell) finish() error {
	if c.isNull {
		return nullError(c.position, c.expression)
	}
	return nil
}

// indirectReadProblem names why T must not be read through **T, or returns "". A
// pointer T would reach the driver as ***T, which pgx decodes through encoding/json
// for json and jsonb columns, changing the value. Drivers refuse sql.RawBytes and
// pgtype.DriverBytes where they would outlive the row, but only for the exact *T. A
// zero pgtype.CompositeFields, and a nil *pgtype.Multirange, panic in the codec on
// every row. Best effort by name: the package imports neither database/sql nor pgx.
func indirectReadProblem[T any]() string {
	name := fmt.Sprintf("%T", (*T)(nil))[1:]
	switch {
	case strings.HasPrefix(name, "*"):
		return fmt.Sprintf("T %s is a pointer; use ColumnNull[%s]", name, name[1:])
	case name == "sql.RawBytes", name == "pgtype.DriverBytes", name == "pgtype.CompositeFields",
		strings.HasPrefix(name, "pgtype.Multirange["):
		return fmt.Sprintf("T %s cannot be read through a pointer", name)
	}
	return ""
}

// checkGroupTarget rejects a target the driver cannot fill. It allocates a fresh zero
// T behind **T and the value is assigned through target afterwards, so a target that
// differs per call is never reached and an initialised wrapper loses its write-through.
// Printing cannot tell that wrapper from a plain default, so both are rejected.
// Declaration-only: panicking at Scan would strand the query, and printing is the zero
// test that needs neither reflect nor a comparable T.
func checkGroupTarget[T any](target, again *T, position int, expression qs.Expr) {
	label := func() string { return "projection: EmbedNull: group " + columnLabel(position, expression) }
	if problem := indirectReadProblem[T](); problem != "" {
		panic(label() + ": " + problem)
	}
	if target != again {
		panic(fmt.Sprintf("%s: dest for %T must return a stable address or implement Scan(any) error", label(), *target))
	}
	var zero T
	if fmt.Sprintf("%#v", *target) != fmt.Sprintf("%#v", zero) {
		panic(fmt.Sprintf("%s: dest for %T must return a zero target or implement Scan(any) error", label(), *target))
	}
}

// Column reads one required result column into the address dest returns, as
// rows.Scan(dest(&d)) does outside a group. dest returns a field of its argument, or a
// Scanner writing into it. It runs on a zero D at declaration and once per Scan, before
// the row is read, so it must be deterministic and must not return nil. It may allocate
// a nil pointer but must not replace a value already set; put nested pointers behind
// Embed's dest. Inside EmbedNull, NULL in a present group is ErrNull whatever T
// is; use ColumnNull for nullable columns. Column panics if dest is nil or returns nil.
func Column[D, T any](expression qs.Expr, dest func(*D) *T) Projection[D] {
	if dest == nil {
		panic("projection: Column with nil dest")
	}
	var zero D
	if dest(&zero) == nil {
		panic(fmt.Sprintf("projection: Column dest returned nil for a zero %T", zero))
	}
	return Projection[D]{expressions: []qs.Expr{expression}, parts: []part[D]{func(d *D, r reader) reader {
		target := dest(d)
		if !r.inGroup {
			r.dests = append(r.dests, target)
			return r
		}
		if s, ok := any(target).(scanner); ok {
			c := &scannerCell{target: s, position: len(r.dests), expression: expression}
			r.dests, r.cells = append(r.dests, c), append(r.cells, c)
			return r
		}
		if r.declaring {
			checkGroupTarget(target, dest(d), len(r.dests), expression)
		}
		c := &requiredCell[T]{dest: target, position: len(r.dests), expression: expression}
		r.dests, r.cells = append(r.dests, &c.value), append(r.cells, c)
		return r
	}}}
}

type nullCell[D, T any] struct {
	value *T
	d     *D
	set   func(*D, *T)
}

func (c *nullCell[D, T]) null() bool { return c.value == nil }
func (c *nullCell[D, T]) finish() error {
	c.set(c.d, c.value)
	return nil
}

// ColumnNull reads one nullable result column as var v *T; rows.Scan(&v) does, and calls
// set with v after the row is read: nil on NULL. See EmbedNull for what such a read
// requires of T. A write-through Scanner cannot be used here, because the driver
// allocates T. ColumnNull panics if set is nil or T is detected as unreadable that way.
func ColumnNull[D, T any](expression qs.Expr, set func(*D, *T)) Projection[D] {
	if set == nil {
		panic("projection: ColumnNull with nil set")
	}
	if problem := indirectReadProblem[T](); problem != "" {
		panic("projection: ColumnNull: " + problem)
	}
	return Projection[D]{expressions: []qs.Expr{expression}, cells: 1, parts: []part[D]{func(d *D, r reader) reader {
		c := &nullCell[D, T]{d: d, set: set}
		r.dests, r.cells = append(r.dests, &c.value), append(r.cells, c)
		return r
	}}}
}

// Embed reads inner into the value dest returns. dest runs on a zero D at declaration
// and once per Scan, before the row is read, so a nil pointer inside D must be
// allocated by dest. Allocate only if nil: Embeds sharing a pointer would otherwise
// each replace the last, losing its columns. inner's dests then receive what dest
// returned. Embed panics if dest is nil or returns nil.
func Embed[D, N any](inner Projection[N], dest func(*D) *N) Projection[D] {
	if dest == nil {
		panic("projection: Embed with nil dest")
	}
	var zero D
	if dest(&zero) == nil {
		panic(fmt.Sprintf("projection: Embed dest returned nil for a zero %T", zero))
	}
	// One part, not one per column: an allocating dest must not split a row.
	return Projection[D]{expressions: inner.expressions, cells: inner.cells, parts: []part[D]{func(d *D, r reader) reader {
		row := dest(d)
		for _, p := range inner.parts {
			r = p(row, r)
		}
		return r
	}}}
}

type groupCell[D, N any] struct {
	row   N
	d     *D
	set   func(*D, *N)
	cells []cell
}

func (c *groupCell[D, N]) null() bool {
	for _, inner := range c.cells {
		if !inner.null() {
			return false
		}
	}
	return true
}

func (c *groupCell[D, N]) finish() error {
	if c.null() {
		c.set(c.d, nil)
		return nil
	}
	for _, inner := range c.cells {
		if err := inner.finish(); err != nil {
			return err
		}
	}
	c.set(c.d, &c.row)
	c.cells, c.d, c.set = nil, nil, nil // row outlives Scan and must not pin the rest
	return nil
}

// EmbedNull reads inner as an optional group, such as the nullable side of a LEFT
// JOIN, and calls set after the row is read. set receives nil when every inner
// column is NULL, so a matched row whose selected columns are all NULL is
// indistinguishable from no match. Otherwise a NULL Column is ErrNull and a
// ColumnNull receives nil. Under an absent group Map does not run convert.
//
// Inside a group, at any depth, a Column whose T is not a Scanner reads as
// var v *T; rows.Scan(&v) does, with v == nil meaning NULL, and then assigns *v through
// dest. ColumnNull reads the same way everywhere. Such a read has that read's limits on
// each driver: T must decode from its zero value, because the driver allocates it, so
// the Column's dest must return a stable address holding a zero value; T must own its
// memory, because the driver's check for borrowed types sees only **T; and presence is
// what the driver reports, so a non-NULL value it decodes to nil counts as NULL. Which
// values do depends on driver and T: pgx v5.11 does for 'null'::jsonb into a string or
// a map.
//
// A Scanner T in a group reads as a value exposing only Scan(any), with NULL withheld
// from T, so it keeps JSON null present. On database/sql that matches a read outside a
// group. On pgx it bypasses T's native codec, so a read outside a group can differ: an
// error where the codec would convert, or another value.
//
// EmbedNull runs inner's Column and Embed dests at declaration and panics on what it
// detects: a pointer T, sql.RawBytes, pgtype.DriverBytes, pgtype.CompositeFields,
// pgtype.Multirange, or, unless T is a Scanner, an unstable address or a target that
// prints differently from zero. Detection is best effort: a defined type over any of
// these passes, as does any other driver's equivalent, and then behaves as the
// hand-written read does, including a pgx panic or a database/sql hang.
//
// EmbedNull panics if set is nil.
func EmbedNull[D, N any](inner Projection[N], set func(*D, *N)) Projection[D] {
	if set == nil {
		panic("projection: EmbedNull with nil set")
	}
	var row N
	declared := reader{inGroup: true, declaring: true}
	for _, p := range inner.parts {
		declared = p(&row, declared)
	}
	capacity := len(declared.cells)
	return Projection[D]{expressions: inner.expressions, cells: 1, parts: []part[D]{func(d *D, r reader) reader {
		c := &groupCell[D, N]{d: d, set: set}
		group := reader{dests: r.dests, cells: make([]cell, 0, capacity), inGroup: true}
		for _, p := range inner.parts {
			group = p(&c.row, group)
		}
		c.cells = group.cells
		r.dests, r.cells = group.dests, append(r.cells, c)
		return r
	}}}
}

type mapCell[B, T any] struct {
	row     B
	d       *T
	convert func(B) (T, error)
}

// null is always true: a conversion proves nothing about presence.
func (c *mapCell[B, T]) null() bool { return true }
func (c *mapCell[B, T]) finish() error {
	value, err := c.convert(c.row)
	if err != nil {
		return err
	}
	*c.d = value
	return nil
}

// Map transforms inner's value after the row is read. convert runs synchronously
// inside Scan while the result set is open and receives no context, so it must be
// CPU-only and safe for concurrent use. Its error is returned unchanged, without
// column context. Map panics if convert is nil.
func Map[B, T any](inner Projection[B], convert func(B) (T, error)) Projection[T] {
	if convert == nil {
		panic("projection: Map with nil convert")
	}
	return Projection[T]{expressions: inner.expressions, cells: inner.cells + 1, parts: []part[T]{func(d *T, r reader) reader {
		c := &mapCell[B, T]{d: d, convert: convert}
		for _, p := range inner.parts {
			r = p(&c.row, r)
		}
		r.cells = append(r.cells, c)
		return r
	}}}
}

// New concatenates projections in order, preserving duplicates.
func New[D any](projections ...Projection[D]) Projection[D] {
	var expressions, parts, cells int
	for _, q := range projections {
		expressions += len(q.expressions)
		parts += len(q.parts)
		cells += q.cells
	}
	p := Projection[D]{
		expressions: make([]qs.Expr, 0, expressions),
		parts:       make([]part[D], 0, parts),
		cells:       cells,
	}
	for _, q := range projections {
		p.expressions = append(p.expressions, q.expressions...)
		p.parts = append(p.parts, q.parts...)
	}
	return p
}

// Expressions returns a new slice of the declared expressions in scan order.
func (p Projection[D]) Expressions() []qs.Expr { return slices.Clone(p.expressions) }

// Scan reads one row through scan, normally rows.Scan or row.Scan, and returns the
// value of D. An error from scan is returned unchanged. On any error Scan returns
// the zero D.
func (p Projection[D]) Scan(scan func(dest ...any) error) (D, error) {
	var d, zero D
	r := reader{dests: make([]any, 0, len(p.expressions)), cells: make([]cell, 0, p.cells)}
	for _, q := range p.parts {
		r = q(&d, r)
	}
	if err := scan(r.dests...); err != nil {
		return zero, err
	}
	for _, c := range r.cells {
		if err := c.finish(); err != nil {
			return zero, err
		}
	}
	return d, nil
}
