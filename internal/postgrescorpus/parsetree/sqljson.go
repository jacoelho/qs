package parsetree

import (
	"strings"

	qs "github.com/jacoelho/qs"
)

// This file builds the SQL/JSON nodes. PostgreSQL's grammar accepts clause
// combinations its analyzer then rejects; qs gives each form only the methods
// PostgreSQL accepts, so those combinations are gaps with the reason stated.

// jsonExpr builds the SQL/JSON expression nodes; any other tag is a gap.
func (b *builder) jsonExpr(n node) qs.Expr {
	switch n.tag {
	case "JsonObjectConstructor":
		return b.jsonObject(n)
	case "JsonArrayConstructor":
		return b.jsonArray(n)
	case "JsonArrayQueryConstructor":
		return b.jsonArrayQuery(n)
	case "JsonObjectAgg":
		return b.jsonObjectAggregate(n)
	case "JsonArrayAgg":
		return b.jsonArrayAggregate(n)
	case "JsonParseExpr":
		return b.jsonParse(n)
	case "JsonScalarExpr":
		return qs.JSONScalar(b.expr(n.node("expr")))
	case "JsonSerializeExpr":
		return b.jsonSerialize(n)
	case "JsonIsPredicate":
		return b.jsonPredicate(n)
	case "JsonFuncExpr":
		return b.jsonFunction(n)
	default:
		b.gapf("unsupported expression node %s", n.tag)
		return qs.Expr{}
	}
}

const (
	jsonBehaviorPrefix  = "JSON_BEHAVIOR_"
	jsonBehaviorDefault = "JSON_BEHAVIOR_DEFAULT"
	jsonBehaviorError   = "JSON_BEHAVIOR_ERROR"
	jsonBehaviorNull    = "JSON_BEHAVIOR_NULL"
	jsonBehaviorTrue    = "JSON_BEHAVIOR_TRUE"
	jsonBehaviorFalse   = "JSON_BEHAVIOR_FALSE"
	jsonBehaviorUnknown = "JSON_BEHAVIOR_UNKNOWN"
	jsonBehaviorArray   = "JSON_BEHAVIOR_EMPTY_ARRAY"
	jsonBehaviorObject  = "JSON_BEHAVIOR_EMPTY_OBJECT"
)

// jsonFormat is a FORMAT clause as the parse tree records it.
type jsonFormat uint8

const (
	jsonFormatNone jsonFormat = iota
	jsonFormatJSON
	jsonFormatJSONUTF8
)

func (b *builder) jsonFormat(n node) jsonFormat {
	if n.text("format_type") != "JS_FORMAT_JSON" {
		return jsonFormatNone
	}
	switch encoding := n.text("encoding"); encoding {
	case "JS_ENC_DEFAULT", "":
		return jsonFormatJSON
	case "JS_ENC_UTF8":
		return jsonFormatJSONUTF8
	default:
		b.gapf("FORMAT JSON ENCODING %s; PostgreSQL accepts UTF8 only", strings.TrimPrefix(encoding, "JS_ENC_"))
		return jsonFormatNone
	}
}

// jsonOperand builds a JsonValueExpr: an expression and its optional format.
func (b *builder) jsonOperand(n node) qs.JSONOperand {
	value := b.expr(n.node("raw_expr"))
	switch b.jsonFormat(n.record("format")) {
	case jsonFormatJSON:
		return qs.FormatJSON(value)
	case jsonFormatJSONUTF8:
		return qs.FormatJSONUTF8(value)
	case jsonFormatNone:
	}
	return value
}

func (b *builder) jsonOperands(items []node) []qs.JSONOperand {
	operands := make([]qs.JSONOperand, len(items))
	for index, item := range items {
		operands[index] = b.jsonOperand(item)
	}
	return operands
}

type jsonReturns[T any] interface {
	Returning(typ qs.DataType) T
}

type jsonReturnsFormatted[T any] interface {
	jsonReturns[T]
	ReturningFormatJSON(typ qs.DataType) T
	ReturningFormatJSONUTF8() T
}

// jsonOutput reads a JsonOutput for a form whose RETURNING takes a format.
func jsonOutput[T jsonReturnsFormatted[T]](b *builder, target T, output node) T {
	if !output.present() {
		return target
	}
	name := output.record("typeName")
	typ := b.dataType(name)
	switch b.jsonFormat(output.record("returning").record("format")) {
	case jsonFormatJSON:
		return target.ReturningFormatJSON(typ)
	case jsonFormatJSONUTF8:
		// The builder states bytea itself, the one type PostgreSQL accepts
		// with an encoding, so any other spelling has no construction.
		if names := name.strings("names"); len(names) != 1 || names[0] != "bytea" || name.has("typmods") || name.has("arrayBounds") {
			b.gapf("RETURNING FORMAT JSON ENCODING UTF8 with a type not spelled bytea")
			return target
		}
		return target.ReturningFormatJSONUTF8()
	case jsonFormatNone:
	}
	return target.Returning(typ)
}

// jsonPlainOutput reads a JsonOutput for JSON_VALUE, whose RETURNING takes no
// format.
func jsonPlainOutput[T jsonReturns[T]](b *builder, target T, output node, form string) T {
	if !output.present() {
		return target
	}
	if b.jsonFormat(output.record("returning").record("format")) != jsonFormatNone {
		b.gapf("%s RETURNING with FORMAT JSON; PostgreSQL rejects it", form)
		return target
	}
	return target.Returning(b.dataType(output.record("typeName")))
}

type jsonNullable[T any] interface {
	NullOnNull() T
	AbsentOnNull() T
}

// jsonNulls states the NULL clause only when it differs from the form's
// default, which is all the parse tree records.
func jsonNulls[T jsonNullable[T]](target T, n node, absentByDefault bool) T {
	switch absent := n.flag("absent_on_null"); {
	case absent && !absentByDefault:
		return target.AbsentOnNull()
	case !absent && absentByDefault:
		return target.NullOnNull()
	}
	return target
}

func jsonUnique[T interface{ WithUniqueKeys() T }](target T, unique bool) T {
	if unique {
		return target.WithUniqueKeys()
	}
	return target
}

func (b *builder) jsonObject(n node) qs.Expr {
	pairs := n.nodes("exprs")
	members := make([]qs.JSONMember, len(pairs))
	for index, pair := range pairs {
		members[index] = b.jsonMember(pair)
	}
	object := jsonNulls(qs.JSONObject(members...), n, false)
	object = jsonUnique(object, n.flag("unique"))
	return jsonOutput(b, object, n.record("output")).Expr()
}

// jsonMember builds a JsonKeyValue.
func (b *builder) jsonMember(n node) qs.JSONMember {
	return qs.JSONPair(b.expr(n.node("key")), b.jsonOperand(n.record("value")))
}

func (b *builder) jsonArray(n node) qs.Expr {
	array := jsonNulls(qs.JSONArray(b.jsonOperands(n.nodes("exprs"))...), n, true)
	return jsonOutput(b, array, n.record("output")).Expr()
}

func (b *builder) jsonArrayQuery(n node) qs.Expr {
	array := qs.JSONArrayQuery(b.rowset(n.node("query")))
	switch b.jsonFormat(n.record("format")) {
	case jsonFormatJSON:
		array = array.FormatJSON()
	case jsonFormatJSONUTF8:
		array = array.FormatJSONUTF8()
	case jsonFormatNone:
	}
	return jsonOutput(b, array, n.record("output")).Expr()
}

type jsonAggregate[T any] interface {
	Filter(conditions ...qs.Condition) T
	Over(window qs.WindowSpec) T
	OverNamed(name string) T
}

// jsonAggregateTail applies the FILTER and OVER of a JsonAggConstructor.
func jsonAggregateTail[T jsonAggregate[T]](b *builder, target T, constructor node) T {
	if filter := constructor.node("agg_filter"); filter.present() {
		target = target.Filter(b.condition(filter))
	}
	over := constructor.record("over")
	if !over.present() {
		return target
	}
	if reference := over.text("name"); reference != "" {
		return target.OverNamed(reference)
	}
	return target.Over(b.window(over))
}

func (b *builder) jsonObjectAggregate(n node) qs.Expr {
	pair := n.record("arg")
	constructor := n.record("constructor")
	aggregate := qs.JSONObjectAggregate(b.expr(pair.node("key")), b.jsonOperand(pair.record("value")))
	aggregate = jsonNulls(aggregate, n, false)
	aggregate = jsonUnique(aggregate, n.flag("unique"))
	aggregate = jsonOutput(b, aggregate, constructor.record("output"))
	return jsonAggregateTail(b, aggregate, constructor).Expr()
}

func (b *builder) jsonArrayAggregate(n node) qs.Expr {
	constructor := n.record("constructor")
	aggregate := qs.JSONArrayAggregate(b.jsonOperand(n.record("arg")))
	if order := constructor.nodes("agg_order"); len(order) != 0 {
		aggregate = aggregate.OrderBy(b.orders(order)...)
	}
	aggregate = jsonNulls(aggregate, n, true)
	aggregate = jsonOutput(b, aggregate, constructor.record("output"))
	return jsonAggregateTail(b, aggregate, constructor).Expr()
}

func (b *builder) jsonParse(n node) qs.Expr {
	return jsonUnique(qs.JSONParse(b.jsonOperand(n.record("expr"))), n.flag("unique_keys")).Expr()
}

func (b *builder) jsonSerialize(n node) qs.Expr {
	return jsonOutput(b, qs.JSONSerialize(b.jsonOperand(n.record("expr"))), n.record("output")).Expr()
}

func (b *builder) jsonPredicate(n node) qs.Expr {
	predicate := qs.IsJSON(b.expr(n.node("expr")))
	switch item := n.text("item_type"); item {
	case "JS_TYPE_ANY", "":
	case "JS_TYPE_SCALAR":
		predicate = predicate.Scalar()
	case "JS_TYPE_ARRAY":
		predicate = predicate.Array()
	case "JS_TYPE_OBJECT":
		predicate = predicate.Object()
	default:
		b.gapf("unsupported IS JSON item type %s", item)
	}
	return jsonUnique(predicate, n.flag("unique_keys")).Expr()
}

// jsonBehaviors is the ON EMPTY or ON ERROR set of one form: the behaviors
// PostgreSQL accepts there, each as the qs method that states it.
type jsonBehaviors[T any] struct {
	plain map[string]func(T) T
	// value is DEFAULT expression; nil where the form accepts none.
	value func(T, qs.Expr) T
	// where names the form and event in a gap reason.
	where string
}

func jsonBehavior[T any](b *builder, target T, n node, set jsonBehaviors[T]) T {
	if !n.present() {
		return target
	}
	kind := n.text("btype")
	if kind == jsonBehaviorDefault && set.value != nil {
		return set.value(target, b.expr(n.node("expr")))
	}
	apply, ok := set.plain[kind]
	if !ok {
		name := strings.ReplaceAll(strings.TrimPrefix(kind, jsonBehaviorPrefix), "_", " ")
		b.gapf("%s does not accept %s; PostgreSQL rejects it", set.where, name)
		return target
	}
	return apply(target)
}

var (
	jsonExistsOnError = jsonBehaviors[qs.JSONExistsBuilder]{
		where: "JSON_EXISTS ON ERROR",
		plain: map[string]func(qs.JSONExistsBuilder) qs.JSONExistsBuilder{
			jsonBehaviorError:   qs.JSONExistsBuilder.ErrorOnError,
			jsonBehaviorTrue:    qs.JSONExistsBuilder.TrueOnError,
			jsonBehaviorFalse:   qs.JSONExistsBuilder.FalseOnError,
			jsonBehaviorUnknown: qs.JSONExistsBuilder.UnknownOnError,
		},
	}
	jsonValueOnEmpty = jsonBehaviors[qs.JSONValueBuilder]{
		where: "JSON_VALUE ON EMPTY",
		plain: map[string]func(qs.JSONValueBuilder) qs.JSONValueBuilder{
			jsonBehaviorError: qs.JSONValueBuilder.ErrorOnEmpty,
			jsonBehaviorNull:  qs.JSONValueBuilder.NullOnEmpty,
		},
		value: qs.JSONValueBuilder.DefaultOnEmpty,
	}
	jsonValueOnError = jsonBehaviors[qs.JSONValueBuilder]{
		where: "JSON_VALUE ON ERROR",
		plain: map[string]func(qs.JSONValueBuilder) qs.JSONValueBuilder{
			jsonBehaviorError: qs.JSONValueBuilder.ErrorOnError,
			jsonBehaviorNull:  qs.JSONValueBuilder.NullOnError,
		},
		value: qs.JSONValueBuilder.DefaultOnError,
	}
	jsonQueryOnEmpty = jsonBehaviors[qs.JSONQueryBuilder]{
		where: "JSON_QUERY ON EMPTY",
		plain: map[string]func(qs.JSONQueryBuilder) qs.JSONQueryBuilder{
			jsonBehaviorError:  qs.JSONQueryBuilder.ErrorOnEmpty,
			jsonBehaviorNull:   qs.JSONQueryBuilder.NullOnEmpty,
			jsonBehaviorArray:  qs.JSONQueryBuilder.EmptyArrayOnEmpty,
			jsonBehaviorObject: qs.JSONQueryBuilder.EmptyObjectOnEmpty,
		},
		value: qs.JSONQueryBuilder.DefaultOnEmpty,
	}
	jsonQueryOnError = jsonBehaviors[qs.JSONQueryBuilder]{
		where: "JSON_QUERY ON ERROR",
		plain: map[string]func(qs.JSONQueryBuilder) qs.JSONQueryBuilder{
			jsonBehaviorError:  qs.JSONQueryBuilder.ErrorOnError,
			jsonBehaviorNull:   qs.JSONQueryBuilder.NullOnError,
			jsonBehaviorArray:  qs.JSONQueryBuilder.EmptyArrayOnError,
			jsonBehaviorObject: qs.JSONQueryBuilder.EmptyObjectOnError,
		},
		value: qs.JSONQueryBuilder.DefaultOnError,
	}
	jsonTableOnError = jsonBehaviors[qs.JSONTableBuilder]{
		where: "JSON_TABLE ON ERROR",
		plain: map[string]func(qs.JSONTableBuilder) qs.JSONTableBuilder{
			jsonBehaviorError: qs.JSONTableBuilder.ErrorOnError,
			jsonBehaviorArray: qs.JSONTableBuilder.EmptyArrayOnError,
		},
	}
	jsonColumnOnEmpty = jsonBehaviors[qs.JSONTableValueColumn]{
		where: "JSON_TABLE column ON EMPTY",
		plain: map[string]func(qs.JSONTableValueColumn) qs.JSONTableValueColumn{
			jsonBehaviorError:  qs.JSONTableValueColumn.ErrorOnEmpty,
			jsonBehaviorNull:   qs.JSONTableValueColumn.NullOnEmpty,
			jsonBehaviorArray:  qs.JSONTableValueColumn.EmptyArrayOnEmpty,
			jsonBehaviorObject: qs.JSONTableValueColumn.EmptyObjectOnEmpty,
		},
		value: qs.JSONTableValueColumn.DefaultOnEmpty,
	}
	jsonColumnOnError = jsonBehaviors[qs.JSONTableValueColumn]{
		where: "JSON_TABLE column ON ERROR",
		plain: map[string]func(qs.JSONTableValueColumn) qs.JSONTableValueColumn{
			jsonBehaviorError:  qs.JSONTableValueColumn.ErrorOnError,
			jsonBehaviorNull:   qs.JSONTableValueColumn.NullOnError,
			jsonBehaviorArray:  qs.JSONTableValueColumn.EmptyArrayOnError,
			jsonBehaviorObject: qs.JSONTableValueColumn.EmptyObjectOnError,
		},
		value: qs.JSONTableValueColumn.DefaultOnError,
	}
	jsonExistsColumnOnError = jsonBehaviors[qs.JSONTableExistsColumn]{
		where: "JSON_TABLE EXISTS column ON ERROR",
		plain: map[string]func(qs.JSONTableExistsColumn) qs.JSONTableExistsColumn{
			jsonBehaviorError:   qs.JSONTableExistsColumn.ErrorOnError,
			jsonBehaviorTrue:    qs.JSONTableExistsColumn.TrueOnError,
			jsonBehaviorFalse:   qs.JSONTableExistsColumn.FalseOnError,
			jsonBehaviorUnknown: qs.JSONTableExistsColumn.UnknownOnError,
		},
	}
)

// jsonPassing applies a PASSING list of JsonArgument nodes.
func jsonPassing[T interface {
	Passing(name string, value qs.JSONOperand) T
}](b *builder, target T, arguments []node) T {
	for _, argument := range arguments {
		target = target.Passing(argument.text("name"), b.jsonOperand(argument.record("val")))
	}
	return target
}

type jsonShaped[T any] interface {
	WithoutWrapper() T
	WithWrapper() T
	WithConditionalWrapper() T
	KeepQuotes() T
	OmitQuotes() T
}

// jsonShape applies the wrapper and quotes of JSON_QUERY or of a JSON_TABLE
// column. The two clauses are independent; rendering refuses the pair
// PostgreSQL rejects.
func jsonShape[T jsonShaped[T]](b *builder, target T, n node) T {
	switch wrapper := n.text("wrapper"); wrapper {
	case "JSW_UNSPEC", "":
	case "JSW_NONE":
		target = target.WithoutWrapper()
	case "JSW_UNCONDITIONAL":
		target = target.WithWrapper()
	case "JSW_CONDITIONAL":
		target = target.WithConditionalWrapper()
	default:
		b.gapf("unsupported SQL/JSON wrapper %s", wrapper)
	}
	switch quotes := n.text("quotes"); quotes {
	case "JS_QUOTES_UNSPEC", "":
	case "JS_QUOTES_KEEP":
		target = target.KeepQuotes()
	case "JS_QUOTES_OMIT":
		target = target.OmitQuotes()
	default:
		b.gapf("unsupported SQL/JSON quotes %s", quotes)
	}
	return target
}

// jsonFunction builds JSON_EXISTS, JSON_VALUE or JSON_QUERY.
func (b *builder) jsonFunction(n node) qs.Expr {
	document := b.jsonOperand(n.record("context_item"))
	path := b.expr(n.node("pathspec"))
	passing := n.nodes("passing")
	switch operation := n.text("op"); operation {
	case "JSON_EXISTS_OP":
		exists := jsonPassing(b, qs.JSONExists(document, path), passing)
		return jsonBehavior(b, exists, n.record("on_error"), jsonExistsOnError).Expr()
	case "JSON_VALUE_OP":
		value := jsonPassing(b, qs.JSONValue(document, path), passing)
		value = jsonPlainOutput(b, value, n.record("output"), "JSON_VALUE")
		value = jsonBehavior(b, value, n.record("on_empty"), jsonValueOnEmpty)
		return jsonBehavior(b, value, n.record("on_error"), jsonValueOnError).Expr()
	case "JSON_QUERY_OP":
		query := jsonPassing(b, qs.JSONQuery(document, path), passing)
		query = jsonOutput(b, query, n.record("output"))
		query = jsonShape(b, query, n)
		query = jsonBehavior(b, query, n.record("on_empty"), jsonQueryOnEmpty)
		return jsonBehavior(b, query, n.record("on_error"), jsonQueryOnError).Expr()
	default:
		b.gapf("unsupported SQL/JSON query function %s", operation)
		return qs.Expr{}
	}
}

// jsonPath reads a JsonTablePathSpec: a string constant and its optional name.
func (b *builder) jsonPath(n node) (path, name string) {
	path, ok := b.stringConstant(n.node("string"))
	if !ok {
		b.gapf("JSON_TABLE path is not a string constant")
	}
	return path, n.text("name")
}

func (b *builder) jsonTable(n node) qs.Relation {
	path, name := b.jsonPath(n.record("pathspec"))
	columns := b.jsonTableColumns(n.nodes("columns"))
	if b.stopped() {
		return qs.Relation{}
	}
	table := variadic(b, "JSON_TABLE", func(first qs.JSONTableColumn, rest ...qs.JSONTableColumn) qs.JSONTableBuilder {
		return qs.JSONTable(b.jsonOperand(n.record("context_item")), path, first, rest...)
	}, columns)
	if name != "" {
		table = table.PathName(name)
	}
	table = jsonPassing(b, table, n.nodes("passing"))
	table = jsonBehavior(b, table, n.record("on_error"), jsonTableOnError)
	return b.lateral(b.aliased(table.Ref(), n), n)
}

func (b *builder) jsonTableColumns(items []node) []qs.JSONTableColumn {
	columns := make([]qs.JSONTableColumn, len(items))
	for index, item := range items {
		columns[index] = b.jsonTableColumn(item)
	}
	return columns
}

func (b *builder) jsonTableColumn(n node) qs.JSONTableColumn {
	name := n.text("name")
	switch kind := n.text("coltype"); kind {
	case "JTC_FOR_ORDINALITY":
		return qs.JSONOrdinality(name)
	case "JTC_REGULAR", "JTC_FORMATTED":
		column := qs.JSONColumn(name, b.dataType(n.record("typeName")))
		switch b.jsonFormat(n.record("format")) {
		case jsonFormatJSON:
			column = column.FormatJSON()
		case jsonFormatJSONUTF8:
			column = column.FormatJSONUTF8()
		case jsonFormatNone:
		}
		if path := n.record("pathspec"); path.present() {
			text, _ := b.jsonPath(path)
			column = column.Path(text)
		}
		column = jsonShape(b, column, n)
		column = jsonBehavior(b, column, n.record("on_empty"), jsonColumnOnEmpty)
		return jsonBehavior(b, column, n.record("on_error"), jsonColumnOnError)
	case "JTC_EXISTS":
		column := qs.JSONExistsColumn(name, b.dataType(n.record("typeName")))
		if path := n.record("pathspec"); path.present() {
			text, _ := b.jsonPath(path)
			column = column.Path(text)
		}
		return jsonBehavior(b, column, n.record("on_error"), jsonExistsColumnOnError)
	case "JTC_NESTED":
		path, pathName := b.jsonPath(n.record("pathspec"))
		nested := variadic(b, "NESTED PATH", func(first qs.JSONTableColumn, rest ...qs.JSONTableColumn) qs.JSONTableNestedColumns {
			return qs.JSONNested(path, first, rest...)
		}, b.jsonTableColumns(n.nodes("columns")))
		if pathName != "" {
			nested = nested.PathName(pathName)
		}
		return nested
	default:
		b.gapf("unsupported JSON_TABLE column kind %s", kind)
		return qs.JSONOrdinality(name)
	}
}
