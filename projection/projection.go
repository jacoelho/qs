package projection

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jacoelho/qs"
)

// ErrNull reports NULL in a Column inside an EmbedNull group with at least one
// non-NULL column. Scan wraps it with the column's Expressions index and SQL. When
// several such columns are NULL it names one of them.
// NULL means the driver left the destination nil: see EmbedNull for JSON null.
var ErrNull = errors.New("projection: NULL in required column of present group")

func columnLabel(position int, expression qs.Expr) string {
	sql, _, err := qs.ExprToSQL(expression)
	if err != nil {
		return fmt.Sprintf("column %d (unrendered: %v)", position, err)
	}
	return fmt.Sprintf("column %d (%s)", position, sql)
}

func nullError(position int, expression *qs.Expr) error {
	return fmt.Errorf("projection: %s: %w", columnLabel(position, *expression), ErrNull)
}

// cell is one post-scan step. null reports presence to an enclosing group.
type cell interface {
	null() bool
	finish() error
}

// scratch is what consecutive rows of one result set reuse: the destination list, the
// cell lists and the cells themselves. Nothing in it outlives the row it was filled
// for, except as storage. A cell is filled again for the next row unless it holds a row
// that a set received: a present group's cell is replaced (groupCell.present) and a
// Map's cell is never taken from the pool. The package documentation says what callers
// may keep.
//
// A nil *scratch is a single row: cellAt and list then allocate.
type scratch struct {
	dests []any
	// pool holds the cells created for the previous row, in creation order, which the
	// declaration fixes.
	pool []cell
	// arena backs this row's cell lists.
	arena []cell
	// next is the pool slot being filled for this row.
	next int
	// need is what this row asked of arena.
	need int
}

// cellAt returns the cell the calling part fills for this row: the one it filled for
// the previous row, or a new one when s is nil. The caller overwrites it. The
// single-row path stays small enough to inline.
func cellAt[C any, P interface {
	*C
	cell
}](s *scratch) P {
	if s == nil {
		return new(C)
	}
	return pooledCell[C, P](s)
}

// replace puts c in the slot cellAt just returned, for a part whose previous cell must not be filled again.
func (s *scratch) replace(c cell) { s.pool[s.next-1] = c }

func pooledCell[C any, P interface {
	*C
	cell
}](s *scratch) P {
	slot := s.next
	s.next++
	if slot < len(s.pool) {
		if c, ok := s.pool[slot].(P); ok {
			return c
		}
		c := P(new(C))
		s.pool[slot] = c
		return c
	}
	c := P(new(C))
	s.pool = append(s.pool, c)
	return c
}

// mode says where a reader is: inside a group, and in a declaration pass of Embed or
// EmbedNull, where the targets that receive a driver-allocated value are checked behind
// the dests that lead to them. A bit set, to keep reader at eight words.
type mode uint8

const (
	inGroup mode = 1 << iota
	declaring
)

// reader is the row being assembled. It passes by value, because a pointer through part
// func values would escape, and it is passed through every part of every row, so its
// size is the cost of a flat Column: measured on arm64, a ninth word adds about a tenth
// to a three-column Scan. That is why the scratch travels beside it and mode is one field.
type reader struct {
	dests []any
	// cells holds this row's cells in the order they finish. The first assigned of them
	// are those that assign a column after the scan: every ColumnOrZero, and a group's
	// Columns. They finish before the rest, so set and convert see every column of their
	// row, as they see a flat Column, which the driver writes.
	cells    []cell
	assigned int
	mode     mode
}

// list returns an empty cell list with room for capacity cells, taken from the arena
// when s is not nil and it has room. capacity is a hint: a list that outgrows it is
// reallocated by append.
func (s *scratch) list(capacity int) []cell {
	if s != nil {
		s.need += capacity
		if start := len(s.arena); capacity <= cap(s.arena)-start {
			s.arena = s.arena[:start+capacity]
			return s.arena[start : start : start+capacity]
		}
	}
	return make([]cell, 0, capacity)
}

// assign adds c after the assigning cells already in the list and ahead of the rest,
// which move up one place. Order within each kind stays creation order, whatever the
// declaration interleaves.
func (r reader) assign(c cell) reader {
	r.cells = append(r.cells, c)
	copy(r.cells[r.assigned+1:], r.cells[r.assigned:])
	r.cells[r.assigned] = c
	r.assigned++
	return r
}

// part adds one declaration's destinations and cells for the row read into d.
type part[D any] func(d *D, r reader, reuse *scratch) reader

// Projection is an ordered, immutable list of result columns bound to fields of D.
// The zero Projection is empty.
type Projection[D any] struct {
	expressions []qs.Expr
	parts       []part[D]
	// cells is how many cells the parts add outside a group. It sizes the list, so a
	// wrong count costs an allocation, not correctness.
	cells int
}

// scanner mirrors database/sql.Scanner without importing it.
type scanner interface{ Scan(src any) error }

// requiredCell lets a group tolerate NULL in a Column until presence is known.
type requiredCell[T any] struct {
	value      *T
	dest       *T
	expression *qs.Expr
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
	expression *qs.Expr
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
	if problem := targetProblem(target, again); problem != "" {
		panic(label() + ": " + problem + " or implement Scan(any) error")
	}
}

// targetProblem names why a value the driver allocated cannot be assigned through
// target, or returns "": see checkGroupTarget.
func targetProblem[T any](target, again *T) string {
	var zero T
	switch {
	case target != again:
		return fmt.Sprintf("dest for %T must return a stable address", zero)
	case fmt.Sprintf("%#v", *target) != fmt.Sprintf("%#v", zero):
		return fmt.Sprintf("dest for %T must return a zero target", zero)
	}
	return ""
}

// Column reads one required result column into the address dest returns, as
// rows.Scan(dest(&d)) does outside a group. dest returns a field of its argument, or a
// Scanner writing into it. It runs on a zero D at declaration and once per Scan, before
// the row is read, so it must be deterministic and must not return nil. It may allocate
// a nil pointer but must not replace a value already set; put nested pointers behind
// Embed's dest. Inside EmbedNull, NULL in a present group is ErrNull whatever T
// is; use ColumnNull or ColumnOrZero for nullable columns. Column panics if dest is nil
// or returns nil.
func Column[D, T any](expression qs.Expr, dest func(*D) *T) Projection[D] {
	if dest == nil {
		panic("projection: Column with nil dest")
	}
	var zero D
	if dest(&zero) == nil {
		panic(fmt.Sprintf("projection: Column dest returned nil for a zero %T", zero))
	}
	return Projection[D]{expressions: []qs.Expr{expression}, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		target := dest(d)
		if r.mode&inGroup == 0 {
			r.dests = append(r.dests, target)
			return r
		}
		if s, ok := any(target).(scanner); ok {
			c := cellAt[scannerCell](reuse)
			*c = scannerCell{target: s, position: len(r.dests), expression: &expression}
			r.dests = append(r.dests, c)
			return r.assign(c)
		}
		if r.mode&declaring != 0 {
			checkGroupTarget(target, dest(d), len(r.dests), expression)
		}
		c := cellAt[requiredCell[T]](reuse)
		*c = requiredCell[T]{dest: target, position: len(r.dests), expression: &expression}
		r.dests = append(r.dests, &c.value)
		return r.assign(c)
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
// set with v after the row is read: nil on NULL. set finds every Column and ColumnOrZero
// of its row already assigned, wherever each is declared; set and convert calls run in
// declaration order. See EmbedNull for what such a read requires of T. A write-through
// Scanner cannot be used here, because the driver allocates T. ColumnNull panics if set
// is nil or T is detected as unreadable that way.
//
// A set that does nothing reads a column no field holds, such as a join key that only
// decides whether an EmbedNull group is present.
func ColumnNull[D, T any](expression qs.Expr, set func(*D, *T)) Projection[D] {
	if set == nil {
		panic("projection: ColumnNull with nil set")
	}
	if problem := indirectReadProblem[T](); problem != "" {
		panic("projection: ColumnNull: " + problem)
	}
	return Projection[D]{expressions: []qs.Expr{expression}, cells: 1, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		c := cellAt[nullCell[D, T]](reuse)
		*c = nullCell[D, T]{d: d, set: set}
		r.dests, r.cells = append(r.dests, &c.value), append(r.cells, c)
		return r
	}}}
}

type zeroCell[T any] struct {
	value *T
	dest  *T
}

func (c *zeroCell[T]) null() bool { return c.value == nil }
func (c *zeroCell[T]) finish() error {
	if c.value == nil {
		// Assigned, not skipped: under ScanInto the target may hold the previous row.
		var zero T
		*c.dest = zero
		return nil
	}
	*c.dest = *c.value
	return nil
}

// ColumnOrZero reads one nullable result column as ColumnNull does and assigns it to
// the address dest returns: the zero T on NULL. Use it where the row does not tell NULL
// from zero, or where T represents NULL itself, as sql.NullString does. NULL is never
// ErrNull, and inside EmbedNull it counts as NULL for the group's presence.
//
// dest follows Column's rules. The driver allocates the T that is then copied to the
// address dest returned, so T must decode from its zero value and a write-through
// Scanner cannot be used: see EmbedNull. As a check on that, the address dest returns
// for a zero D, directly and behind the dests of enclosing Embeds, must be stable and
// hold a zero value. The assignment happens after the row is read, before any set or
// convert runs. ColumnOrZero panics if dest is nil or returns nil, on such a target, or
// if T is detected as unreadable through a pointer.
func ColumnOrZero[D, T any](expression qs.Expr, dest func(*D) *T) Projection[D] {
	if dest == nil {
		panic("projection: ColumnOrZero with nil dest")
	}
	if problem := indirectReadProblem[T](); problem != "" {
		panic("projection: ColumnOrZero: " + problem)
	}
	var zero D
	target := dest(&zero)
	if target == nil {
		panic(fmt.Sprintf("projection: ColumnOrZero dest returned nil for a zero %T", zero))
	}
	if problem := targetProblem(target, dest(&zero)); problem != "" {
		panic("projection: ColumnOrZero: " + problem)
	}
	return Projection[D]{expressions: []qs.Expr{expression}, cells: 1, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		c := cellAt[zeroCell[T]](reuse)
		*c = zeroCell[T]{dest: dest(d)}
		if r.mode&declaring != 0 {
			// d now comes from an enclosing Embed's dest, which may have prepared the target.
			if problem := targetProblem(c.dest, dest(d)); problem != "" {
				panic("projection: ColumnOrZero: " + columnLabel(len(r.dests), expression) + ": " + problem)
			}
		}
		r.dests = append(r.dests, &c.value)
		return r.assign(c)
	}}}
}

// Embed reads inner into the value dest returns. dest runs on a zero D at declaration
// and once per Scan, before the row is read, so a nil pointer inside D must be
// allocated by dest. Allocate only if nil: Embeds sharing a pointer would otherwise
// each replace the last, losing its columns. inner's dests, those of nested
// declarations included, then receive what dest returned, at declaration too, where a
// ColumnOrZero target is checked. Embed panics if dest is nil or returns nil.
func Embed[D, N any](inner Projection[N], dest func(*D) *N) Projection[D] {
	if dest == nil {
		panic("projection: Embed with nil dest")
	}
	var zero D
	row := dest(&zero)
	if row == nil {
		panic(fmt.Sprintf("projection: Embed dest returned nil for a zero %T", zero))
	}
	// Every dest inside inner runs on the row it receives at Scan; a ColumnOrZero checks
	// its target there.
	declared := reader{mode: declaring}
	for _, p := range inner.parts {
		declared = p(row, declared, nil)
	}
	// One part, not one per column: an allocating dest must not split a row.
	return Projection[D]{expressions: inner.expressions, cells: inner.cells, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		row := dest(d)
		for _, p := range inner.parts {
			r = p(row, r, reuse)
		}
		return r
	}}}
}

type groupCell[D, N any] struct {
	row   N
	d     *D
	set   func(*D, *N)
	cells []cell
	// present marks a row whose group was present: its columns were assigned and sets
	// received it, so the caller may hold it and the cell is not filled again.
	present bool
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
	// Before any inner cell finishes: an inner set may keep the row even if a later cell fails.
	c.present = true
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
// indistinguishable from no match. Otherwise a NULL Column is ErrNull, a ColumnNull
// receives nil and a ColumnOrZero assigns zero. Under an absent group Map runs neither
// convert nor set.
//
// A present group assigns its Columns and ColumnOrZeros, then runs the set and convert
// calls declared inside it in declaration order, then calls set. That call takes the group's place
// in the enclosing declaration's order, and finds the enclosing row's Columns assigned.
//
// Inside a group, at any depth, a Column whose T is not a Scanner reads as
// var v *T; rows.Scan(&v) does, with v == nil meaning NULL, and then assigns *v through
// dest. ColumnNull and ColumnOrZero read the same way everywhere, whatever T is. Such a
// read has that read's limits on each driver: T must decode from its zero value,
// because the driver allocates it, so a dest must return a stable address holding a
// zero value; T must own its memory, because the driver's check for borrowed types
// sees only **T; and presence is what the driver reports, so a non-NULL value it
// decodes to nil counts as NULL. Which values do depends on driver and T: pgx v5.11
// does for 'null'::jsonb into a string or a map.
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
	declared := reader{mode: inGroup | declaring}
	for _, p := range inner.parts {
		declared = p(&row, declared, nil)
	}
	capacity := len(declared.cells)
	return Projection[D]{expressions: inner.expressions, cells: 1, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		c := cellAt[groupCell[D, N]](reuse)
		if c.present {
			// Only a pooled cell can be present here. An absent group's row reached no set,
			// so it is filled again with whatever its dests allocated inside it; a present
			// one may be held by the caller.
			c = new(groupCell[D, N])
			reuse.replace(c)
		}
		c.d, c.set = d, set
		group := reader{dests: r.dests, mode: inGroup, cells: reuse.list(capacity)}
		for _, p := range inner.parts {
			group = p(&c.row, group, reuse)
		}
		c.cells = group.cells
		r.dests, r.cells = group.dests, append(r.cells, c)
		return r
	}}}
}

type mapCell[D, B, T any] struct {
	row     B
	d       *D
	convert func(B) (T, error)
	set     func(*D, T)
}

// null is always true: a conversion proves nothing about presence.
func (c *mapCell[D, B, T]) null() bool { return true }
func (c *mapCell[D, B, T]) finish() error {
	value, err := c.convert(c.row)
	if err != nil {
		return err
	}
	c.set(c.d, value)
	return nil
}

// Map reads inner into a value of its own, a new zero B for each row, and after the row
// is read calls convert on it and passes the result to set, which assigns it into D.
// Nothing inner reads reaches D except through set. Map assigns nothing itself, so what
// other declarations read into the same D survives as far as set leaves it: a set that
// assigns a field keeps the rest, and one that assigns the whole value, as
// func(d *T, v T) { *d = v } does, replaces everything read into it before set ran,
// Columns included, without an error. set and convert calls run in declaration order,
// after every Column and ColumnOrZero of the row is assigned. If convert fails, set does
// not run and the error is returned unchanged, without column context. convert runs
// synchronously inside Scan while the result set is open and receives no context, so it
// must be CPU-only and safe for concurrent use. Map panics if convert or set is nil.
func Map[D, B, T any](inner Projection[B], convert func(B) (T, error), set func(*D, T)) Projection[D] {
	if convert == nil {
		panic("projection: Map with nil convert")
	}
	if set == nil {
		panic("projection: Map with nil set")
	}
	return Projection[D]{expressions: inner.expressions, cells: inner.cells + 1, parts: []part[D]{func(d *D, r reader, reuse *scratch) reader {
		// Never taken from the scratch: inner sets receive &c.row and may keep it, as a
		// child's pointer to its parent does.
		c := &mapCell[D, B, T]{d: d, convert: convert, set: set}
		for _, p := range inner.parts {
			r = p(&c.row, r, reuse)
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
// value of D. scan receives the destinations ScanInto describes. An error from scan is
// returned unchanged. On any error Scan returns the zero D.
func (p Projection[D]) Scan(scan func(dest ...any) error) (D, error) {
	var d D
	if err := p.ScanInto(&d, scan); err != nil {
		var zero D
		return zero, err
	}
	return d, nil
}

// ScanInto reads one row through scan into d, as Scan does into a new D. Use it for a
// value the caller already holds: one a constructor prepared, the entity a RETURNING
// clause completes, or a D that must not be copied. Fields the projection does not
// declare keep their values, and dests receive d as it is, so an Embed dest that
// allocates only if nil reads into the value already there.
//
// scan receives one destination per expression, in Expressions order. It may pass
// further destinations of its own after them, to read output the query selects after
// Expressions that belongs to no row, such as a window count:
//
//	err := p.ScanInto(&d, func(dest ...any) error { return row.Scan(append(dest, &total)...) })
//
// An error from scan is returned unchanged. On any error d may be partly written.
//
// d may be reused for the next row. Each call writes or calls back for every declared
// column, passing nil for NULL and for an absent group, so a set that ignores nil
// leaves the previous row's value; ColumnOrZero assigns zero. Memory reachable from d is reused with it: a copy
// of *d kept from an earlier row still shares any pointer a dest returned without
// allocating, such as an Embed field allocated only if nil. Read each row that is kept
// into its own D.
//
// ScanInto panics if d is nil.
func (p Projection[D]) ScanInto(d *D, scan func(dest ...any) error) error {
	if d == nil {
		panic("projection: ScanInto with nil destination")
	}
	return p.read(d, scan, nil)
}

// read is ScanInto. With reused, it takes the destination list, cell lists and cells
// from the previous row of the same result set instead of allocating them, so scan
// must not keep dest after it returns.
func (p Projection[D]) read(d *D, scan func(dest ...any) error, reused *scratch) error {
	var r reader
	if reused != nil {
		r.dests = reused.dests[:0]
		reused.next, reused.need, reused.arena = 0, 0, reused.arena[:0]
	}
	if r.dests == nil {
		r.dests = make([]any, 0, len(p.expressions))
	}
	if p.cells > 0 {
		r.cells = reused.list(p.cells)
	}
	for _, q := range p.parts {
		r = q(d, r, reused)
	}
	if reused != nil {
		reused.dests = r.dests
		if reused.need > cap(reused.arena) {
			reused.arena = make([]cell, 0, reused.need)
		}
	}
	if err := scan(r.dests...); err != nil {
		return err
	}
	for _, c := range r.cells {
		if err := c.finish(); err != nil {
			return err
		}
	}
	return nil
}
