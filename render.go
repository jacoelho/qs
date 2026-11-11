package qs

import (
	"errors"
	"slices"
	"strconv"
)

// Statement describes a SQL statement; it does not prove that its inputs are valid.
// Rendering checks structure and returns an error for invalid input. PostgreSQL
// owns schema and SQL type validation. The interface is sealed to keep rendering
// and parameter ownership in one place. Use StatementSQL for trusted extensions.
type Statement interface {
	// ToSQL renders owned SQL and arguments with default options.
	ToSQL() (string, []any, error)
	// ToSQLWith renders owned SQL and arguments with per-call options.
	ToSQLWith(options Options) (string, []any, error)
	// AppendSQL appends into caller storage with default options and prefix numbering.
	AppendSQL(sql []byte, args []any) ([]byte, []any, error)
	// AppendWith appends with per-call options, preserving visible prefixes on error.
	AppendWith(sql []byte, args []any, options Options) ([]byte, []any, error)
	statement()
}

// Rowset can occur in an ordinary subquery: SELECT, VALUES, TABLE, or a set
// operation. DML with RETURNING is a Statement, not a Rowset; use a CTE instead.
type Rowset interface {
	Statement
	rowset()
}

// Options changes rendering limits. Zero fields select 65535 parameters and
// 256 nesting levels.
type Options struct {
	// MaxParameters bounds total arguments including append prefixes; zero selects 65535.
	MaxParameters int
	// MaxDepth bounds nested traversal; zero selects 256 and valid nonzero values are 1..4096.
	MaxDepth int
}

type renderer struct {
	err error
	// The SharedParams this render has numbered: the first few inline, so a
	// warm render of a keyset page allocates nothing, and the rest in spill. A
	// statement rarely shares more than a few, so a scan beats a map.
	shared         [4]sharedSlot
	sql            []byte
	args           []any
	sharedSpill    []sharedSlot
	sharedCount    int
	options        Options
	depth          int
	statementDepth int
	hasWindow      bool // Structural OVER encountered in the current statement.
}

type sharedSlot struct {
	parameter *sharedParameter
	number    int
}

func normalOptions(o Options) Options {
	if o.MaxParameters == 0 {
		o.MaxParameters = 65535
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = 256
	}
	return o
}

// ownedStatement returns the statement that qs keeps or renders for s. A
// pointer to a builder satisfies Statement through the builder's value
// methods; it is replaced here, where it enters, by the builder it points to
// at that moment, and a nil pointer by a nil statement. Everything stored is
// therefore a value: it cannot change after construction, and the switches
// over statements see one form of each builder.
func ownedStatement(s Statement) Statement {
	if value, pointer := statementPointee(s); pointer {
		return value
	}
	return s
}

// ownedRowset is ownedStatement for a rowset.
func ownedRowset(r Rowset) Rowset {
	value, pointer := statementPointee(r)
	if !pointer {
		return r
	}
	// A nil pointer has no builder, and so no rowset.
	if rowset, ok := value.(Rowset); ok {
		return rowset
	}
	return nil
}

// ToSQL renders s using default options. On error it returns no SQL or args.
func ToSQL(s Statement) (string, []any, error) { return ToSQLWith(s, Options{}) }

// ToSQLWith renders an owned result with explicit options.
func ToSQLWith(s Statement, options Options) (string, []any, error) {
	var scratch [ownedSQLScratch]byte
	return ownedSQL(AppendWith(scratch[:0], nil, s, options))
}

// ownedSQLScratch is the initial buffer of a render that returns a string. It
// avoids repeated growth for common queries.
const ownedSQLScratch = 256

// ownedSQL copies a rendered buffer into a string, so the result never aliases
// reusable storage. On error it returns no SQL or args.
func ownedSQL(sql []byte, args []any, err error) (string, []any, error) {
	if err != nil {
		return "", nil, err
	}
	return string(sql), args, nil
}

// MustToSQL is intended for tests and static statements; it panics on errors.
func MustToSQL(s Statement) (string, []any) {
	query, args, err := ToSQL(s)
	if err != nil {
		panic(err)
	}
	return query, args
}

// AppendSQL appends s to caller-owned storage. Parameter numbering starts at
// len(args)+1. On failure the original slices, lengths and prefix contents are
// returned unchanged. Their unused capacity is not preserved.
func AppendSQL(sql []byte, args []any, s Statement) ([]byte, []any, error) {
	return AppendWith(sql, args, s, Options{})
}

// AppendWith is AppendSQL with explicit rendering limits.
func AppendWith(sql []byte, args []any, s Statement, options Options) ([]byte, []any, error) {
	w, ok := newRenderer(sql, args, options)
	if ok {
		w.statement(ownedStatement(s))
	}
	return w.result(sql, args)
}

// newRenderer starts a render into sql and args. It reports false, with the
// failure recorded, when the options or the argument prefix are invalid.
func newRenderer(sql []byte, args []any, options Options) (renderer, bool) {
	w := renderer{sql: sql, args: args, options: normalOptions(options)}
	switch {
	case w.options.MaxParameters < 1 || w.options.MaxParameters > 65535 || w.options.MaxDepth < 1 || w.options.MaxDepth > 4096:
		w.fail(ErrInvalid, "options", "require 1..65535 parameters and 1..4096 nesting levels")
	case len(args) > w.options.MaxParameters:
		w.fail(ErrParameterLimit, "parameters", "existing argument prefix exceeds the limit")
	}
	return w, w.err == nil
}

// result returns the rendered slices, or the caller's sql and args unchanged
// when rendering failed.
func (w *renderer) result(sql []byte, args []any) ([]byte, []any, error) {
	if w.err != nil {
		// Binding only appends to the argument slice, so the final length bounds
		// every slot that rendering could have written in the original array.
		clear(args[len(args):min(len(w.args), cap(args))])
		clear(w.args[len(args):])
		if detail, ok := errors.AsType[*RenderError](w.err); ok {
			slices.Reverse(detail.Path)
		}
		return sql, args, w.err
	}
	return w.sql, w.args, nil
}

func (w *renderer) text(s string) { w.sql = append(w.sql, s...) }
func (w *renderer) byte(b byte)   { w.sql = append(w.sql, b) }
func (w *renderer) fail(cause error, clause, detail string) {
	if w.err == nil {
		w.err = &RenderError{Clause: clause, Detail: detail, Cause: cause}
	}
}
func (w *renderer) require(ok bool, clause, detail string) bool {
	if !ok {
		w.fail(ErrInvalid, clause, detail)
	}
	return ok
}
func (w *renderer) enter() bool {
	if w.err != nil {
		return false
	}
	if w.depth >= w.options.MaxDepth {
		w.fail(ErrDepth, "structure", "nesting exceeds MaxDepth; flatten the query or raise the limit")
		return false
	}
	w.depth++
	return true
}
func (w *renderer) bind(value any) {
	if w.err != nil {
		return
	}
	if len(w.args) >= w.options.MaxParameters {
		w.fail(ErrParameterLimit, "parameters", "use fewer parameters, an array argument, or a separate bulk-loading operation")
		return
	}
	if w.args == nil {
		w.args = make([]any, 0, 8)
	}
	w.args = append(w.args, value)
	w.byte('$')
	w.sql = strconv.AppendInt(w.sql, int64(len(w.args)), 10)
}

// bindShared binds a SharedParam at its first use in this render and repeats
// the number it took at each later one, which adds no argument.
func (w *renderer) bindShared(e Expr) {
	p, ok := e.value.(*sharedParameter)
	if !w.require(ok, "expression", "zero expression") {
		return
	}
	number := 0
	for i := range min(w.sharedCount, len(w.shared)) {
		if w.shared[i].parameter == p {
			number = w.shared[i].number
		}
	}
	for _, slot := range w.sharedSpill {
		if slot.parameter == p {
			number = slot.number
		}
	}
	if number != 0 {
		if w.err == nil {
			w.byte('$')
			w.sql = strconv.AppendInt(w.sql, int64(number), 10)
		}
		return
	}
	w.bind(p.value)
	if w.err != nil {
		return
	}
	slot := sharedSlot{parameter: p, number: len(w.args)}
	if w.sharedCount < len(w.shared) {
		w.shared[w.sharedCount] = slot
	} else {
		w.sharedSpill = append(w.sharedSpill, slot)
	}
	w.sharedCount++
}

// Concrete dispatch keeps renderer pointers out of public interfaces; this is
// significant for escape analysis of the warm, reusable-buffer path.
func (w *renderer) statement(s Statement) {
	if w.err != nil {
		return
	}
	if outerHasWindow, ok := w.openStatement(); ok {
		w.dispatchStatement(s)
		w.closeStatement(outerHasWindow)
	}
	if w.err != nil {
		w.errorPath(statementLabel(s), 0)
	}
}

// openStatement enters a nested statement scope, which owns its own window
// function state. A successful open must be paired with closeStatement.
func (w *renderer) openStatement() (outerHasWindow, ok bool) {
	if !w.enter() {
		return false, false
	}
	outerHasWindow = w.hasWindow
	w.hasWindow = false
	w.statementDepth++
	return outerHasWindow, true
}

func (w *renderer) closeStatement(outerHasWindow bool) {
	w.statementDepth--
	w.hasWindow = outerHasWindow
	w.depth--
}

// sqlStatement renders a trusted statement.
func (w *renderer) sqlStatement(b *SQLStatement) { w.expr(b.expr) }

// ExprToSQL renders a standalone expression; useful for testing reusable parts.
func ExprToSQL(expr Expr) (string, []any, error) {
	return ToSQL(StatementSQL(expr))
}
