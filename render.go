package qx

import "strconv"

// Statement is a complete query. It is sealed to keep rendering and parameter
// ownership in one place. Use StatementSQL for trusted extensions.
type Statement interface {
	ToSQL() (string, []any, error)
	ToSQLWith(options Options) (string, []any, error)
	AppendSQL(sql []byte, args []any) ([]byte, []any, error)
	AppendWith(sql []byte, args []any, options Options) ([]byte, []any, error)
	statement()
}

// Rowset can occur in an ordinary subquery: SELECT, VALUES, TABLE, or a set
// operation. DML with RETURNING is a Statement, not a Rowset; use a CTE instead.
type Rowset interface {
	Statement
	rowset()
}

// PlaceholderStyle selects parameter syntax without rewriting SQL text.
// It does not change the PostgreSQL dialect or the argument order.
type PlaceholderStyle uint8

const (
	Dollar   PlaceholderStyle = iota // $1, $2, ...; the PostgreSQL default.
	Question                         // ?, ?, ...; requires a consumer supporting this format.
)

// PostgreSQLVersion selects the major release used for feature validation.
// Zero selects PostgreSQL18; other values must be one of the named releases.
type PostgreSQLVersion int

// Supported PostgreSQL major releases for feature validation.
const (
	PostgreSQL12 PostgreSQLVersion = 12 + iota
	PostgreSQL13
	PostgreSQL14
	PostgreSQL15
	PostgreSQL16
	PostgreSQL17
	PostgreSQL18
)

func (v PostgreSQLVersion) valid() bool { return v >= PostgreSQL12 && v <= PostgreSQL18 }

// Options changes parameter syntax, rendering limits and feature validation.
// Zero fields select Dollar, PostgreSQL 18, 65535 parameters and 256 nesting levels.
// A supported version does not guarantee that referenced schema objects exist.
type Options struct {
	PlaceholderStyle PlaceholderStyle
	PostgreSQL       PostgreSQLVersion
	MaxParameters    int
	MaxDepth         int
}

type renderer struct {
	sql            []byte
	args           []any
	err            error
	options        Options
	depth          int
	statementDepth int
}

func normalOptions(o Options) Options {
	if o.PostgreSQL == 0 {
		o.PostgreSQL = PostgreSQL18
	}
	if o.MaxParameters == 0 {
		o.MaxParameters = 65535
	}
	if o.MaxDepth == 0 {
		o.MaxDepth = 256
	}
	return o
}

// ToSQL renders s using default options. On error it returns no SQL or args.
func ToSQL(s Statement) (string, []any, error) { return ToSQLWith(s, Options{}) }

// ToSQLWith renders an owned result with explicit options.
func ToSQLWith(s Statement, options Options) (string, []any, error) {
	// A small initial buffer avoids repeated growth for common queries. The
	// owned string is copied out and never aliases reusable storage.
	var scratch [256]byte
	buf, args, err := AppendWith(scratch[:0], nil, s, options)
	if err != nil {
		return "", nil, err
	}
	return string(buf), args, nil
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

// AppendWith is AppendSQL with explicit parameter syntax, limits and feature validation.
func AppendWith(sql []byte, args []any, s Statement, options Options) ([]byte, []any, error) {
	w := renderer{sql: sql, args: args, options: normalOptions(options)}
	if w.options.PlaceholderStyle > Question {
		w.fail(ErrInvalid, "options", "unknown placeholder style")
	} else if !w.options.PostgreSQL.valid() || w.options.MaxParameters < 1 || w.options.MaxParameters > 65535 || w.options.MaxDepth < 1 || w.options.MaxDepth > 4096 {
		w.fail(ErrInvalid, "options", "require PostgreSQL 12..18, 1..65535 parameters and 1..4096 nesting levels")
	} else if len(args) > w.options.MaxParameters {
		w.fail(ErrParameterLimit, "parameters", "existing argument prefix exceeds the limit")
	} else {
		w.statement(s)
	}
	if w.err != nil {
		clear(w.args[len(args):])
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
func (w *renderer) feature(version PostgreSQLVersion, name string) {
	if w.options.PostgreSQL < version {
		w.fail(ErrUnsupported, name, "not available in the configured PostgreSQL version")
	}
}
func (w *renderer) enter() bool {
	if w.err != nil {
		return false
	}
	if w.depth >= w.options.MaxDepth {
		w.fail(ErrDepth, "structure", "check for a builder containing itself or increase MaxDepth")
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
	if w.options.PlaceholderStyle == Dollar {
		w.byte('$')
		w.sql = strconv.AppendInt(w.sql, int64(len(w.args)), 10)
	} else {
		w.byte('?')
	}
}

// Concrete dispatch keeps renderer pointers out of public interfaces; this is
// significant for escape analysis of the warm, reusable-buffer path.
func (w *renderer) statement(s Statement) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	w.statementDepth++
	defer func() { w.statementDepth-- }()
	switch b := s.(type) {
	case *SelectBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil SELECT")
			return
		}
		b.append(w)
	case *InsertBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil INSERT")
			return
		}
		b.append(w)
	case *UpdateBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil UPDATE")
			return
		}
		b.append(w)
	case *DeleteBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil DELETE")
			return
		}
		b.append(w)
	case *MergeBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil MERGE")
			return
		}
		b.append(w)
	case *SetBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil set operation")
			return
		}
		b.append(w)
	case *ValuesBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil VALUES")
			return
		}
		b.append(w)
	case *TableBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil TABLE")
			return
		}
		b.append(w)
	case *ExplainBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil EXPLAIN")
			return
		}
		b.append(w)
	case *TruncateBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil TRUNCATE")
			return
		}
		b.append(w)
	case *ExecuteBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil EXECUTE")
			return
		}
		b.append(w)
	case *CreateTableAsBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil CREATE TABLE AS")
			return
		}
		b.append(w)
	case *MaterializedViewBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil CREATE MATERIALIZED VIEW")
			return
		}
		b.append(w)
	case *DeclareCursorBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil DECLARE CURSOR")
			return
		}
		b.append(w)
	case *SelectIntoBuilder:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil SELECT INTO")
			return
		}
		b.append(w)
	case *SQLStatement:
		if b == nil {
			w.fail(ErrInvalid, "statement", "nil SQL statement")
			return
		}
		w.expr(b.expr)
	default:
		w.fail(ErrInvalid, "statement", "nil or unknown statement")
	}
}

// appendTransparentStatement renders a utility statement's query at the
// same statement level as its enclosing command. PostgreSQL permits a
// data-modifying CTE in a top-level CREATE TABLE AS or SELECT INTO query, but
// ordinary statement dispatch must still account for physical nesting depth.
func (w *renderer) appendTransparentStatement(s Statement) {
	if w.statementDepth > 0 {
		w.statementDepth--
		defer func() { w.statementDepth++ }()
	}
	w.statement(s)
}

// ExprToSQL renders a standalone expression; useful for testing reusable parts.
func ExprToSQL(expr Expr) (string, []any, error) {
	return ToSQL(StatementSQL(expr))
}
