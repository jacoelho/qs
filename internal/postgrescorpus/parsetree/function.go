package parsetree

import (
	"slices"
	"strings"

	qs "github.com/jacoelho/qs"
)

var datePartsByName = map[string]qs.DatePart{
	"year":            qs.PartYear,
	"month":           qs.PartMonth,
	"day":             qs.PartDay,
	"hour":            qs.PartHour,
	"minute":          qs.PartMinute,
	"second":          qs.PartSecond,
	"epoch":           qs.PartEpoch,
	"dow":             qs.PartDOW,
	"isodow":          qs.PartISODOW,
	"doy":             qs.PartDOY,
	"week":            qs.PartWeek,
	"quarter":         qs.PartQuarter,
	"isoyear":         qs.PartISOYear,
	"timezone":        qs.PartTimezone,
	"timezone_hour":   qs.PartTimezoneHour,
	"timezone_minute": qs.PartTimezoneMinute,
	"microseconds":    qs.PartMicroseconds,
	"milliseconds":    qs.PartMilliseconds,
	"decade":          qs.PartDecade,
	"century":         qs.PartCentury,
	"millennium":      qs.PartMillennium,
}

var normalForms = map[string]qs.UnicodeNormalForm{
	"NFC": qs.NFC, "NFD": qs.NFD, "NFKC": qs.NFKC, "NFKD": qs.NFKD,
}

func (b *builder) function(n node) qs.Expr {
	name := n.strings("funcname")
	if len(name) == 0 {
		b.gapf("function has no name")
		return qs.Expr{}
	}
	if n.text("funcformat") == "COERCE_SQL_SYNTAX" {
		return b.syntaxFunction(n, name)
	}
	items := n.nodes("args")
	arguments := make([]qs.Expr, len(items))
	for index, item := range items {
		if item.tag == "NamedArgExpr" {
			arguments[index] = qs.NamedArg(item.text("name"), b.expr(item.node("arg")))
		} else {
			arguments[index] = b.expr(item)
		}
	}
	if n.flag("func_variadic") {
		if len(arguments) == 0 {
			b.gapf("VARIADIC function requires one argument")
			return qs.Expr{}
		}
		last := len(arguments) - 1
		arguments[last] = qs.Variadic(arguments[last])
	}
	if n.flag("agg_star") {
		arguments = []qs.Expr{qs.Star()}
	}
	var call qs.Expr
	switch {
	case len(name) == 1 && name[0] == "concat":
		call = qs.Concat(arguments...)
	case len(name) == 1:
		call = qs.Call(name[0], arguments...)
	case len(name) == 2:
		call = qs.QualifiedCall(name[0], name[1], arguments...)
	default:
		b.gapf("function name has %d parts", len(name))
		return qs.Expr{}
	}
	if n.flag("agg_distinct") {
		call = call.Distinct()
	}
	if order := n.nodes("agg_order"); len(order) != 0 {
		if n.flag("agg_within_group") {
			call = call.WithinGroup(b.orders(order)...)
		} else {
			call = call.OrderBy(b.orders(order)...)
		}
	}
	if filter := n.node("agg_filter"); filter.present() {
		call = call.Filter(b.condition(filter))
	}
	if over := n.record("over"); over.present() {
		// OVER name references a window; OVER (name) is a specification
		// that inherits from it.
		if reference := over.text("name"); reference != "" {
			call = call.OverNamed(reference)
		} else {
			call = call.Over(b.window(over))
		}
	}
	return call
}

// syntaxFunction builds calls the parser derives from dedicated SQL syntax
// such as EXTRACT(... FROM ...). Each must be re-emitted in that syntax to
// parse back to the same tree.
func (b *builder) syntaxFunction(n node, name []string) qs.Expr {
	arguments := n.nodes("args")
	function := name[len(name)-1]
	arity := func(counts ...int) bool {
		if slices.Contains(counts, len(arguments)) {
			return true
		}
		b.gapf("%s argument count %d", function, len(arguments))
		return false
	}
	switch function {
	case "extract":
		if !arity(2) {
			break
		}
		part, ok := b.stringConstant(arguments[0])
		if !ok {
			b.gapf("EXTRACT needs a date-part and expression")
			break
		}
		if known, ok := datePartsByName[part]; ok {
			return qs.Extract(known, b.expr(arguments[1]))
		}
		// qs validates the remaining spellings and rejects unknown fields.
		return qs.ExtractNamed(part, b.expr(arguments[1]))
	case "timezone":
		// The zone precedes the value in the stored call.
		switch {
		case len(arguments) == 2:
			zone := arguments[0]
			return b.expr(arguments[1]).AtTimeZone(b.expr(zone))
		case arity(1):
			return b.expr(arguments[0]).AtLocal()
		}
	case "substring":
		// FROM, FOR and SIMILAR forms share this call shape; SubstringFrom
		// reparses to it for all of them.
		if arity(2, 3) {
			values := b.exprs(arguments)
			return qs.SubstringFrom(values[0], values[1], values[2:]...)
		}
	case "position":
		// Stored as (value, substring); written as substring IN value.
		if arity(2) {
			substring := b.expr(arguments[1])
			return qs.Position(substring, b.expr(arguments[0]))
		}
	case "normalize":
		if arity(1, 2) {
			value := b.expr(arguments[0])
			if forms, ok := b.normalForm(arguments[1:]); ok {
				return qs.Normalize(value, forms...)
			}
		}
	case "is_normalized":
		if arity(1, 2) {
			value := b.expr(arguments[0])
			if forms, ok := b.normalForm(arguments[1:]); ok {
				return qs.IsNormalized(value, forms...).Expr()
			}
		}
	case "overlay":
		if arity(3, 4) {
			values := b.exprs(arguments)
			return qs.Overlay(values[0], values[1], values[2], values[3:]...)
		}
	case "overlaps":
		if arity(4) {
			values := b.exprs(arguments)
			return qs.Overlaps(values[0], values[1], values[2], values[3]).Expr()
		}
	case "btrim", "ltrim", "rtrim":
		if arity(1, 2) {
			direction := qs.TrimBothDirection
			switch function {
			case "ltrim":
				direction = qs.TrimLeadingDirection
			case "rtrim":
				direction = qs.TrimTrailingDirection
			}
			values := b.exprs(arguments)
			return qs.TrimSyntax(values[0], direction, values[1:]...)
		}
	case "pg_collation_for":
		if arity(1) {
			return qs.CollationFor(b.expr(arguments[0]))
		}
	default:
		b.gapf("SQL-syntax function %s", function)
	}
	return qs.Expr{}
}

// stringConstant returns the value of a string literal node.
func (b *builder) stringConstant(n node) (string, bool) {
	if n.tag != tagConstant || !n.has("sval") {
		return "", false
	}
	return n.record("sval").text("sval"), true
}

// normalForm reads the optional Unicode normal form argument.
func (b *builder) normalForm(arguments []node) ([]qs.UnicodeNormalForm, bool) {
	if len(arguments) == 0 {
		return nil, true
	}
	name, ok := b.stringConstant(arguments[0])
	form, known := normalForms[strings.ToUpper(name)]
	if !ok || !known {
		b.gapf("Unicode normal form %q", name)
		return nil, false
	}
	return []qs.UnicodeNormalForm{form}, true
}
