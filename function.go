package qs

import (
	"slices"
	"strings"
)

type callExpression struct {
	mods           *callModifiers
	args           []Expr
	minimumVersion PostgreSQLVersion
	builtin        bool
}

type callModifiers struct {
	aggregateTail
	order    []Order
	within   []Order
	distinct bool
}

// Call invokes a function identified by a dotted name. Its arguments are
// expressions, never implicitly raw SQL. PostgreSQL resolves overloads/codecs.
func Call(name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments)}}
}

func builtin(name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments), builtin: true}}
}

// builtinOwned constructs a builtin call from a freshly allocated argument
// list. The private callers below allocate exact-sized lists, so copying them
// again would only add construction work while callers of builtin remain
// protected by its variadic-slice copy.
func builtinOwned(name string, arguments []Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: arguments, builtin: true}}
}
func versionedCall(version PostgreSQLVersion, name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments), builtin: true, minimumVersion: version}}
}

func (e Expr) withModifiers(modify func(*callModifiers)) Expr {
	if e.kind != exprCall {
		return invalidExpr("function", "modifiers require a function or aggregate call")
	}
	n := *ownedPayload[*callExpression](e.value)
	var m callModifiers
	if n.mods != nil {
		m = *n.mods
	}
	modify(&m)
	n.mods = &m
	e.value = &n
	return e
}

// Distinct adds DISTINCT to a function or aggregate call.
func (e Expr) Distinct() Expr { return e.withModifiers(func(m *callModifiers) { m.distinct = true }) }

// Filter adds a FILTER (WHERE ...) clause to an aggregate call.
func (e Expr) Filter(conditions ...Condition) Expr {
	return e.withModifiers(func(m *callModifiers) { m.filter = slices.Concat(m.filter, conditions) })
}

// OrderBy adds in-call ordering terms to an aggregate call.
func (e Expr) OrderBy(terms ...Order) Expr {
	return e.withModifiers(func(m *callModifiers) { m.order = slices.Concat(m.order, terms) })
}

// WithinGroup adds the ORDER BY clause used by an ordered-set aggregate.
func (e Expr) WithinGroup(terms ...Order) Expr {
	return e.withModifiers(func(m *callModifiers) { m.within = slices.Concat(m.within, terms) })
}

// Over applies an inline window definition to a function or aggregate call.
func (e Expr) Over(window WindowSpec) Expr {
	return e.withModifiers(func(m *callModifiers) { m.window = &window; m.windowName = "" })
}

// OverNamed applies a named window reference to a function or aggregate call.
// An empty name produces an invalid expression.
func (e Expr) OverNamed(name string) Expr {
	if name == "" {
		return invalidExpr("OVER", "empty window name")
	}
	return e.withModifiers(func(m *callModifiers) { m.windowName = name; m.window = nil })
}

func (w *renderer) call(e Expr) {
	n := ownedPayload[*callExpression](e.value)
	if n.minimumVersion != 0 {
		w.feature(n.minimumVersion, e.text)
		if w.stopped("version", 0) {
			return
		}
	}
	if n.builtin {
		w.text(e.text)
	} else {
		w.identifierPath(e.text, false)
		if w.stopped("function name", 0) {
			return
		}
	}
	w.byte('(')
	m := n.mods
	if m != nil && m.distinct {
		w.text("DISTINCT ")
	}
	w.exprs(n.args, ", ")
	if w.stopped("arguments", 0) {
		return
	}
	if m != nil && len(m.order) != 0 {
		w.text(" ORDER BY ")
		w.orders(m.order)
		if w.stopped("ORDER BY", 0) {
			return
		}
	}
	w.byte(')')
	if m == nil {
		return
	}
	if len(m.within) != 0 {
		if !w.require(!m.distinct && len(m.order) == 0, "WITHIN GROUP", "cannot combine with DISTINCT or in-call ORDER BY") {
			return
		}
		if !w.require(m.window == nil && m.windowName == "", "WITHIN GROUP", "ordered-set aggregates cannot be window functions") {
			return
		}
		w.text(" WITHIN GROUP (ORDER BY ")
		w.orders(m.within)
		if w.stopped("WITHIN GROUP", 0) {
			return
		}
		w.byte(')')
	}
	w.aggregateTail(m.aggregateTail)
}

// CountAll returns count(*) as an aggregate expression.
func CountAll() Expr { return builtin("count", Star()) }

// Count returns count(expr) as an aggregate expression.
func Count(expr Expr) Expr { return builtin("count", expr) }

// Sum returns sum(expr) as an aggregate expression.
func Sum(expr Expr) Expr { return builtin("sum", expr) }

// Avg returns avg(expr) as an aggregate expression.
func Avg(expr Expr) Expr { return builtin("avg", expr) }

// Min returns min(expr) as an aggregate expression.
func Min(expr Expr) Expr { return builtin("min", expr) }

// Max returns max(expr) as an aggregate expression.
func Max(expr Expr) Expr { return builtin("max", expr) }

// ArrayAgg returns array_agg(expr) as an aggregate expression.
func ArrayAgg(expr Expr) Expr { return builtin("array_agg", expr) }

// StringAgg returns string_agg(expr, delimiter) as an aggregate expression.
func StringAgg(expr, delimiter Expr) Expr { return builtin("string_agg", expr, delimiter) }

// JSONAgg returns json_agg(expr) as an aggregate expression.
func JSONAgg(expr Expr) Expr { return builtin("json_agg", expr) }

// JSONBAgg returns jsonb_agg(expr) as an aggregate expression.
func JSONBAgg(expr Expr) Expr { return builtin("jsonb_agg", expr) }

// JSONObjectAgg returns json_object_agg(key, value) as an aggregate expression.
func JSONObjectAgg(key, value Expr) Expr { return builtin("json_object_agg", key, value) }

// JSONBObjectAgg returns jsonb_object_agg(key, value) as an aggregate expression.
func JSONBObjectAgg(key, value Expr) Expr { return builtin("jsonb_object_agg", key, value) }

// BoolAnd returns bool_and(expr) as an aggregate expression.
func BoolAnd(expr Expr) Expr { return builtin("bool_and", expr) }

// BoolOr returns bool_or(expr) as an aggregate expression.
func BoolOr(expr Expr) Expr { return builtin("bool_or", expr) }

// Every returns every(expr), PostgreSQL's synonym for bool_and, as an aggregate expression.
func Every(expr Expr) Expr { return builtin("every", expr) }

// PercentileCont returns percentile_cont(fraction) as an ordered-set aggregate expression.
func PercentileCont(fraction Expr) Expr { return builtin("percentile_cont", fraction) }

// PercentileDisc returns percentile_disc(fraction) as an ordered-set aggregate expression.
func PercentileDisc(fraction Expr) Expr { return builtin("percentile_disc", fraction) }

// Mode returns mode() as an ordered-set aggregate expression.
func Mode() Expr { return builtin("mode") }

// Grouping returns grouping(exprs...) as an aggregate expression.
func Grouping(exprs ...Expr) Expr { return builtin("grouping", exprs...) }

// RowNumber returns the row_number window function.
func RowNumber() Expr { return builtin("row_number") }

// Rank returns the rank window function.
func Rank() Expr { return builtin("rank") }

// DenseRank returns the dense_rank window function.
func DenseRank() Expr { return builtin("dense_rank") }

// PercentRank returns the percent_rank window function.
func PercentRank() Expr { return builtin("percent_rank") }

// CumeDist returns the cume_dist window function.
func CumeDist() Expr { return builtin("cume_dist") }

// Ntile returns the ntile window function with the requested bucket count.
func Ntile(buckets Expr) Expr { return builtin("ntile", buckets) }

// Lag returns the lag window function with an optional offset and default.
func Lag(expr Expr, offsetAndDefault ...Expr) Expr {
	if len(offsetAndDefault) > 2 {
		return invalidExpr("lag", "at most offset and default are accepted")
	}
	arguments := make([]Expr, 1+len(offsetAndDefault))
	arguments[0] = expr
	copy(arguments[1:], offsetAndDefault)
	return builtinOwned("lag", arguments)
}

// Lead returns the lead window function with an optional offset and default.
func Lead(expr Expr, offsetAndDefault ...Expr) Expr {
	if len(offsetAndDefault) > 2 {
		return invalidExpr("lead", "at most offset and default are accepted")
	}
	arguments := make([]Expr, 1+len(offsetAndDefault))
	arguments[0] = expr
	copy(arguments[1:], offsetAndDefault)
	return builtinOwned("lead", arguments)
}

// FirstValue returns the first_value window function.
func FirstValue(expr Expr) Expr { return builtin("first_value", expr) }

// LastValue returns the last_value window function.
func LastValue(expr Expr) Expr { return builtin("last_value", expr) }

// NthValue returns the nth_value window function.
func NthValue(expr, n Expr) Expr { return builtin("nth_value", expr, n) }

// Coalesce returns the COALESCE(first, rest...) conditional expression.
func Coalesce(first Expr, rest ...Expr) Expr {
	arguments := make([]Expr, 1+len(rest))
	arguments[0] = first
	copy(arguments[1:], rest)
	return builtinOwned("coalesce", arguments)
}

// NullIf returns the NULLIF(value, other) conditional expression.
func NullIf(value, other Expr) Expr { return builtin("nullif", value, other) }

// Greatest returns the GREATEST(first, rest...) conditional expression.
func Greatest(first Expr, rest ...Expr) Expr {
	arguments := make([]Expr, 1+len(rest))
	arguments[0] = first
	copy(arguments[1:], rest)
	return builtinOwned("greatest", arguments)
}

// Least returns the LEAST(first, rest...) conditional expression.
func Least(first Expr, rest ...Expr) Expr {
	arguments := make([]Expr, 1+len(rest))
	arguments[0] = first
	copy(arguments[1:], rest)
	return builtinOwned("least", arguments)
}

// Concat returns concat(exprs...) as a function expression.
func Concat(exprs ...Expr) Expr { return builtin("concat", exprs...) }

// Lower returns lower(expr) as a function expression.
func Lower(expr Expr) Expr { return builtin("lower", expr) }

// Upper returns upper(expr) as a function expression.
func Upper(expr Expr) Expr { return builtin("upper", expr) }

// Length returns length(expr) as a function expression.
func Length(expr Expr) Expr { return builtin("length", expr) }

// Trim returns btrim(expr) as a function expression.
func Trim(expr Expr) Expr { return builtin("btrim", expr) }

// Replace returns replace(expr, from, to) as a function expression.
func Replace(expr, from, to Expr) Expr { return builtin("replace", expr, from, to) }

// SplitPart returns split_part(expr, delimiter, field) as a function expression.
func SplitPart(expr, delimiter, field Expr) Expr {
	return builtin("split_part", expr, delimiter, field)
}

// Substring returns the ordinary substr(expr, start [, length]) function.
// Use SubstringFrom or SubstringFor for PostgreSQL's SQL substring grammar.
func Substring(expr, start Expr, length ...Expr) Expr {
	if len(length) > 1 {
		return invalidExpr("substring", "at most one length is accepted")
	}
	arguments := make([]Expr, 2+len(length))
	arguments[0], arguments[1] = expr, start
	copy(arguments[2:], length)
	return builtinOwned("substr", arguments)
}

// Abs returns abs(expr) as a function expression.
func Abs(expr Expr) Expr { return builtin("abs", expr) }

// Ceil returns ceil(expr) as a function expression.
func Ceil(expr Expr) Expr { return builtin("ceil", expr) }

// Floor returns floor(expr) as a function expression.
func Floor(expr Expr) Expr { return builtin("floor", expr) }

// Round returns round(expr [, scale]) as a function expression.
func Round(expr Expr, scale ...Expr) Expr {
	if len(scale) > 1 {
		return invalidExpr("round", "at most one scale is accepted")
	}
	arguments := make([]Expr, 1+len(scale))
	arguments[0] = expr
	copy(arguments[1:], scale)
	return builtinOwned("round", arguments)
}

// Power returns power(base, exponent) as a function expression.
func Power(base, exponent Expr) Expr { return builtin("power", base, exponent) }

// Sqrt returns sqrt(expr) as a function expression.
func Sqrt(expr Expr) Expr { return builtin("sqrt", expr) }

// Now returns the now() time function.
func Now() Expr { return builtin("now") }

// CurrentDate uses the transaction's current date, without a function call.
func CurrentDate() Expr { return Expr{kind: exprKeyword, text: "CURRENT_DATE"} }

// CurrentTimestamp returns CURRENT_TIMESTAMP, optionally with precision from
// zero through six.
func CurrentTimestamp(precision ...int) Expr {
	return sqlValue(sqlCurrentTimestamp, precision...)
}

// CurrentUser refers to the effective database role, including SET ROLE changes.
func CurrentUser() Expr { return Expr{kind: exprKeyword, text: "CURRENT_USER"} }

// DateTrunc returns date_trunc(precision, value [, zone]).
func DateTrunc(precision, value Expr, zone ...Expr) Expr {
	if len(zone) > 1 {
		return invalidExpr("date_trunc", "at most one time zone is accepted")
	}
	arguments := make([]Expr, 2+len(zone))
	arguments[0], arguments[1] = precision, value
	copy(arguments[2:], zone)
	return builtinOwned("date_trunc", arguments)
}

// DateBin returns date_bin(stride, source, origin), available in PostgreSQL 14+.
func DateBin(stride, source, origin Expr) Expr {
	return versionedCall(PostgreSQL14, "date_bin", stride, source, origin)
}

// Age returns age(left [, right]).
func Age(left Expr, right ...Expr) Expr {
	if len(right) > 1 {
		return invalidExpr("age", "at most one second operand is accepted")
	}
	arguments := make([]Expr, 1+len(right))
	arguments[0] = left
	copy(arguments[1:], right)
	return builtinOwned("age", arguments)
}

// GenerateSeries returns generate_series(start, stop [, step]).
func GenerateSeries(start, stop Expr, step ...Expr) Expr {
	if len(step) > 1 {
		return invalidExpr("generate_series", "at most one step is accepted")
	}
	arguments := make([]Expr, 2+len(step))
	arguments[0], arguments[1] = start, stop
	copy(arguments[2:], step)
	return builtinOwned("generate_series", arguments)
}

// NamedArg returns a function argument using PostgreSQL's name => value syntax.
func NamedArg(name string, expr Expr) Expr { return Fragment(Ident(name), UnsafeSQL(" => "), expr) }

// Variadic marks a function argument with PostgreSQL's VARIADIC keyword.
func Variadic(expr Expr) Expr { return Fragment(UnsafeSQL("VARIADIC "), expr) }

// DatePart identifies a field accepted by EXTRACT.
type DatePart uint8

const (
	// PartYear extracts the year field.
	PartYear DatePart = iota + 1
	// PartMonth extracts the month field.
	PartMonth
	// PartDay extracts the day field.
	PartDay
	// PartHour extracts the hour field.
	PartHour
	// PartMinute extracts the minute field.
	PartMinute
	// PartSecond extracts the second field.
	PartSecond
	// PartEpoch extracts the epoch field.
	PartEpoch
	// PartDOW extracts the day-of-week field.
	PartDOW
	// PartISODOW extracts the ISO day-of-week field.
	PartISODOW
	// PartDOY extracts the day-of-year field.
	PartDOY
	// PartWeek extracts the week field.
	PartWeek
	// PartQuarter extracts the quarter field.
	PartQuarter
	// PartISOYear extracts the ISO week-numbering year field.
	PartISOYear
	// PartTimezone extracts the time-zone offset in seconds.
	PartTimezone
	// PartTimezoneHour extracts the time-zone offset in hours.
	PartTimezoneHour
	// PartTimezoneMinute extracts the time-zone offset in minutes.
	PartTimezoneMinute
	// PartMicroseconds extracts the microseconds field.
	PartMicroseconds
	// PartMilliseconds extracts the milliseconds field.
	PartMilliseconds
	// PartDecade extracts the decade field.
	PartDecade
	// PartCentury extracts the century field.
	PartCentury
	// PartMillennium extracts the millennium field.
	PartMillennium
)

// Extract returns PostgreSQL's EXTRACT(field FROM expr) expression.
func Extract(part DatePart, expr Expr) Expr {
	var name string
	switch part {
	case PartYear:
		name = "YEAR"
	case PartMonth:
		name = "MONTH"
	case PartDay:
		name = "DAY"
	case PartHour:
		name = "HOUR"
	case PartMinute:
		name = "MINUTE"
	case PartSecond:
		name = "SECOND"
	case PartEpoch:
		name = "EPOCH"
	case PartDOW:
		name = "DOW"
	case PartISODOW:
		name = "ISODOW"
	case PartDOY:
		name = "DOY"
	case PartWeek:
		name = "WEEK"
	case PartQuarter:
		name = "QUARTER"
	case PartISOYear:
		name = "ISOYEAR"
	case PartTimezone:
		name = "TIMEZONE"
	case PartTimezoneHour:
		name = "TIMEZONE_HOUR"
	case PartTimezoneMinute:
		name = "TIMEZONE_MINUTE"
	case PartMicroseconds:
		name = "MICROSECONDS"
	case PartMilliseconds:
		name = "MILLISECONDS"
	case PartDecade:
		name = "DECADE"
	case PartCentury:
		name = "CENTURY"
	case PartMillennium:
		name = "MILLENNIUM"
	default:
		return invalidExpr("EXTRACT", "unknown date part")
	}
	return Expr{kind: exprSpecialCall, text: name, node: &expression{left: expr}}
}

// ExtractNamed preserves one of PostgreSQL's accepted EXTRACT field aliases
// when source spelling carries information that DatePart intentionally folds
// together (for example MICROSECOND versus MICROSECONDS).  The allowlist is
// the grammar's field vocabulary, so this remains a static EXTRACT helper and
// cannot be used as an arbitrary SQL fragment.
func ExtractNamed(part string, expr Expr) Expr {
	part = strings.ToLower(part)
	switch part {
	case "year", "month", "day", "hour", "minute", "second", "seconds",
		"epoch", "dow", "isodow", "doy", "week", "quarter", "isoyear",
		"timezone", "timezone_hour", "timezone_minute", "timezone_h", "timezone_m",
		"microseconds", "microsecond", "microsec", "milliseconds", "millisecond",
		"decade", "century", "millennium", "julian":
		return Expr{kind: exprSpecialCall, text: part, node: &expression{left: expr}}
	default:
		return invalidExpr("EXTRACT", "unknown date part")
	}
}

func (w *renderer) specialCall(e Expr) {
	w.text("EXTRACT(")
	w.text(e.text)
	w.text(" FROM ")
	w.expr(e.node.left)
	w.byte(')')
}
