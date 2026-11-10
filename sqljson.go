package qs

// This file owns the SQL/JSON constructors, aggregates, conversions and the
// IS JSON predicate. Each form is its own builder type whose methods are the
// clauses PostgreSQL's grammar gives that form; the methods several forms
// share are generated into sqljson_methods.go from one table.

// JSONOperand is a value in a SQL/JSON position that accepts FORMAT JSON: an
// expression, a JSON or JSONB document, or either marked with FormatJSON.
type JSONOperand interface{ jsonOperand() jsonOperand }

type jsonFormat uint8

const (
	jsonFormatNone jsonFormat = iota
	jsonFormatJSON
	jsonFormatJSONUTF8
)

type jsonOperand struct {
	expr   Expr
	format jsonFormat
}

func (e Expr) jsonOperand() jsonOperand          { return jsonOperand{expr: e} }
func (d JSONDocument) jsonOperand() jsonOperand  { return jsonOperand{expr: d.expr} }
func (d JSONBDocument) jsonOperand() jsonOperand { return jsonOperand{expr: d.expr} }

// JSONFormatted is an operand marked FORMAT JSON.
type JSONFormatted struct{ operand jsonOperand }

func (f JSONFormatted) jsonOperand() jsonOperand { return f.operand }

// FormatJSON marks a character-string value as JSON text, so that a SQL/JSON
// function reads it as JSON instead of quoting it as a string.
func FormatJSON(value Expr) JSONFormatted {
	return JSONFormatted{jsonOperand{expr: value, format: jsonFormatJSON}}
}

// FormatJSONUTF8 is FormatJSON for a bytea value holding UTF-8 JSON text.
// PostgreSQL accepts ENCODING only for bytea and only UTF8.
func FormatJSONUTF8(value Expr) JSONFormatted {
	return JSONFormatted{jsonOperand{expr: value, format: jsonFormatJSONUTF8}}
}

// operandOf reads a caller's operand. A nil interface or a nil pointer to an
// operand type renders as an invalid expression instead of panicking here.
func operandOf(value JSONOperand) jsonOperand {
	missing := false
	switch v := value.(type) {
	case nil:
		missing = true
	case *Expr:
		missing = v == nil
	case *JSONDocument:
		missing = v == nil
	case *JSONBDocument:
		missing = v == nil
	case *JSONFormatted:
		missing = v == nil
	}
	if missing {
		return jsonOperand{expr: invalidExpr("SQL/JSON", "requires a value")}
	}
	return value.jsonOperand()
}

// jsonNode renders the SQL/JSON expression forms.
func (w *renderer) jsonNode(n any) {
	switch n := n.(type) {
	case *JSONObjectBuilder:
		w.jsonObject(n)
	case *JSONArrayBuilder:
		w.jsonArray(n)
	case *JSONArrayQueryBuilder:
		w.jsonArrayQuery(n)
	case *JSONObjectAggregateBuilder:
		w.jsonObjectAggregate(n)
	case *JSONArrayAggregateBuilder:
		w.jsonArrayAggregate(n)
	case *JSONParseBuilder:
		w.jsonParse(n)
	case *jsonScalarExpression:
		w.text("JSON_SCALAR(")
		w.expr(n.value)
		w.byte(')')
	case *JSONSerializeBuilder:
		w.jsonSerialize(n)
	case *JSONPredicateBuilder:
		w.jsonPredicate(n)
	case *JSONExistsBuilder:
		w.jsonExists(n)
	case *JSONValueBuilder:
		w.jsonValue(n)
	case *JSONQueryBuilder:
		w.jsonQuery(n)
	default:
		w.fail(ErrInvalid, "expression", "unknown expression form")
	}
}

func (w *renderer) jsonFormat(format jsonFormat) {
	switch format {
	case jsonFormatNone:
	case jsonFormatJSON:
		w.text(" FORMAT JSON")
	case jsonFormatJSONUTF8:
		w.text(" FORMAT JSON ENCODING UTF8")
	}
}

func (w *renderer) jsonOperand(v jsonOperand) {
	w.expr(v.expr)
	w.jsonFormat(v.format)
}

// jsonOutput is a RETURNING clause. FORMAT JSON belongs to RETURNING, so one
// value states both and a format cannot exist without its type.
type jsonOutput struct {
	typ      DataType
	format   jsonFormat
	returned bool
}

func returning(typ DataType, format jsonFormat) jsonOutput {
	return jsonOutput{typ: typ, format: format, returned: true}
}

// jsonOutput writes the clause, after a space when it follows other content.
func (w *renderer) jsonOutput(o jsonOutput, separated bool) {
	if !o.returned || w.err != nil {
		return
	}
	if separated {
		w.byte(' ')
	}
	w.text("RETURNING ")
	w.dataType(o.typ)
	w.jsonFormat(o.format)
}

// jsonNulls is how a constructor treats a NULL value. Objects default to NULL
// ON NULL and arrays to ABSENT ON NULL.
type jsonNulls uint8

const (
	jsonNullsDefault jsonNulls = iota
	jsonNullOnNull
	jsonAbsentOnNull
)

func (w *renderer) jsonNulls(nulls jsonNulls) {
	switch nulls {
	case jsonNullsDefault:
	case jsonNullOnNull:
		w.text(" NULL ON NULL")
	case jsonAbsentOnNull:
		w.text(" ABSENT ON NULL")
	}
}

type jsonUniqueness uint8

const (
	jsonUniquenessDefault jsonUniqueness = iota
	jsonWithUniqueKeys
	jsonWithoutUniqueKeys
)

func (w *renderer) jsonUniqueness(unique jsonUniqueness) {
	switch unique {
	case jsonUniquenessDefault:
	case jsonWithUniqueKeys:
		w.text(" WITH UNIQUE KEYS")
	case jsonWithoutUniqueKeys:
		w.text(" WITHOUT UNIQUE KEYS")
	}
}

// JSONMember is one key and value of JSON_OBJECT.
type JSONMember struct {
	key   Expr
	value jsonOperand
}

// JSONPair pairs a JSON_OBJECT key with its value.
func JSONPair(key Expr, value JSONOperand) JSONMember {
	return JSONMember{key: key, value: operandOf(value)}
}

// JSONObjectBuilder is a JSON_OBJECT constructor. It is an immutable value.
type JSONObjectBuilder struct {
	members []JSONMember
	output  jsonOutput
	nulls   jsonNulls
	unique  jsonUniqueness
}

// JSONObject constructs JSON_OBJECT from key and value pairs. With no members
// it is the empty object, which accepts RETURNING only: a NULL or uniqueness
// clause on it fails rendering.
func JSONObject(members ...JSONMember) JSONObjectBuilder {
	return JSONObjectBuilder{members: cloneSlice(members)}
}

// Members appends key and value pairs.
func (b JSONObjectBuilder) Members(members ...JSONMember) JSONObjectBuilder {
	b.members = extend(b.members, members...)
	return b
}

func (w *renderer) jsonObject(b *JSONObjectBuilder) {
	const clause = "JSON_OBJECT"
	if len(b.members) == 0 && !w.require(b.nulls == jsonNullsDefault && b.unique == jsonUniquenessDefault, clause, "an empty constructor accepts RETURNING only") {
		return
	}
	w.text("JSON_OBJECT(")
	for i, member := range b.members {
		if i != 0 {
			w.text(", ")
		}
		w.expr(member.key)
		w.text(" : ")
		w.jsonOperand(member.value)
		if w.stopped("member", i+1) {
			return
		}
	}
	w.jsonNulls(b.nulls)
	w.jsonUniqueness(b.unique)
	w.jsonOutput(b.output, len(b.members) != 0)
	w.byte(')')
}

// JSONArrayBuilder is a JSON_ARRAY constructor over a value list. It is an
// immutable value.
type JSONArrayBuilder struct {
	values []jsonOperand
	output jsonOutput
	nulls  jsonNulls
}

// JSONArray constructs JSON_ARRAY from values. With no values it is the empty
// array, which accepts RETURNING only: a NULL clause on it fails rendering.
func JSONArray(values ...JSONOperand) JSONArrayBuilder {
	return JSONArrayBuilder{}.Values(values...)
}

// Values appends array elements.
func (b JSONArrayBuilder) Values(values ...JSONOperand) JSONArrayBuilder {
	b.values = extendWith(b.values, values, operandOf)
	return b
}

func (w *renderer) jsonArray(b *JSONArrayBuilder) {
	const clause = "JSON_ARRAY"
	if len(b.values) == 0 && !w.require(b.nulls == jsonNullsDefault, clause, "an empty constructor accepts RETURNING only") {
		return
	}
	w.text("JSON_ARRAY(")
	for i, value := range b.values {
		if i != 0 {
			w.text(", ")
		}
		w.jsonOperand(value)
		if w.stopped("value", i+1) {
			return
		}
	}
	w.jsonNulls(b.nulls)
	w.jsonOutput(b.output, len(b.values) != 0)
	w.byte(')')
}

// JSONArrayQueryBuilder is a JSON_ARRAY constructor over a query. It is an
// immutable value.
type JSONArrayQueryBuilder struct {
	query  Rowset
	output jsonOutput
	format jsonFormat
}

// JSONArrayQuery constructs JSON_ARRAY from the rows of a query, which must
// return exactly one column. A known width other than one fails rendering.
func JSONArrayQuery(query Rowset) JSONArrayQueryBuilder {
	return JSONArrayQueryBuilder{query: ownedRowset(query)}
}

// FormatJSON reads the query's column as JSON text.
func (b JSONArrayQueryBuilder) FormatJSON() JSONArrayQueryBuilder {
	b.format = jsonFormatJSON
	return b
}

// FormatJSONUTF8 is FormatJSON for a bytea column holding UTF-8 JSON text.
func (b JSONArrayQueryBuilder) FormatJSONUTF8() JSONArrayQueryBuilder {
	b.format = jsonFormatJSONUTF8
	return b
}

func (w *renderer) jsonArrayQuery(b *JSONArrayQueryBuilder) {
	const clause = "JSON_ARRAY"
	if !w.require(b.query != nil, clause, "requires a query") {
		return
	}
	if width := statementWidth(b.query, w.options.MaxDepth); width >= 0 && !w.require(width == 1, clause, "query must return exactly one column") {
		return
	}
	w.text("JSON_ARRAY(")
	w.statement(b.query)
	if w.stopped("query", 0) {
		return
	}
	w.jsonFormat(b.format)
	w.jsonOutput(b.output, true)
	w.byte(')')
}

// JSONObjectAggregateBuilder is a JSON_OBJECTAGG aggregate. It is an immutable
// value.
type JSONObjectAggregateBuilder struct {
	key    Expr
	tail   aggregateTail
	value  jsonOperand
	output jsonOutput
	nulls  jsonNulls
	unique jsonUniqueness
}

// JSONObjectAggregate constructs JSON_OBJECTAGG, the SQL-standard aggregate
// that collects key and value pairs into one object. JSONObjectAgg and
// JSONBObjectAgg call PostgreSQL's json_object_agg functions instead.
func JSONObjectAggregate(key Expr, value JSONOperand) JSONObjectAggregateBuilder {
	return JSONObjectAggregateBuilder{key: key, value: operandOf(value)}
}

func (w *renderer) jsonObjectAggregate(b *JSONObjectAggregateBuilder) {
	w.text("JSON_OBJECTAGG(")
	w.expr(b.key)
	w.text(" : ")
	w.jsonOperand(b.value)
	w.jsonNulls(b.nulls)
	w.jsonUniqueness(b.unique)
	w.jsonOutput(b.output, true)
	w.byte(')')
	w.aggregateTail(b.tail, false)
}

// JSONArrayAggregateBuilder is a JSON_ARRAYAGG aggregate. It is an immutable
// value.
type JSONArrayAggregateBuilder struct {
	order  []Order
	tail   aggregateTail
	value  jsonOperand
	output jsonOutput
	nulls  jsonNulls
}

// JSONArrayAggregate constructs JSON_ARRAYAGG, the SQL-standard aggregate that
// collects values into one array. JSONAgg and JSONBAgg call PostgreSQL's
// json_agg functions instead.
func JSONArrayAggregate(value JSONOperand) JSONArrayAggregateBuilder {
	return JSONArrayAggregateBuilder{value: operandOf(value)}
}

// OrderBy appends terms to the ORDER BY that fixes the element order.
// PostgreSQL does not implement it for a window function, so it fails
// rendering together with Over or OverNamed.
func (b JSONArrayAggregateBuilder) OrderBy(terms ...Order) JSONArrayAggregateBuilder {
	b.order = extend(b.order, terms...)
	return b
}

func (w *renderer) jsonArrayAggregate(b *JSONArrayAggregateBuilder) {
	w.text("JSON_ARRAYAGG(")
	w.jsonOperand(b.value)
	if len(b.order) != 0 {
		w.text(" ORDER BY ")
		w.orders(b.order)
		if w.stopped("ORDER BY", 0) {
			return
		}
	}
	w.jsonNulls(b.nulls)
	w.jsonOutput(b.output, true)
	w.byte(')')
	w.aggregateTail(b.tail, len(b.order) != 0)
}

// JSONParseBuilder is the JSON(...) conversion of text to a json value. It is
// an immutable value.
type JSONParseBuilder struct {
	value  jsonOperand
	unique jsonUniqueness
}

// JSONParse constructs JSON(value), which parses text or bytea as JSON.
func JSONParse(value JSONOperand) JSONParseBuilder {
	return JSONParseBuilder{value: operandOf(value)}
}

func (w *renderer) jsonParse(b *JSONParseBuilder) {
	w.text("JSON(")
	w.jsonOperand(b.value)
	w.jsonUniqueness(b.unique)
	w.byte(')')
}

type jsonScalarExpression struct{ value Expr }

func (*jsonScalarExpression) exprNode() {}

// JSONScalar constructs JSON_SCALAR(value), which converts a SQL scalar to a
// JSON scalar.
func JSONScalar(value Expr) Expr { return nodeExpr(&jsonScalarExpression{value}) }

// JSONSerializeBuilder is a JSON_SERIALIZE conversion of a JSON value to text
// or bytea. It is an immutable value.
type JSONSerializeBuilder struct {
	value  jsonOperand
	output jsonOutput
}

// JSONSerialize constructs JSON_SERIALIZE(value).
func JSONSerialize(value JSONOperand) JSONSerializeBuilder {
	return JSONSerializeBuilder{value: operandOf(value)}
}

func (w *renderer) jsonSerialize(b *JSONSerializeBuilder) {
	w.text("JSON_SERIALIZE(")
	w.jsonOperand(b.value)
	w.jsonOutput(b.output, true)
	w.byte(')')
}

type jsonItem uint8

const (
	jsonItemAny jsonItem = iota
	jsonItemScalar
	jsonItemArray
	jsonItemObject
)

// JSONPredicateBuilder is an IS JSON predicate. It is an immutable value.
type JSONPredicateBuilder struct {
	value  Expr
	item   jsonItem
	unique jsonUniqueness
}

// IsJSON tests whether value is well-formed JSON. Negate it with
// Condition().Not().
func IsJSON(value Expr) JSONPredicateBuilder { return JSONPredicateBuilder{value: value} }

// Scalar narrows the test to a JSON scalar, replacing an earlier item type.
func (b JSONPredicateBuilder) Scalar() JSONPredicateBuilder { b.item = jsonItemScalar; return b }

// Array narrows the test to a JSON array, replacing an earlier item type.
func (b JSONPredicateBuilder) Array() JSONPredicateBuilder { b.item = jsonItemArray; return b }

// Object narrows the test to a JSON object, replacing an earlier item type.
func (b JSONPredicateBuilder) Object() JSONPredicateBuilder { b.item = jsonItemObject; return b }

func (w *renderer) jsonPredicate(b *JSONPredicateBuilder) {
	w.byte('(')
	w.expr(b.value)
	w.text(" IS JSON")
	switch b.item {
	case jsonItemAny:
	case jsonItemScalar:
		w.text(" SCALAR")
	case jsonItemArray:
		w.text(" ARRAY")
	case jsonItemObject:
		w.text(" OBJECT")
	}
	w.jsonUniqueness(b.unique)
	w.byte(')')
}
