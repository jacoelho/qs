package qs

import (
	"slices"
	"strings"
)

type callExpression struct {
	args           []Expr
	mods           *callModifiers
	builtin        bool
	minimumVersion PostgreSQLVersion
}

type callModifiers struct {
	aggregateTail
	distinct bool
	order    []Order
	within   []Order
}

// Call invokes a function identified by a dotted name. Its arguments are
// expressions, never implicitly raw SQL. PostgreSQL resolves overloads/codecs.
func Call(name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments)}}
}

func builtin(name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments), builtin: true}}
}
func versionedCall(version PostgreSQLVersion, name string, arguments ...Expr) Expr {
	return Expr{kind: exprCall, text: name, value: &callExpression{args: cloneSlice(arguments), builtin: true, minimumVersion: version}}
}

func (e Expr) withModifiers(modify func(*callModifiers)) Expr {
	if e.kind != exprCall {
		return invalidExpr("function", "modifiers require a function or aggregate call")
	}
	n := *e.value.(*callExpression)
	var m callModifiers
	if n.mods != nil {
		m = *n.mods
	}
	modify(&m)
	n.mods = &m
	e.value = &n
	return e
}

func (e Expr) Distinct() Expr { return e.withModifiers(func(m *callModifiers) { m.distinct = true }) }
func (e Expr) Filter(conditions ...Condition) Expr {
	return e.withModifiers(func(m *callModifiers) { m.filter = slices.Concat(m.filter, conditions) })
}
func (e Expr) OrderBy(terms ...Order) Expr {
	return e.withModifiers(func(m *callModifiers) { m.order = slices.Concat(m.order, terms) })
}
func (e Expr) WithinGroup(terms ...Order) Expr {
	return e.withModifiers(func(m *callModifiers) { m.within = slices.Concat(m.within, terms) })
}
func (e Expr) Over(window WindowSpec) Expr {
	return e.withModifiers(func(m *callModifiers) { m.window = &window; m.windowName = "" })
}
func (e Expr) OverNamed(name string) Expr {
	if name == "" {
		return invalidExpr("OVER", "empty window name")
	}
	return e.withModifiers(func(m *callModifiers) { m.windowName = name; m.window = nil })
}

func (w *renderer) call(e Expr) {
	n := e.value.(*callExpression)
	if n.minimumVersion != 0 {
		w.feature(n.minimumVersion, e.text)
	}
	if n.builtin {
		w.text(e.text)
	} else {
		w.identifierPath(e.text, false)
	}
	w.byte('(')
	m := n.mods
	if m != nil && m.distinct {
		w.text("DISTINCT ")
	}
	w.exprs(n.args, ", ")
	if m != nil && len(m.order) != 0 {
		w.text(" ORDER BY ")
		w.orders(m.order)
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
		w.byte(')')
	}
	w.aggregateTail(m.aggregateTail)
}

func CountAll() Expr                      { return builtin("count", Star()) }
func Count(expr Expr) Expr                { return builtin("count", expr) }
func Sum(expr Expr) Expr                  { return builtin("sum", expr) }
func Avg(expr Expr) Expr                  { return builtin("avg", expr) }
func Min(expr Expr) Expr                  { return builtin("min", expr) }
func Max(expr Expr) Expr                  { return builtin("max", expr) }
func ArrayAgg(expr Expr) Expr             { return builtin("array_agg", expr) }
func StringAgg(expr, delimiter Expr) Expr { return builtin("string_agg", expr, delimiter) }
func JSONAgg(expr Expr) Expr              { return builtin("json_agg", expr) }
func JSONBAgg(expr Expr) Expr             { return builtin("jsonb_agg", expr) }
func JSONObjectAgg(key, value Expr) Expr  { return builtin("json_object_agg", key, value) }
func JSONBObjectAgg(key, value Expr) Expr { return builtin("jsonb_object_agg", key, value) }
func BoolAnd(expr Expr) Expr              { return builtin("bool_and", expr) }
func BoolOr(expr Expr) Expr               { return builtin("bool_or", expr) }
func Every(expr Expr) Expr                { return builtin("every", expr) }
func PercentileCont(fraction Expr) Expr   { return builtin("percentile_cont", fraction) }
func PercentileDisc(fraction Expr) Expr   { return builtin("percentile_disc", fraction) }
func Mode() Expr                          { return builtin("mode") }
func Grouping(exprs ...Expr) Expr         { return builtin("grouping", exprs...) }
func RowNumber() Expr                     { return builtin("row_number") }
func Rank() Expr                          { return builtin("rank") }
func DenseRank() Expr                     { return builtin("dense_rank") }
func PercentRank() Expr                   { return builtin("percent_rank") }
func CumeDist() Expr                      { return builtin("cume_dist") }
func Ntile(buckets Expr) Expr             { return builtin("ntile", buckets) }
func Lag(expr Expr, offsetAndDefault ...Expr) Expr {
	if len(offsetAndDefault) > 2 {
		return invalidExpr("lag", "at most offset and default are accepted")
	}
	return builtin("lag", append([]Expr{expr}, offsetAndDefault...)...)
}
func Lead(expr Expr, offsetAndDefault ...Expr) Expr {
	if len(offsetAndDefault) > 2 {
		return invalidExpr("lead", "at most offset and default are accepted")
	}
	return builtin("lead", append([]Expr{expr}, offsetAndDefault...)...)
}
func FirstValue(expr Expr) Expr  { return builtin("first_value", expr) }
func LastValue(expr Expr) Expr   { return builtin("last_value", expr) }
func NthValue(expr, n Expr) Expr { return builtin("nth_value", expr, n) }

// Special syntactic functions (COALESCE etc.) are not quoted function names.
func Coalesce(first Expr, rest ...Expr) Expr {
	return builtin("coalesce", append([]Expr{first}, rest...)...)
}
func NullIf(value, other Expr) Expr { return builtin("nullif", value, other) }
func Greatest(first Expr, rest ...Expr) Expr {
	return builtin("greatest", append([]Expr{first}, rest...)...)
}
func Least(first Expr, rest ...Expr) Expr { return builtin("least", append([]Expr{first}, rest...)...) }
func Concat(exprs ...Expr) Expr           { return builtin("concat", exprs...) }
func Lower(expr Expr) Expr                { return builtin("lower", expr) }
func Upper(expr Expr) Expr                { return builtin("upper", expr) }
func Length(expr Expr) Expr               { return builtin("length", expr) }
func Trim(expr Expr) Expr                 { return builtin("btrim", expr) }
func Replace(expr, from, to Expr) Expr    { return builtin("replace", expr, from, to) }
func SplitPart(expr, delimiter, field Expr) Expr {
	return builtin("split_part", expr, delimiter, field)
}
func Substring(expr, start Expr, length ...Expr) Expr {
	if len(length) > 1 {
		return invalidExpr("substring", "at most one length is accepted")
	}
	return builtin("substr", append([]Expr{expr, start}, length...)...)
}
func Abs(expr Expr) Expr   { return builtin("abs", expr) }
func Ceil(expr Expr) Expr  { return builtin("ceil", expr) }
func Floor(expr Expr) Expr { return builtin("floor", expr) }
func Round(expr Expr, scale ...Expr) Expr {
	if len(scale) > 1 {
		return invalidExpr("round", "at most one scale is accepted")
	}
	return builtin("round", append([]Expr{expr}, scale...)...)
}
func Power(base, exponent Expr) Expr { return builtin("power", base, exponent) }
func Sqrt(expr Expr) Expr            { return builtin("sqrt", expr) }
func Now() Expr                      { return builtin("now") }

// CurrentDate uses the transaction's current date, without a function call.
func CurrentDate() Expr { return Expr{kind: exprKeyword, text: "CURRENT_DATE"} }
func CurrentTimestamp(precision ...int) Expr {
	return sqlValue(sqlCurrentTimestamp, precision...)
}

// CurrentUser refers to the effective database role, including SET ROLE changes.
func CurrentUser() Expr { return Expr{kind: exprKeyword, text: "CURRENT_USER"} }
func DateTrunc(precision, value Expr, zone ...Expr) Expr {
	if len(zone) > 1 {
		return invalidExpr("date_trunc", "at most one time zone is accepted")
	}
	return builtin("date_trunc", append([]Expr{precision, value}, zone...)...)
}
func DateBin(stride, source, origin Expr) Expr {
	return versionedCall(PostgreSQL14, "date_bin", stride, source, origin)
}
func Age(left Expr, right ...Expr) Expr {
	if len(right) > 1 {
		return invalidExpr("age", "at most one second operand is accepted")
	}
	return builtin("age", append([]Expr{left}, right...)...)
}
func GenerateSeries(start, stop Expr, step ...Expr) Expr {
	if len(step) > 1 {
		return invalidExpr("generate_series", "at most one step is accepted")
	}
	return builtin("generate_series", append([]Expr{start, stop}, step...)...)
}

// NamedArg and Variadic provide PostgreSQL function argument syntax.
func NamedArg(name string, expr Expr) Expr { return Fragment(Ident(name), UnsafeSQL(" => "), expr) }
func Variadic(expr Expr) Expr              { return Fragment(UnsafeSQL("VARIADIC "), expr) }

type DatePart uint8

const (
	PartYear DatePart = iota + 1
	PartMonth
	PartDay
	PartHour
	PartMinute
	PartSecond
	PartEpoch
	PartDOW
	PartISODOW
	PartDOY
	PartWeek
	PartQuarter
	PartISOYear
	PartTimezone
	PartTimezoneHour
	PartTimezoneMinute
	PartMicroseconds
	PartMilliseconds
	PartDecade
	PartCentury
	PartMillennium
)

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
