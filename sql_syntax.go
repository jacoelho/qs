package qx

// sqlSyntaxKind identifies one of PostgreSQL's grammar productions that looks
// like a function call but is not an ordinary function invocation.  The
// renderer owns the spelling of each production; callers cannot supply a
// function name or SQL keyword through this payload.
type sqlSyntaxKind uint8

const (
	sqlSubstringFrom sqlSyntaxKind = iota + 1
	sqlSubstringFor
	sqlSubstringSimilar
	sqlPosition
	sqlNormalize
	sqlOverlay
	sqlOverlaps
	sqlAtLocal
	sqlTrim
	sqlCollationFor
	sqlIsNormalized
	sqlValueFunction
)

// UnicodeNormalForm is one of PostgreSQL's four Unicode normalization forms.
// The zero value is intentionally invalid: it represents the omitted form in
// a syntax payload only when hasForm is false.
type UnicodeNormalForm uint8

const (
	NFC UnicodeNormalForm = iota + 1
	NFD
	NFKC
	NFKD
)

func (f UnicodeNormalForm) valid() bool { return f >= NFC && f <= NFKD }

// TrimDirection controls the SQL TRIM grammar production.
type TrimDirection uint8

const (
	TrimBothDirection TrimDirection = iota + 1
	TrimLeadingDirection
	TrimTrailingDirection
)

func (d TrimDirection) valid() bool {
	return d >= TrimBothDirection && d <= TrimTrailingDirection
}

type sqlValueKind uint8

const (
	sqlCurrentTime sqlValueKind = iota + 1
	sqlCurrentTimestamp
	sqlLocalTime
	sqlLocalTimestamp
	sqlSessionUser
	sqlCurrentSchema
	sqlCurrentCatalog
)

// sqlSyntaxExpression is the complete payload for exprSQLSyntax.  args is
// always an owned slice of child expressions.  The remaining fields are
// closed enums or validated scalar grammar values; no caller-provided SQL
// spelling is retained here.
//
// The clone walker must copy args recursively with cloneContext.exprs and then
// copy this payload before replacing it in the cloned Expr.  The renderer must
// traverse args through the current renderer so nested parameters, statements,
// depth limits and placeholder styles remain part of the outer render.
type sqlSyntaxExpression struct {
	kind      sqlSyntaxKind
	args      []Expr
	form      UnicodeNormalForm
	hasForm   bool
	direction TrimDirection
	negated   bool
	value     sqlValueKind
	precision int
	hasPrec   bool
}

func newSQLSyntax(kind sqlSyntaxKind, args []Expr) Expr {
	return Expr{kind: exprSQLSyntax, value: &sqlSyntaxExpression{kind: kind, args: cloneSlice(args)}}
}

func newSQLSyntaxWithForm(kind sqlSyntaxKind, args []Expr, form UnicodeNormalForm) Expr {
	if !form.valid() {
		return invalidExpr("Unicode normalization", "unknown normalization form")
	}
	return Expr{kind: exprSQLSyntax, value: &sqlSyntaxExpression{
		kind:    kind,
		args:    cloneSlice(args),
		form:    form,
		hasForm: true,
	}}
}

func normalizeForm(form []UnicodeNormalForm) (UnicodeNormalForm, bool, Expr) {
	if len(form) > 1 {
		return 0, false, invalidExpr("Unicode normalization", "at most one normalization form is accepted")
	}
	if len(form) == 0 {
		return 0, false, Expr{}
	}
	if !form[0].valid() {
		return 0, false, invalidExpr("Unicode normalization", "unknown normalization form")
	}
	return form[0], true, Expr{}
}

// SubstringFrom emits PostgreSQL's SQL substring grammar:
// SUBSTRING(value FROM start [FOR length]).  It is separate from Substring,
// which intentionally retains its ordinary substr(value, start [, length])
// function semantics.
func SubstringFrom(value, start Expr, length ...Expr) Expr {
	if len(length) > 1 {
		return invalidExpr("SUBSTRING", "at most one length is accepted")
	}
	args := []Expr{value, start}
	args = append(args, length...)
	return newSQLSyntax(sqlSubstringFrom, args)
}

// SubstringFor emits PostgreSQL's extension form SUBSTRING(value FOR length).
// PostgreSQL interprets this as a substring starting at one.
func SubstringFor(value, length Expr) Expr {
	return newSQLSyntax(sqlSubstringFor, []Expr{value, length})
}

// SubstringSimilar emits SQL's regular-expression form
// SUBSTRING(value SIMILAR pattern ESCAPE escape).
func SubstringSimilar(value, pattern, escape Expr) Expr {
	return newSQLSyntax(sqlSubstringSimilar, []Expr{value, pattern, escape})
}

// Position emits POSITION(substring IN value).  PostgreSQL's parser maps the
// operands to position(value, substring) internally; keeping the SQL order in
// this API avoids exposing that implementation detail.
func Position(substring, value Expr) Expr {
	return newSQLSyntax(sqlPosition, []Expr{substring, value})
}

// Normalize emits NORMALIZE(value) or NORMALIZE(value, form).
func Normalize(value Expr, form ...UnicodeNormalForm) Expr {
	f, hasForm, invalid := normalizeForm(form)
	if invalid.value != nil {
		return invalid
	}
	if !hasForm {
		return newSQLSyntax(sqlNormalize, []Expr{value})
	}
	return newSQLSyntaxWithForm(sqlNormalize, []Expr{value}, f)
}

// Overlay emits OVERLAY(value PLACING replacement FROM start [FOR length]).
func Overlay(value, replacement, start Expr, length ...Expr) Expr {
	if len(length) > 1 {
		return invalidExpr("OVERLAY", "at most one length is accepted")
	}
	args := []Expr{value, replacement, start}
	args = append(args, length...)
	return newSQLSyntax(sqlOverlay, args)
}

// Overlaps emits the SQL OVERLAPS predicate for two two-element endpoint
// pairs.  The four-argument form makes the required pair arity explicit and
// avoids silently accepting a row of the wrong width.
func Overlaps(leftStart, leftEnd, rightStart, rightEnd Expr) Condition {
	return AsCondition(newSQLSyntax(sqlOverlaps, []Expr{leftStart, leftEnd, rightStart, rightEnd}))
}

// AtLocal emits value AT LOCAL.  AtTimeZone remains the existing expression
// helper for the value-bearing AT TIME ZONE form.
func AtLocal(value Expr) Expr { return newSQLSyntax(sqlAtLocal, []Expr{value}) }

// AtLocal is also available as a method for parity with AtTimeZone.
func (e Expr) AtLocal() Expr { return AtLocal(e) }

// TrimSyntax emits the SQL TRIM grammar.  The character expression is
// optional; when omitted PostgreSQL trims whitespace.  The existing Trim
// helper remains the ordinary btrim(value) function.
func TrimSyntax(value Expr, direction TrimDirection, characters ...Expr) Expr {
	if !direction.valid() {
		return invalidExpr("TRIM", "unknown trim direction")
	}
	if len(characters) > 1 {
		return invalidExpr("TRIM", "at most one trim character expression is accepted")
	}
	args := []Expr{value}
	if len(characters) == 1 {
		// Keep traversal and parameter order equal to the SQL grammar: the
		// optional trim character appears before the value in TRIM(... FROM ...).
		args = []Expr{characters[0], value}
	}
	e := newSQLSyntax(sqlTrim, args)
	n := *e.value.(*sqlSyntaxExpression)
	n.direction = direction
	e.value = &n
	return e
}

// CollationFor emits COLLATION FOR (value), which PostgreSQL represents as
// the pg_collation_for SQL-syntax function.
func CollationFor(value Expr) Expr {
	return newSQLSyntax(sqlCollationFor, []Expr{value})
}

// IsNormalized emits value IS [form] NORMALIZED.
func IsNormalized(value Expr, form ...UnicodeNormalForm) Condition {
	return AsCondition(normalized(value, false, form...))
}

// IsNotNormalized emits value IS NOT [form] NORMALIZED.
func IsNotNormalized(value Expr, form ...UnicodeNormalForm) Condition {
	return AsCondition(normalized(value, true, form...))
}

func normalized(value Expr, negated bool, form ...UnicodeNormalForm) Expr {
	f, hasForm, invalid := normalizeForm(form)
	if invalid.value != nil {
		return invalid
	}
	e := newSQLSyntax(sqlIsNormalized, []Expr{value})
	n := *e.value.(*sqlSyntaxExpression)
	n.negated = negated
	n.form = f
	n.hasForm = hasForm
	e.value = &n
	return e
}

func sqlValue(value sqlValueKind, precision ...int) Expr {
	if len(precision) > 1 {
		return invalidExpr("SQL value function", "at most one precision is accepted")
	}
	e := Expr{kind: exprSQLSyntax, value: &sqlSyntaxExpression{kind: sqlValueFunction, value: value}}
	if len(precision) == 0 {
		return e
	}
	if precision[0] < 0 || precision[0] > 6 {
		return invalidExpr("SQL value function", "precision must be between 0 and 6")
	}
	n := e.value.(*sqlSyntaxExpression)
	n.precision = precision[0]
	n.hasPrec = true
	return e
}

// CurrentTime emits CURRENT_TIME or CURRENT_TIME(precision).
func CurrentTime(precision ...int) Expr { return sqlValue(sqlCurrentTime, precision...) }

// LocalTime emits LOCALTIME or LOCALTIME(precision).
func LocalTime(precision ...int) Expr { return sqlValue(sqlLocalTime, precision...) }

// LocalTimestamp emits LOCALTIMESTAMP or LOCALTIMESTAMP(precision).
func LocalTimestamp(precision ...int) Expr { return sqlValue(sqlLocalTimestamp, precision...) }

// SessionUser emits SESSION_USER.
func SessionUser() Expr { return sqlValue(sqlSessionUser) }

// CurrentSchema emits CURRENT_SCHEMA.
func CurrentSchema() Expr { return sqlValue(sqlCurrentSchema) }

// CurrentCatalog emits CURRENT_CATALOG.
func CurrentCatalog() Expr { return sqlValue(sqlCurrentCatalog) }

func (w *renderer) sqlSyntax(e Expr) {
	n := e.value.(*sqlSyntaxExpression)
	switch n.kind {
	case sqlSubstringFrom:
		if !w.require(len(n.args) == 2 || len(n.args) == 3, "SUBSTRING", "requires value, start and optional length") {
			return
		}
		w.text("SUBSTRING(")
		w.expr(n.args[0])
		w.text(" FROM ")
		w.expr(n.args[1])
		if len(n.args) == 3 {
			w.text(" FOR ")
			w.expr(n.args[2])
		}
		w.byte(')')
	case sqlSubstringFor:
		if !w.require(len(n.args) == 2, "SUBSTRING", "requires value and length") {
			return
		}
		w.text("SUBSTRING(")
		w.expr(n.args[0])
		w.text(" FOR ")
		w.expr(n.args[1])
		w.byte(')')
	case sqlSubstringSimilar:
		if !w.require(len(n.args) == 3, "SUBSTRING", "requires value, pattern and escape") {
			return
		}
		w.text("SUBSTRING(")
		w.expr(n.args[0])
		w.text(" SIMILAR ")
		w.expr(n.args[1])
		w.text(" ESCAPE ")
		w.expr(n.args[2])
		w.byte(')')
	case sqlPosition:
		if !w.require(len(n.args) == 2, "POSITION", "requires substring and value") {
			return
		}
		w.text("POSITION(")
		w.expr(n.args[0])
		w.text(" IN ")
		w.expr(n.args[1])
		w.byte(')')
	case sqlNormalize:
		if !w.require(len(n.args) == 1, "NORMALIZE", "requires one value") {
			return
		}
		w.text("NORMALIZE(")
		w.expr(n.args[0])
		if n.hasForm {
			w.text(", ")
			w.normalForm(n.form)
		}
		w.byte(')')
	case sqlOverlay:
		if !w.require(len(n.args) == 3 || len(n.args) == 4, "OVERLAY", "requires value, replacement, start and optional length") {
			return
		}
		w.text("OVERLAY(")
		w.expr(n.args[0])
		w.text(" PLACING ")
		w.expr(n.args[1])
		w.text(" FROM ")
		w.expr(n.args[2])
		if len(n.args) == 4 {
			w.text(" FOR ")
			w.expr(n.args[3])
		}
		w.byte(')')
	case sqlOverlaps:
		if !w.require(len(n.args) == 4, "OVERLAPS", "requires two pairs of expressions") {
			return
		}
		w.byte('(')
		w.byte('(')
		w.expr(n.args[0])
		w.text(", ")
		w.expr(n.args[1])
		w.text(") OVERLAPS (")
		w.expr(n.args[2])
		w.text(", ")
		w.expr(n.args[3])
		w.text("))")
	case sqlAtLocal:
		if !w.require(len(n.args) == 1, "AT LOCAL", "requires one value") {
			return
		}
		w.byte('(')
		w.expr(n.args[0])
		w.text(" AT LOCAL)")
	case sqlTrim:
		if !w.require(len(n.args) == 1 || len(n.args) == 2, "TRIM", "requires value and optional trim character") {
			return
		}
		if !w.require(n.direction.valid(), "TRIM", "unknown trim direction") {
			return
		}
		w.text("TRIM(")
		w.trimDirection(n.direction)
		if len(n.args) == 2 {
			w.byte(' ')
			w.expr(n.args[0])
		}
		w.text(" FROM ")
		if len(n.args) == 2 {
			w.expr(n.args[1])
		} else {
			w.expr(n.args[0])
		}
		w.byte(')')
	case sqlCollationFor:
		if !w.require(len(n.args) == 1, "COLLATION FOR", "requires one value") {
			return
		}
		w.text("COLLATION FOR (")
		w.expr(n.args[0])
		w.byte(')')
	case sqlIsNormalized:
		if !w.require(len(n.args) == 1, "NORMALIZED", "requires one value") {
			return
		}
		if n.hasForm && !n.form.valid() {
			w.fail(ErrInvalid, "NORMALIZED", "unknown normalization form")
			return
		}
		w.byte('(')
		w.expr(n.args[0])
		if n.negated {
			w.text(" IS NOT ")
		} else {
			w.text(" IS ")
		}
		if n.hasForm {
			w.normalForm(n.form)
			w.byte(' ')
		}
		w.text("NORMALIZED)")
	case sqlValueFunction:
		if !w.require(n.value >= sqlCurrentTime && n.value <= sqlCurrentCatalog, "SQL value function", "unknown SQL value function") {
			return
		}
		if !w.require(!n.hasPrec || n.value <= sqlLocalTimestamp, "SQL value function", "precision is only valid for time and timestamp values") {
			return
		}
		if n.hasPrec && (n.precision < 0 || n.precision > 6) {
			w.fail(ErrInvalid, "SQL value function", "precision must be between 0 and 6")
			return
		}
		switch n.value {
		case sqlCurrentTime:
			w.text("CURRENT_TIME")
		case sqlCurrentTimestamp:
			w.text("CURRENT_TIMESTAMP")
		case sqlLocalTime:
			w.text("LOCALTIME")
		case sqlLocalTimestamp:
			w.text("LOCALTIMESTAMP")
		case sqlSessionUser:
			w.text("SESSION_USER")
		case sqlCurrentSchema:
			w.text("CURRENT_SCHEMA")
		case sqlCurrentCatalog:
			w.text("CURRENT_CATALOG")
		}
		if n.hasPrec {
			w.byte('(')
			w.sqlInt(n.precision)
			w.byte(')')
		}
	default:
		w.fail(ErrInvalid, "SQL syntax", "unknown SQL syntax expression")
	}
}

func (w *renderer) normalForm(form UnicodeNormalForm) {
	switch form {
	case NFC:
		w.text("NFC")
	case NFD:
		w.text("NFD")
	case NFKC:
		w.text("NFKC")
	case NFKD:
		w.text("NFKD")
	default:
		w.fail(ErrInvalid, "Unicode normalization", "unknown normalization form")
	}
}

func (w *renderer) trimDirection(direction TrimDirection) {
	switch direction {
	case TrimBothDirection:
		w.text("BOTH")
	case TrimLeadingDirection:
		w.text("LEADING")
	case TrimTrailingDirection:
		w.text("TRAILING")
	default:
		w.fail(ErrInvalid, "TRIM", "unknown trim direction")
	}
}

func (w *renderer) sqlInt(value int) {
	if value >= 0 {
		w.byte(byte('0' + value))
	}
}
