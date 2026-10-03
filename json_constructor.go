package qs

import "slices"

// JSONInputValue is the SQL/JSON value role accepted by the constructor
// builders. Expr values are accepted directly; JSONConstructorInput adds an
// explicit FORMAT JSON clause without exposing raw SQL syntax to callers.
// Constructors validate the concrete role at runtime, so a foreign type cannot
// contribute an arbitrary descriptor by embedding one of these known types.
type JSONInputValue interface {
	jsonInputValue()
}

type jsonConstructorValue struct {
	expr     Expr
	format   bool
	encoding JSONEncoding
}

// JSONConstructorInput is an input value descriptor for SQL/JSON constructors.
// Construct it with JSONInputExpr; its fields are intentionally private so callers
// cannot smuggle SQL syntax into a FORMAT JSON role.
type JSONConstructorInput struct {
	value jsonConstructorValue
}

// JSONInputExpr marks an expression as a SQL/JSON input value. Expressions may be
// passed directly where JSONInputValue is accepted; use this descriptor when
// the SQL/JSON grammar needs FORMAT JSON or an encoding clause.
func JSONInputExpr(value Expr) JSONConstructorInput {
	return JSONConstructorInput{value: jsonConstructorValue{expr: value}}
}

func (Expr) jsonInputValue() {}

func (JSONConstructorInput) jsonInputValue() {}

func constructorInputValue(value JSONInputValue) jsonConstructorValue {
	invalid := func(detail string) jsonConstructorValue {
		return jsonConstructorValue{expr: invalidExpr("SQL/JSON", detail)}
	}
	switch value := value.(type) {
	case Expr:
		return jsonConstructorValue{expr: value}
	case *Expr:
		if value == nil {
			return invalid("nil input value")
		}
		return jsonConstructorValue{expr: *value}
	case JSONConstructorInput:
		return value.value
	case *JSONConstructorInput:
		if value == nil {
			return invalid("nil input value")
		}
		return value.value
	default:
		// Do not invoke JSONInputValue's unexported method here. A foreign type
		// can satisfy the interface by embedding Expr or JSONConstructorInput,
		// but it must not smuggle an arbitrary descriptor into this grammar role.
		return invalid("unsupported input value type")
	}
}

// FormatJSON marks the input as already formatted JSON. It also enables the
// encoding clause, as required by PostgreSQL's SQL/JSON grammar.
func (v JSONConstructorInput) FormatJSON() JSONConstructorInput {
	v.value.format = true
	return v
}

// EncodingUTF8 emits FORMAT JSON ENCODING UTF8 for this input. PostgreSQL
// validates whether the input type supports the encoding clause.
func (v JSONConstructorInput) EncodingUTF8() JSONConstructorInput {
	v.value.format = true
	v.value.encoding = JSONEncodingUTF8
	return v
}

// Encoding sets FORMAT JSON ENCODING for this input. PostgreSQL accepts UTF8,
// UTF16, and UTF32 in the grammar; the server validates whether the selected
// encoding is supported for the input type.
func (v JSONConstructorInput) Encoding(encoding JSONEncoding) JSONConstructorInput {
	v.value.format = true
	v.value.encoding = encoding
	return v
}

// JSONMember is one key/value entry in JSON_OBJECT or JSON_OBJECTAGG. Use
// JSONPair to construct it; the fields are private to preserve value roles.
type JSONMember struct {
	key   Expr
	value jsonConstructorValue
}

// JSONPair constructs a JSON object key/value entry. The key is an ordinary
// expression; FORMAT JSON applies only to the value role.
func JSONPair(key Expr, value JSONInputValue) JSONMember {
	return JSONMember{key: key, value: constructorInputValue(value)}
}

type jsonConstructorKind uint8

const (
	jsonObjectConstructor jsonConstructorKind = iota + 1
	jsonArrayConstructor
	jsonObjectAggregate
	jsonArrayAggregate
	jsonArrayQueryConstructor
	jsonParseConstructor
	jsonScalarConstructor
	jsonSerializeConstructor
	jsonIsPredicate
)

// jsonConstructor is the single tagged expression payload for native SQL/JSON
// constructors. Its concrete payload is one of the constructor structs below.
type jsonConstructor struct {
	payload any
	kind    jsonConstructorKind
}

type jsonConstructorOutput struct {
	returning    DataType
	hasReturning bool
	format       bool
	encoding     JSONEncoding
}

// JSONEncoding is one of PostgreSQL's SQL/JSON encoding names. Its zero value
// omits ENCODING and lets PostgreSQL select UTF8 as the default.
type JSONEncoding uint8

const (
	// JSONEncodingDefault omits an ENCODING clause and lets PostgreSQL use its default.
	JSONEncodingDefault JSONEncoding = iota
	// JSONEncodingUTF8 selects UTF8 for a FORMAT JSON value.
	JSONEncodingUTF8
	// JSONEncodingUTF16 selects UTF16 for a FORMAT JSON value.
	JSONEncodingUTF16
	// JSONEncodingUTF32 selects UTF32 for a FORMAT JSON value.
	JSONEncodingUTF32
)

func (e JSONEncoding) valid() bool { return e <= JSONEncodingUTF32 }

func (e JSONEncoding) name() string {
	switch e {
	case JSONEncodingUTF8:
		return "UTF8"
	case JSONEncodingUTF16:
		return "UTF16"
	case JSONEncodingUTF32:
		return "UTF32"
	default:
		return ""
	}
}

type jsonNullPolicy uint8

const (
	jsonNullDefault jsonNullPolicy = iota
	jsonNullOnNull
	jsonAbsentOnNull
)

type jsonKeyUniqueness uint8

const (
	jsonUniqueDefault jsonKeyUniqueness = iota
	jsonWithUniqueKeys
	jsonWithoutUniqueKeys
)

type jsonObjectConstructorPayload struct {
	members []JSONMember
	output  jsonConstructorOutput
	nulls   jsonNullPolicy
	unique  jsonKeyUniqueness
}

type jsonArrayConstructorPayload struct {
	values []jsonConstructorValue
	output jsonConstructorOutput
	nulls  jsonNullPolicy
}

type jsonObjectAggregatePayload struct {
	key     jsonConstructorValue
	value   jsonConstructorValue
	tail    aggregateTail
	invalid string
	output  jsonConstructorOutput
	nulls   jsonNullPolicy
	unique  jsonKeyUniqueness
}

type jsonArrayAggregatePayload struct {
	invalid string
	tail    aggregateTail
	order   []Order
	value   jsonConstructorValue
	output  jsonConstructorOutput
	nulls   jsonNullPolicy
}

type jsonArrayQueryPayload struct {
	query         Rowset
	output        jsonConstructorOutput
	format        bool
	inputEncoding JSONEncoding
}

type jsonParsePayload struct {
	value  jsonConstructorValue
	unique jsonKeyUniqueness
}

type jsonScalarPayload struct{ value Expr }

type jsonSerializePayload struct {
	value  jsonConstructorValue
	output jsonConstructorOutput
}

type jsonIsPredicatePayload struct {
	value  Expr
	item   jsonPredicateItem
	unique jsonKeyUniqueness
	not    bool
}

// JSONPredicateItem selects the JSON item kind checked by IS JSON.
type JSONPredicateItem uint8

const (
	// JSONPredicateAny accepts any valid JSON item in an IS JSON predicate.
	JSONPredicateAny JSONPredicateItem = iota
	// JSONPredicateValue restricts an IS JSON predicate to JSON values.
	JSONPredicateValue
	// JSONPredicateScalar restricts an IS JSON predicate to JSON scalars.
	JSONPredicateScalar
	// JSONPredicateArray restricts an IS JSON predicate to JSON arrays.
	JSONPredicateArray
	// JSONPredicateObject restricts an IS JSON predicate to JSON objects.
	JSONPredicateObject
)

// JSONNullPolicy is the explicit NULL handling policy accepted by object and
// array constructors and aggregates. The zero value leaves PostgreSQL's
// constructor default in place (NULL for objects, ABSENT for arrays).
type JSONNullPolicy uint8

const (
	// JSONNullDefault omits an ON NULL clause and uses the PostgreSQL default.
	JSONNullDefault JSONNullPolicy = iota
	// JSONNullOnNull retains entries whose value is SQL NULL.
	JSONNullOnNull
	// JSONAbsentOnNull omits entries whose value is SQL NULL.
	JSONAbsentOnNull
)

// JSONKeyUniqueness selects duplicate-key handling for object constructors,
// object aggregates, IS JSON, and JSON parsing. The zero value omits the
// clause and retains PostgreSQL's default.
type JSONKeyUniqueness uint8

const (
	// JSONUniqueDefault omits a key uniqueness clause and uses the PostgreSQL default.
	JSONUniqueDefault JSONKeyUniqueness = iota
	// JSONWithUniqueKeys rejects duplicate object keys.
	JSONWithUniqueKeys
	// JSONWithoutUniqueKeys permits duplicate object keys.
	JSONWithoutUniqueKeys
)

func (p JSONNullPolicy) internal() jsonNullPolicy { return jsonNullPolicy(p) }

func (i JSONPredicateItem) internal() jsonPredicateItem { return jsonPredicateItem(i) }

type jsonPredicateItem uint8

const (
	jsonPredicateAny jsonPredicateItem = iota
	jsonPredicateValue
	jsonPredicateScalar
	jsonPredicateArray
	jsonPredicateObject
)

// JSONObjectBuilder constructs PostgreSQL's SQL/JSON JSON_OBJECT constructor.
// It is immutable: list and option methods return a branched descriptor.
type JSONObjectBuilder struct {
	payload jsonObjectConstructorPayload
}

// JSONObject constructs a JSON object from key/value entries. Object values
// default to NULL ON NULL according to PostgreSQL's grammar.
func JSONObject(members ...JSONMember) JSONObjectBuilder {
	return JSONObjectBuilder{payload: jsonObjectConstructorPayload{members: cloneSlice(members)}}
}

// Members appends key/value entries to the JSON object constructor.
func (b JSONObjectBuilder) Members(members ...JSONMember) JSONObjectBuilder {
	b.payload.members = slices.Concat(b.payload.members, members)
	return b
}

// OnNull selects how JSON_OBJECT handles SQL NULL values.
func (b JSONObjectBuilder) OnNull(policy JSONNullPolicy) JSONObjectBuilder {
	b.payload.nulls = policy.internal()
	return b
}

// NullOnNull makes JSON_OBJECT retain entries whose values are SQL NULL.
func (b JSONObjectBuilder) NullOnNull() JSONObjectBuilder {
	return b.OnNull(JSONNullOnNull)
}

// AbsentOnNull makes JSON_OBJECT omit entries whose values are SQL NULL.
func (b JSONObjectBuilder) AbsentOnNull() JSONObjectBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

// WithUniqueKeys makes JSON_OBJECT reject duplicate object keys.
func (b JSONObjectBuilder) WithUniqueKeys() JSONObjectBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

// WithoutUniqueKeys makes JSON_OBJECT permit duplicate object keys.
func (b JSONObjectBuilder) WithoutUniqueKeys() JSONObjectBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

// UniqueKeys selects whether JSON_OBJECT enforces unique object keys.
func (b JSONObjectBuilder) UniqueKeys(unique bool) JSONObjectBuilder {
	if unique {
		return b.WithUniqueKeys()
	}
	return b.WithoutUniqueKeys()
}

// Returning selects the SQL type returned by JSON_OBJECT.
func (b JSONObjectBuilder) Returning(typ DataType) JSONObjectBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the JSON_OBJECT result as formatted JSON.
func (b JSONObjectBuilder) FormatJSON() JSONObjectBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted JSON_OBJECT result.
func (b JSONObjectBuilder) EncodingUTF8() JSONObjectBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted JSON_OBJECT result.
func (b JSONObjectBuilder) Encoding(encoding JSONEncoding) JSONObjectBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Expr freezes the JSON_OBJECT builder as an expression.
func (b JSONObjectBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonObjectConstructor, &p)
}

// As freezes the JSON_OBJECT builder as an aliased expression.
func (b JSONObjectBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONArrayBuilder constructs PostgreSQL's SQL/JSON JSON_ARRAY constructor.
type JSONArrayBuilder struct {
	payload jsonArrayConstructorPayload
}

// JSONArray constructs a JSON array from value expressions. Array values
// default to ABSENT ON NULL according to PostgreSQL's grammar.
func JSONArray(values ...JSONInputValue) JSONArrayBuilder {
	b := JSONArrayBuilder{}
	return b.Values(values...)
}

// Values appends input values to the JSON_ARRAY constructor.
func (b JSONArrayBuilder) Values(values ...JSONInputValue) JSONArrayBuilder {
	if len(values) == 0 {
		return b
	}
	oldLen := len(b.payload.values)
	items := make([]jsonConstructorValue, oldLen+len(values))
	copy(items, b.payload.values)
	for i, value := range values {
		items[oldLen+i] = constructorInputValue(value)
	}
	b.payload.values = items
	return b
}

// OnNull selects how JSON_ARRAY handles SQL NULL values.
func (b JSONArrayBuilder) OnNull(policy JSONNullPolicy) JSONArrayBuilder {
	b.payload.nulls = policy.internal()
	return b
}

// NullOnNull makes JSON_ARRAY retain values that are SQL NULL.
func (b JSONArrayBuilder) NullOnNull() JSONArrayBuilder {
	return b.OnNull(JSONNullOnNull)
}

// AbsentOnNull makes JSON_ARRAY omit values that are SQL NULL.
func (b JSONArrayBuilder) AbsentOnNull() JSONArrayBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

// Returning selects the SQL type returned by JSON_ARRAY.
func (b JSONArrayBuilder) Returning(typ DataType) JSONArrayBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the JSON_ARRAY result as formatted JSON.
func (b JSONArrayBuilder) FormatJSON() JSONArrayBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted JSON_ARRAY result.
func (b JSONArrayBuilder) EncodingUTF8() JSONArrayBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted JSON_ARRAY result.
func (b JSONArrayBuilder) Encoding(encoding JSONEncoding) JSONArrayBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Expr freezes the JSON_ARRAY builder as an expression.
func (b JSONArrayBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayConstructor, &p)
}

// As freezes the JSON_ARRAY builder as an aliased expression.
func (b JSONArrayBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONArrayQueryBuilder constructs JSON_ARRAY(SELECT ...), whose PostgreSQL
// grammar always omits NULL rows and does not permit a NULL policy clause.
type JSONArrayQueryBuilder struct {
	payload jsonArrayQueryPayload
}

// JSONArrayQuery constructs the query form of JSON_ARRAY from a one-column rowset.
func JSONArrayQuery(query Rowset) JSONArrayQueryBuilder {
	return JSONArrayQueryBuilder{payload: jsonArrayQueryPayload{query: query}}
}

// InputFormatJSON marks the rowset result as already formatted JSON.
func (b JSONArrayQueryBuilder) InputFormatJSON() JSONArrayQueryBuilder {
	b.payload.format = true
	return b
}

// InputEncoding marks the rowset's single value as FORMAT JSON with a named
// encoding. It is separate from Encoding, which controls the constructor's
// RETURNING output format.
func (b JSONArrayQueryBuilder) InputEncoding(encoding JSONEncoding) JSONArrayQueryBuilder {
	b.payload.format = true
	b.payload.inputEncoding = encoding
	return b
}

// Returning selects the SQL type returned by the query form of JSON_ARRAY.
func (b JSONArrayQueryBuilder) Returning(typ DataType) JSONArrayQueryBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the query form of JSON_ARRAY as formatted JSON.
func (b JSONArrayQueryBuilder) FormatJSON() JSONArrayQueryBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted query result.
func (b JSONArrayQueryBuilder) EncodingUTF8() JSONArrayQueryBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted query result.
func (b JSONArrayQueryBuilder) Encoding(encoding JSONEncoding) JSONArrayQueryBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Expr freezes the query form of JSON_ARRAY as an expression.
func (b JSONArrayQueryBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayQueryConstructor, &p)
}

// As freezes the query form of JSON_ARRAY as an aliased expression.
func (b JSONArrayQueryBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONObjectAggregateBuilder constructs JSON_OBJECTAGG. JSON_OBJECTAGG has
// FILTER and OVER tails but no in-call ORDER BY, DISTINCT, or WITHIN GROUP.
type JSONObjectAggregateBuilder struct {
	payload jsonObjectAggregatePayload
}

// JSONObjectAggregate constructs a JSON_OBJECTAGG builder from a key and value.
func JSONObjectAggregate(key Expr, value JSONInputValue) JSONObjectAggregateBuilder {
	return JSONObjectAggregateBuilder{payload: jsonObjectAggregatePayload{
		key:   jsonConstructorValue{expr: key},
		value: constructorInputValue(value),
	}}
}

// OnNull selects how JSON_OBJECTAGG handles SQL NULL values.
func (b JSONObjectAggregateBuilder) OnNull(policy JSONNullPolicy) JSONObjectAggregateBuilder {
	b.payload.nulls = policy.internal()
	return b
}

// NullOnNull makes JSON_OBJECTAGG retain rows whose values are SQL NULL.
func (b JSONObjectAggregateBuilder) NullOnNull() JSONObjectAggregateBuilder {
	return b.OnNull(JSONNullOnNull)
}

// AbsentOnNull makes JSON_OBJECTAGG omit rows whose values are SQL NULL.
func (b JSONObjectAggregateBuilder) AbsentOnNull() JSONObjectAggregateBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

// WithUniqueKeys makes JSON_OBJECTAGG reject duplicate object keys.
func (b JSONObjectAggregateBuilder) WithUniqueKeys() JSONObjectAggregateBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

// WithoutUniqueKeys makes JSON_OBJECTAGG permit duplicate object keys.
func (b JSONObjectAggregateBuilder) WithoutUniqueKeys() JSONObjectAggregateBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

// Returning selects the SQL type returned by JSON_OBJECTAGG.
func (b JSONObjectAggregateBuilder) Returning(typ DataType) JSONObjectAggregateBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the JSON_OBJECTAGG result as formatted JSON.
func (b JSONObjectAggregateBuilder) FormatJSON() JSONObjectAggregateBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted JSON_OBJECTAGG result.
func (b JSONObjectAggregateBuilder) EncodingUTF8() JSONObjectAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted JSON_OBJECTAGG result.
func (b JSONObjectAggregateBuilder) Encoding(encoding JSONEncoding) JSONObjectAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Filter adds conditions to the JSON_OBJECTAGG FILTER clause.
func (b JSONObjectAggregateBuilder) Filter(conditions ...Condition) JSONObjectAggregateBuilder {
	b.payload.tail.filter = slices.Concat(b.payload.tail.filter, conditions)
	return b
}

// Over attaches a window specification to JSON_OBJECTAGG.
func (b JSONObjectAggregateBuilder) Over(window WindowSpec) JSONObjectAggregateBuilder {
	b.payload.tail.window = &window
	b.payload.tail.windowName = ""
	b.payload.invalid = ""
	return b
}

// OverNamed attaches a named window to JSON_OBJECTAGG. An empty name records a
// render-time validation error.
func (b JSONObjectAggregateBuilder) OverNamed(name string) JSONObjectAggregateBuilder {
	if name == "" {
		b.payload.invalid = "OVER requires a non-empty window name"
		return b
	}
	b.payload.tail.windowName = name
	b.payload.tail.window = nil
	b.payload.invalid = ""
	return b
}

// Expr freezes the JSON_OBJECTAGG builder as an expression.
func (b JSONObjectAggregateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonObjectAggregate, &p)
}

// As freezes the JSON_OBJECTAGG builder as an aliased expression.
func (b JSONObjectAggregateBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONArrayAggregateBuilder constructs JSON_ARRAYAGG. It supports
// PostgreSQL's in-call ORDER BY plus FILTER and OVER tails.
type JSONArrayAggregateBuilder struct {
	payload jsonArrayAggregatePayload
}

// JSONArrayAggregate constructs a JSON_ARRAYAGG builder from one input value.
func JSONArrayAggregate(value JSONInputValue) JSONArrayAggregateBuilder {
	return JSONArrayAggregateBuilder{payload: jsonArrayAggregatePayload{value: constructorInputValue(value)}}
}

// OrderBy appends in-call ordering terms to JSON_ARRAYAGG.
func (b JSONArrayAggregateBuilder) OrderBy(terms ...Order) JSONArrayAggregateBuilder {
	b.payload.order = slices.Concat(b.payload.order, terms)
	return b
}

// OnNull selects how JSON_ARRAYAGG handles SQL NULL values.
func (b JSONArrayAggregateBuilder) OnNull(policy JSONNullPolicy) JSONArrayAggregateBuilder {
	b.payload.nulls = policy.internal()
	return b
}

// NullOnNull makes JSON_ARRAYAGG retain rows whose values are SQL NULL.
func (b JSONArrayAggregateBuilder) NullOnNull() JSONArrayAggregateBuilder {
	return b.OnNull(JSONNullOnNull)
}

// AbsentOnNull makes JSON_ARRAYAGG omit rows whose values are SQL NULL.
func (b JSONArrayAggregateBuilder) AbsentOnNull() JSONArrayAggregateBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

// Returning selects the SQL type returned by JSON_ARRAYAGG.
func (b JSONArrayAggregateBuilder) Returning(typ DataType) JSONArrayAggregateBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the JSON_ARRAYAGG result as formatted JSON.
func (b JSONArrayAggregateBuilder) FormatJSON() JSONArrayAggregateBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted JSON_ARRAYAGG result.
func (b JSONArrayAggregateBuilder) EncodingUTF8() JSONArrayAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted JSON_ARRAYAGG result.
func (b JSONArrayAggregateBuilder) Encoding(encoding JSONEncoding) JSONArrayAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Filter adds conditions to the JSON_ARRAYAGG FILTER clause.
func (b JSONArrayAggregateBuilder) Filter(conditions ...Condition) JSONArrayAggregateBuilder {
	b.payload.tail.filter = slices.Concat(b.payload.tail.filter, conditions)
	return b
}

// Over attaches a window specification to JSON_ARRAYAGG.
func (b JSONArrayAggregateBuilder) Over(window WindowSpec) JSONArrayAggregateBuilder {
	b.payload.tail.window = &window
	b.payload.tail.windowName = ""
	b.payload.invalid = ""
	return b
}

// OverNamed attaches a named window to JSON_ARRAYAGG. An empty name records a
// render-time validation error.
func (b JSONArrayAggregateBuilder) OverNamed(name string) JSONArrayAggregateBuilder {
	if name == "" {
		b.payload.invalid = "OVER requires a non-empty window name"
		return b
	}
	b.payload.tail.windowName = name
	b.payload.tail.window = nil
	b.payload.invalid = ""
	return b
}

// Expr freezes the JSON_ARRAYAGG builder as an expression.
func (b JSONArrayAggregateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayAggregate, &p)
}

// As freezes the JSON_ARRAYAGG builder as an aliased expression.
func (b JSONArrayAggregateBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONParseBuilder constructs PostgreSQL's JSON(...) parser/converter.
type JSONParseBuilder struct {
	payload jsonParsePayload
}

// JSONParse constructs a JSON parser/converter builder from an input value.
func JSONParse(value JSONInputValue) JSONParseBuilder {
	return JSONParseBuilder{payload: jsonParsePayload{value: constructorInputValue(value)}}
}

// WithUniqueKeys makes JSON reject duplicate object keys while parsing.
func (b JSONParseBuilder) WithUniqueKeys() JSONParseBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

// WithoutUniqueKeys makes JSON permit duplicate object keys while parsing.
func (b JSONParseBuilder) WithoutUniqueKeys() JSONParseBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

// Expr freezes the JSON parser builder as an expression.
func (b JSONParseBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonParseConstructor, &p)
}

// As freezes the JSON parser builder as an aliased expression.
func (b JSONParseBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONScalar converts one ordinary SQL scalar expression to JSON. PostgreSQL's
// JSON_SCALAR grammar has no FORMAT, RETURNING, or aggregate tail clauses.
func JSONScalar(value Expr) Expr {
	return jsonConstructorExpr(jsonScalarConstructor, &jsonScalarPayload{value: value})
}

// JSONSerializeBuilder constructs JSON_SERIALIZE. Input FORMAT JSON belongs to
// JSONConstructorInput; RETURNING FORMAT JSON belongs to this builder's output.
type JSONSerializeBuilder struct {
	payload jsonSerializePayload
}

// JSONSerialize constructs a JSON_SERIALIZE builder from an input value.
func JSONSerialize(value JSONInputValue) JSONSerializeBuilder {
	return JSONSerializeBuilder{payload: jsonSerializePayload{value: constructorInputValue(value)}}
}

// Returning selects the SQL type returned by JSON_SERIALIZE.
func (b JSONSerializeBuilder) Returning(typ DataType) JSONSerializeBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

// FormatJSON marks the JSON_SERIALIZE result as formatted JSON.
func (b JSONSerializeBuilder) FormatJSON() JSONSerializeBuilder {
	b.payload.output.format = true
	return b
}

// EncodingUTF8 selects UTF8 for the formatted JSON_SERIALIZE result.
func (b JSONSerializeBuilder) EncodingUTF8() JSONSerializeBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

// Encoding selects the encoding for the formatted JSON_SERIALIZE result.
func (b JSONSerializeBuilder) Encoding(encoding JSONEncoding) JSONSerializeBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

// Expr freezes the JSON_SERIALIZE builder as an expression.
func (b JSONSerializeBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonSerializeConstructor, &p)
}

// As freezes the JSON_SERIALIZE builder as an aliased expression.
func (b JSONSerializeBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONPredicateBuilder constructs an IS JSON predicate. Call Condition when
// placing it in WHERE/HAVING, or Expr when selecting/aliasing it as a value.
type JSONPredicateBuilder struct {
	payload jsonIsPredicatePayload
}

// IsJSON constructs an IS JSON predicate builder for value.
func IsJSON(value Expr) JSONPredicateBuilder {
	return JSONPredicateBuilder{payload: jsonIsPredicatePayload{value: value, item: jsonPredicateAny}}
}

// Type selects the JSON item kind checked by the IS JSON predicate.
func (b JSONPredicateBuilder) Type(item JSONPredicateItem) JSONPredicateBuilder {
	b.payload.item = item.internal()
	return b
}

// Value restricts the IS JSON predicate to JSON values.
func (b JSONPredicateBuilder) Value() JSONPredicateBuilder {
	return b.Type(JSONPredicateValue)
}

// Any allows any JSON item in the IS JSON predicate.
func (b JSONPredicateBuilder) Any() JSONPredicateBuilder {
	return b.Type(JSONPredicateAny)
}

// Scalar restricts the IS JSON predicate to JSON scalars.
func (b JSONPredicateBuilder) Scalar() JSONPredicateBuilder {
	return b.Type(JSONPredicateScalar)
}

// Array restricts the IS JSON predicate to JSON arrays.
func (b JSONPredicateBuilder) Array() JSONPredicateBuilder {
	return b.Type(JSONPredicateArray)
}

// Object restricts the IS JSON predicate to JSON objects.
func (b JSONPredicateBuilder) Object() JSONPredicateBuilder {
	return b.Type(JSONPredicateObject)
}

// WithUniqueKeys requires unique object keys in the IS JSON predicate.
func (b JSONPredicateBuilder) WithUniqueKeys() JSONPredicateBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

// WithoutUniqueKeys permits duplicate object keys in the IS JSON predicate.
func (b JSONPredicateBuilder) WithoutUniqueKeys() JSONPredicateBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

// Not changes the predicate to IS NOT JSON.
func (b JSONPredicateBuilder) Not() JSONPredicateBuilder {
	b.payload.not = true
	return b
}

// Expr freezes the IS JSON builder as an expression.
func (b JSONPredicateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonIsPredicate, &p)
}

// Condition freezes the IS JSON builder as a condition.
func (b JSONPredicateBuilder) Condition() Condition { return AsCondition(b.Expr()) }

// As freezes the IS JSON builder as an aliased expression.
func (b JSONPredicateBuilder) As(name string) Expr { return b.Expr().As(name) }

func jsonConstructorExpr(kind jsonConstructorKind, payload any) Expr {
	return Expr{kind: exprJSONConstructor, value: &jsonConstructor{kind: kind, payload: payload}}
}

func (w *renderer) jsonConstructor(e Expr) {
	w.feature(PostgreSQL16, "SQL/JSON constructors")
	c, ok := e.value.(*jsonConstructor)
	if !w.require(ok && c != nil, "SQL/JSON", "invalid constructor payload") {
		return
	}
	switch c.kind {
	case jsonObjectConstructor:
		w.jsonObjectConstructor(ownedPayload[*jsonObjectConstructorPayload](c.payload))
	case jsonArrayConstructor:
		w.jsonArrayConstructor(ownedPayload[*jsonArrayConstructorPayload](c.payload))
	case jsonObjectAggregate:
		w.jsonObjectAggregate(ownedPayload[*jsonObjectAggregatePayload](c.payload))
	case jsonArrayAggregate:
		w.jsonArrayAggregate(ownedPayload[*jsonArrayAggregatePayload](c.payload))
	case jsonArrayQueryConstructor:
		w.jsonArrayQueryConstructor(ownedPayload[*jsonArrayQueryPayload](c.payload))
	case jsonParseConstructor:
		w.jsonParse(ownedPayload[*jsonParsePayload](c.payload))
	case jsonScalarConstructor:
		w.jsonScalar(ownedPayload[*jsonScalarPayload](c.payload))
	case jsonSerializeConstructor:
		w.jsonSerialize(ownedPayload[*jsonSerializePayload](c.payload))
	case jsonIsPredicate:
		w.jsonIsPredicate(ownedPayload[*jsonIsPredicatePayload](c.payload))
	default:
		w.fail(ErrInvalid, "SQL/JSON", "unknown constructor")
	}
}

func (w *renderer) jsonInput(v jsonConstructorValue) {
	w.expr(v.expr)
	if v.format {
		w.text(" FORMAT JSON")
		if v.encoding != JSONEncodingDefault {
			if !w.require(v.encoding.valid(), "FORMAT JSON", "unknown JSON encoding") {
				return
			}
			w.text(" ENCODING ")
			w.text(v.encoding.name())
		}
	} else if v.encoding != JSONEncodingDefault {
		w.fail(ErrInvalid, "FORMAT JSON", "ENCODING requires FORMAT JSON")
	}
}

func (w *renderer) jsonOutput(o jsonConstructorOutput, clause string, separated bool) {
	if !o.hasReturning {
		if o.format || o.encoding != JSONEncodingDefault {
			w.fail(ErrInvalid, clause, "FORMAT JSON requires RETURNING")
		}
		return
	}
	if separated {
		w.byte(' ')
	}
	w.text("RETURNING ")
	w.dataType(o.returning)
	if o.format {
		w.text(" FORMAT JSON")
		if o.encoding != JSONEncodingDefault {
			if !w.require(o.encoding.valid(), clause, "unknown JSON encoding") {
				return
			}
			w.text(" ENCODING ")
			w.text(o.encoding.name())
		}
	} else if o.encoding != JSONEncodingDefault {
		w.fail(ErrInvalid, clause, "ENCODING requires FORMAT JSON")
	}
}

func (w *renderer) jsonNulls(policy jsonNullPolicy, clause string) {
	switch policy {
	case jsonNullDefault:
		return
	case jsonNullOnNull:
		w.text(" NULL ON NULL")
	case jsonAbsentOnNull:
		w.text(" ABSENT ON NULL")
	default:
		w.fail(ErrInvalid, clause, "unknown NULL policy")
	}
}

func (w *renderer) jsonUnique(unique jsonKeyUniqueness, clause string) {
	switch unique {
	case jsonUniqueDefault:
	case jsonWithUniqueKeys:
		w.text(" WITH UNIQUE KEYS")
	case jsonWithoutUniqueKeys:
		w.text(" WITHOUT UNIQUE KEYS")
	default:
		w.fail(ErrInvalid, clause, "unknown key uniqueness policy")
	}
}

func (w *renderer) jsonObjectConstructor(p *jsonObjectConstructorPayload) {
	w.text("JSON_OBJECT(")
	if len(p.members) == 0 {
		if p.nulls != jsonNullDefault || p.unique != jsonUniqueDefault {
			w.fail(ErrInvalid, "JSON_OBJECT", "empty constructor accepts RETURNING only")
			return
		}
	} else {
		for i, member := range p.members {
			if i != 0 {
				w.text(", ")
			}
			w.expr(member.key)
			w.text(" : ")
			w.jsonInput(member.value)
		}
		w.jsonNulls(p.nulls, "JSON_OBJECT")
		w.jsonUnique(p.unique, "JSON_OBJECT")
	}
	w.jsonOutput(p.output, "JSON_OBJECT", len(p.members) != 0)
	w.byte(')')
}

func (w *renderer) jsonArrayConstructor(p *jsonArrayConstructorPayload) {
	w.text("JSON_ARRAY(")
	if len(p.values) == 0 {
		if p.nulls != jsonNullDefault {
			w.fail(ErrInvalid, "JSON_ARRAY", "empty constructor accepts RETURNING only")
			return
		}
	} else {
		for i, value := range p.values {
			if i != 0 {
				w.text(", ")
			}
			w.jsonInput(value)
		}
		w.jsonNulls(p.nulls, "JSON_ARRAY")
	}
	w.jsonOutput(p.output, "JSON_ARRAY", len(p.values) != 0)
	w.byte(')')
}

func (w *renderer) jsonObjectAggregate(p *jsonObjectAggregatePayload) {
	if !w.require(p.invalid == "", "JSON_OBJECTAGG", p.invalid) {
		return
	}
	w.text("JSON_OBJECTAGG(")
	w.expr(p.key.expr)
	w.text(" : ")
	w.jsonInput(p.value)
	w.jsonNulls(p.nulls, "JSON_OBJECTAGG")
	w.jsonUnique(p.unique, "JSON_OBJECTAGG")
	w.jsonOutput(p.output, "JSON_OBJECTAGG", true)
	w.byte(')')
	w.aggregateTail(p.tail)
}

func (w *renderer) jsonArrayAggregate(p *jsonArrayAggregatePayload) {
	if !w.require(p.invalid == "", "JSON_ARRAYAGG", p.invalid) {
		return
	}
	w.text("JSON_ARRAYAGG(")
	w.jsonInput(p.value)
	if len(p.order) != 0 {
		w.text(" ORDER BY ")
		w.orders(p.order)
	}
	w.jsonNulls(p.nulls, "JSON_ARRAYAGG")
	w.jsonOutput(p.output, "JSON_ARRAYAGG", true)
	w.byte(')')
	w.aggregateTail(p.tail)
}

func (w *renderer) jsonArrayQueryConstructor(p *jsonArrayQueryPayload) {
	if !w.require(p.query != nil, "JSON_ARRAY", "query constructor requires a rowset") {
		return
	}
	if width := statementWidth(p.query, w.options.MaxDepth); width >= 0 && !w.require(width == 1, "JSON_ARRAY", "query must return exactly one column") {
		return
	}
	w.text("JSON_ARRAY(")
	w.statement(p.query)
	if p.format {
		w.text(" FORMAT JSON")
		if p.inputEncoding != JSONEncodingDefault {
			if !w.require(p.inputEncoding.valid(), "JSON_ARRAY", "unknown JSON encoding") {
				return
			}
			w.text(" ENCODING ")
			w.text(p.inputEncoding.name())
		}
	}
	w.jsonOutput(p.output, "JSON_ARRAY", true)
	w.byte(')')
}

func (w *renderer) jsonParse(p *jsonParsePayload) {
	w.feature(PostgreSQL17, "JSON")
	if w.err != nil {
		return
	}
	w.text("JSON(")
	w.jsonInput(p.value)
	w.jsonUnique(p.unique, "JSON")
	w.byte(')')
}

func (w *renderer) jsonScalar(p *jsonScalarPayload) {
	w.feature(PostgreSQL17, "JSON_SCALAR")
	if w.err != nil {
		return
	}
	w.text("JSON_SCALAR(")
	w.expr(p.value)
	w.byte(')')
}

func (w *renderer) jsonSerialize(p *jsonSerializePayload) {
	w.feature(PostgreSQL17, "JSON_SERIALIZE")
	if w.err != nil {
		return
	}
	w.text("JSON_SERIALIZE(")
	w.jsonInput(p.value)
	w.jsonOutput(p.output, "JSON_SERIALIZE", true)
	w.byte(')')
}

func (w *renderer) jsonIsPredicate(p *jsonIsPredicatePayload) {
	w.expr(p.value)
	if p.not {
		w.text(" IS NOT JSON")
	} else {
		w.text(" IS JSON")
	}
	switch p.item {
	case jsonPredicateAny:
	case jsonPredicateValue:
		w.text(" VALUE")
	case jsonPredicateScalar:
		w.text(" SCALAR")
	case jsonPredicateArray:
		w.text(" ARRAY")
	case jsonPredicateObject:
		w.text(" OBJECT")
	default:
		w.fail(ErrInvalid, "IS JSON", "unknown JSON item type")
		return
	}
	w.jsonUnique(p.unique, "IS JSON")
}
