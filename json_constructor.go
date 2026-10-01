package qx

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
	kind    jsonConstructorKind
	payload any
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
	JSONEncodingDefault JSONEncoding = iota
	JSONEncodingUTF8
	JSONEncodingUTF16
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
	nulls   jsonNullPolicy
	unique  jsonKeyUniqueness
	output  jsonConstructorOutput
}

type jsonArrayConstructorPayload struct {
	values []jsonConstructorValue
	nulls  jsonNullPolicy
	output jsonConstructorOutput
}

type jsonObjectAggregatePayload struct {
	key, value jsonConstructorValue
	nulls      jsonNullPolicy
	unique     jsonKeyUniqueness
	output     jsonConstructorOutput
	tail       aggregateTail
	invalid    string
}

type jsonArrayAggregatePayload struct {
	value   jsonConstructorValue
	order   []Order
	nulls   jsonNullPolicy
	output  jsonConstructorOutput
	tail    aggregateTail
	invalid string
}

type jsonArrayQueryPayload struct {
	query         Rowset
	format        bool
	inputEncoding JSONEncoding
	output        jsonConstructorOutput
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
	JSONPredicateAny JSONPredicateItem = iota
	JSONPredicateValue
	JSONPredicateScalar
	JSONPredicateArray
	JSONPredicateObject
)

// JSONNullPolicy is the explicit NULL handling policy accepted by object and
// array constructors and aggregates. The zero value leaves PostgreSQL's
// constructor default in place (NULL for objects, ABSENT for arrays).
type JSONNullPolicy uint8

const (
	JSONNullDefault JSONNullPolicy = iota
	JSONNullOnNull
	JSONAbsentOnNull
)

// JSONKeyUniqueness selects duplicate-key handling for object constructors,
// object aggregates, IS JSON, and JSON parsing. The zero value omits the
// clause and retains PostgreSQL's default.
type JSONKeyUniqueness uint8

const (
	JSONUniqueDefault JSONKeyUniqueness = iota
	JSONWithUniqueKeys
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

func (b JSONObjectBuilder) Members(members ...JSONMember) JSONObjectBuilder {
	b.payload.members = slices.Concat(b.payload.members, members)
	return b
}

func (b JSONObjectBuilder) OnNull(policy JSONNullPolicy) JSONObjectBuilder {
	b.payload.nulls = policy.internal()
	return b
}

func (b JSONObjectBuilder) NullOnNull() JSONObjectBuilder {
	return b.OnNull(JSONNullOnNull)
}

func (b JSONObjectBuilder) AbsentOnNull() JSONObjectBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

func (b JSONObjectBuilder) WithUniqueKeys() JSONObjectBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

func (b JSONObjectBuilder) WithoutUniqueKeys() JSONObjectBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

func (b JSONObjectBuilder) UniqueKeys(unique bool) JSONObjectBuilder {
	if unique {
		return b.WithUniqueKeys()
	}
	return b.WithoutUniqueKeys()
}

func (b JSONObjectBuilder) Returning(typ DataType) JSONObjectBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONObjectBuilder) FormatJSON() JSONObjectBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONObjectBuilder) EncodingUTF8() JSONObjectBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONObjectBuilder) Encoding(encoding JSONEncoding) JSONObjectBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONObjectBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonObjectConstructor, &p)
}

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

func (b JSONArrayBuilder) OnNull(policy JSONNullPolicy) JSONArrayBuilder {
	b.payload.nulls = policy.internal()
	return b
}

func (b JSONArrayBuilder) NullOnNull() JSONArrayBuilder {
	return b.OnNull(JSONNullOnNull)
}

func (b JSONArrayBuilder) AbsentOnNull() JSONArrayBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

func (b JSONArrayBuilder) Returning(typ DataType) JSONArrayBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONArrayBuilder) FormatJSON() JSONArrayBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONArrayBuilder) EncodingUTF8() JSONArrayBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONArrayBuilder) Encoding(encoding JSONEncoding) JSONArrayBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONArrayBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayConstructor, &p)
}

func (b JSONArrayBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONArrayQueryBuilder constructs JSON_ARRAY(SELECT ...), whose PostgreSQL
// grammar always omits NULL rows and does not permit a NULL policy clause.
type JSONArrayQueryBuilder struct {
	payload jsonArrayQueryPayload
}

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

func (b JSONArrayQueryBuilder) Returning(typ DataType) JSONArrayQueryBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONArrayQueryBuilder) FormatJSON() JSONArrayQueryBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONArrayQueryBuilder) EncodingUTF8() JSONArrayQueryBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONArrayQueryBuilder) Encoding(encoding JSONEncoding) JSONArrayQueryBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONArrayQueryBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayQueryConstructor, &p)
}

func (b JSONArrayQueryBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONObjectAggregateBuilder constructs JSON_OBJECTAGG. JSON_OBJECTAGG has
// FILTER and OVER tails but no in-call ORDER BY, DISTINCT, or WITHIN GROUP.
type JSONObjectAggregateBuilder struct {
	payload jsonObjectAggregatePayload
}

func JSONObjectAggregate(key Expr, value JSONInputValue) JSONObjectAggregateBuilder {
	return JSONObjectAggregateBuilder{payload: jsonObjectAggregatePayload{
		key:   jsonConstructorValue{expr: key},
		value: constructorInputValue(value),
	}}
}

func (b JSONObjectAggregateBuilder) OnNull(policy JSONNullPolicy) JSONObjectAggregateBuilder {
	b.payload.nulls = policy.internal()
	return b
}

func (b JSONObjectAggregateBuilder) NullOnNull() JSONObjectAggregateBuilder {
	return b.OnNull(JSONNullOnNull)
}

func (b JSONObjectAggregateBuilder) AbsentOnNull() JSONObjectAggregateBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

func (b JSONObjectAggregateBuilder) WithUniqueKeys() JSONObjectAggregateBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

func (b JSONObjectAggregateBuilder) WithoutUniqueKeys() JSONObjectAggregateBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

func (b JSONObjectAggregateBuilder) Returning(typ DataType) JSONObjectAggregateBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONObjectAggregateBuilder) FormatJSON() JSONObjectAggregateBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONObjectAggregateBuilder) EncodingUTF8() JSONObjectAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONObjectAggregateBuilder) Encoding(encoding JSONEncoding) JSONObjectAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONObjectAggregateBuilder) Filter(conditions ...Condition) JSONObjectAggregateBuilder {
	b.payload.tail.filter = slices.Concat(b.payload.tail.filter, conditions)
	return b
}

func (b JSONObjectAggregateBuilder) Over(window WindowSpec) JSONObjectAggregateBuilder {
	b.payload.tail.window = &window
	b.payload.tail.windowName = ""
	b.payload.invalid = ""
	return b
}

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

func (b JSONObjectAggregateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonObjectAggregate, &p)
}

func (b JSONObjectAggregateBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONArrayAggregateBuilder constructs JSON_ARRAYAGG. It supports
// PostgreSQL's in-call ORDER BY plus FILTER and OVER tails.
type JSONArrayAggregateBuilder struct {
	payload jsonArrayAggregatePayload
}

func JSONArrayAggregate(value JSONInputValue) JSONArrayAggregateBuilder {
	return JSONArrayAggregateBuilder{payload: jsonArrayAggregatePayload{value: constructorInputValue(value)}}
}

func (b JSONArrayAggregateBuilder) OrderBy(terms ...Order) JSONArrayAggregateBuilder {
	b.payload.order = slices.Concat(b.payload.order, terms)
	return b
}

func (b JSONArrayAggregateBuilder) OnNull(policy JSONNullPolicy) JSONArrayAggregateBuilder {
	b.payload.nulls = policy.internal()
	return b
}

func (b JSONArrayAggregateBuilder) NullOnNull() JSONArrayAggregateBuilder {
	return b.OnNull(JSONNullOnNull)
}

func (b JSONArrayAggregateBuilder) AbsentOnNull() JSONArrayAggregateBuilder {
	return b.OnNull(JSONAbsentOnNull)
}

func (b JSONArrayAggregateBuilder) Returning(typ DataType) JSONArrayAggregateBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONArrayAggregateBuilder) FormatJSON() JSONArrayAggregateBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONArrayAggregateBuilder) EncodingUTF8() JSONArrayAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONArrayAggregateBuilder) Encoding(encoding JSONEncoding) JSONArrayAggregateBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONArrayAggregateBuilder) Filter(conditions ...Condition) JSONArrayAggregateBuilder {
	b.payload.tail.filter = slices.Concat(b.payload.tail.filter, conditions)
	return b
}

func (b JSONArrayAggregateBuilder) Over(window WindowSpec) JSONArrayAggregateBuilder {
	b.payload.tail.window = &window
	b.payload.tail.windowName = ""
	b.payload.invalid = ""
	return b
}

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

func (b JSONArrayAggregateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonArrayAggregate, &p)
}

func (b JSONArrayAggregateBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONParseBuilder constructs PostgreSQL's JSON(...) parser/converter.
type JSONParseBuilder struct {
	payload jsonParsePayload
}

func JSONParse(value JSONInputValue) JSONParseBuilder {
	return JSONParseBuilder{payload: jsonParsePayload{value: constructorInputValue(value)}}
}

func (b JSONParseBuilder) WithUniqueKeys() JSONParseBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

func (b JSONParseBuilder) WithoutUniqueKeys() JSONParseBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

func (b JSONParseBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonParseConstructor, &p)
}

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

func JSONSerialize(value JSONInputValue) JSONSerializeBuilder {
	return JSONSerializeBuilder{payload: jsonSerializePayload{value: constructorInputValue(value)}}
}

func (b JSONSerializeBuilder) Returning(typ DataType) JSONSerializeBuilder {
	b.payload.output.returning = typ
	b.payload.output.hasReturning = true
	return b
}

func (b JSONSerializeBuilder) FormatJSON() JSONSerializeBuilder {
	b.payload.output.format = true
	return b
}

func (b JSONSerializeBuilder) EncodingUTF8() JSONSerializeBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = JSONEncodingUTF8
	return b
}

func (b JSONSerializeBuilder) Encoding(encoding JSONEncoding) JSONSerializeBuilder {
	b.payload.output.format = true
	b.payload.output.encoding = encoding
	return b
}

func (b JSONSerializeBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonSerializeConstructor, &p)
}

func (b JSONSerializeBuilder) As(name string) Expr { return b.Expr().As(name) }

// JSONPredicateBuilder constructs an IS JSON predicate. Call Condition when
// placing it in WHERE/HAVING, or Expr when selecting/aliasing it as a value.
type JSONPredicateBuilder struct {
	payload jsonIsPredicatePayload
}

func IsJSON(value Expr) JSONPredicateBuilder {
	return JSONPredicateBuilder{payload: jsonIsPredicatePayload{value: value, item: jsonPredicateAny}}
}

func (b JSONPredicateBuilder) Type(item JSONPredicateItem) JSONPredicateBuilder {
	b.payload.item = item.internal()
	return b
}

func (b JSONPredicateBuilder) Value() JSONPredicateBuilder {
	return b.Type(JSONPredicateValue)
}

func (b JSONPredicateBuilder) Any() JSONPredicateBuilder {
	return b.Type(JSONPredicateAny)
}

func (b JSONPredicateBuilder) Scalar() JSONPredicateBuilder {
	return b.Type(JSONPredicateScalar)
}

func (b JSONPredicateBuilder) Array() JSONPredicateBuilder {
	return b.Type(JSONPredicateArray)
}

func (b JSONPredicateBuilder) Object() JSONPredicateBuilder {
	return b.Type(JSONPredicateObject)
}

func (b JSONPredicateBuilder) WithUniqueKeys() JSONPredicateBuilder {
	b.payload.unique = jsonWithUniqueKeys
	return b
}

func (b JSONPredicateBuilder) WithoutUniqueKeys() JSONPredicateBuilder {
	b.payload.unique = jsonWithoutUniqueKeys
	return b
}

func (b JSONPredicateBuilder) Not() JSONPredicateBuilder {
	b.payload.not = true
	return b
}

func (b JSONPredicateBuilder) Expr() Expr {
	p := b.payload
	return jsonConstructorExpr(jsonIsPredicate, &p)
}

func (b JSONPredicateBuilder) Condition() Condition { return AsCondition(b.Expr()) }
func (b JSONPredicateBuilder) As(name string) Expr  { return b.Expr().As(name) }

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
		w.jsonObjectConstructor(c.payload.(*jsonObjectConstructorPayload))
	case jsonArrayConstructor:
		w.jsonArrayConstructor(c.payload.(*jsonArrayConstructorPayload))
	case jsonObjectAggregate:
		w.jsonObjectAggregate(c.payload.(*jsonObjectAggregatePayload))
	case jsonArrayAggregate:
		w.jsonArrayAggregate(c.payload.(*jsonArrayAggregatePayload))
	case jsonArrayQueryConstructor:
		w.jsonArrayQueryConstructor(c.payload.(*jsonArrayQueryPayload))
	case jsonParseConstructor:
		w.jsonParse(c.payload.(*jsonParsePayload))
	case jsonScalarConstructor:
		w.jsonScalar(c.payload.(*jsonScalarPayload))
	case jsonSerializeConstructor:
		w.jsonSerialize(c.payload.(*jsonSerializePayload))
	case jsonIsPredicate:
		w.jsonIsPredicate(c.payload.(*jsonIsPredicatePayload))
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
	w.text("JSON(")
	w.jsonInput(p.value)
	w.jsonUnique(p.unique, "JSON")
	w.byte(')')
}

func (w *renderer) jsonScalar(p *jsonScalarPayload) {
	w.text("JSON_SCALAR(")
	w.expr(p.value)
	w.byte(')')
}

func (w *renderer) jsonSerialize(p *jsonSerializePayload) {
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

// jsonConstructor clones the complete constructor payload while preserving
// nested rowset sharing through cloneContext's statement map.
func (c *cloneContext) jsonConstructor(e Expr) Expr {
	n := *e.value.(*jsonConstructor)
	switch p := n.payload.(type) {
	case *jsonObjectConstructorPayload:
		v := *p
		v.members = cloneSlice(p.members)
		for i := range v.members {
			v.members[i].key = c.expr(v.members[i].key)
			v.members[i].value.expr = c.expr(v.members[i].value.expr)
		}
		n.payload = &v
	case *jsonArrayConstructorPayload:
		v := *p
		v.values = cloneSlice(p.values)
		for i := range v.values {
			v.values[i].expr = c.expr(v.values[i].expr)
		}
		n.payload = &v
	case *jsonObjectAggregatePayload:
		v := *p
		v.key.expr = c.expr(v.key.expr)
		v.value.expr = c.expr(v.value.expr)
		v.tail = c.aggregateTail(v.tail)
		n.payload = &v
	case *jsonArrayAggregatePayload:
		v := *p
		v.value.expr = c.expr(v.value.expr)
		v.order = c.orders(v.order)
		v.tail = c.aggregateTail(v.tail)
		n.payload = &v
	case *jsonArrayQueryPayload:
		v := *p
		v.query = c.rowset(v.query)
		n.payload = &v
	case *jsonParsePayload:
		v := *p
		v.value.expr = c.expr(v.value.expr)
		n.payload = &v
	case *jsonScalarPayload:
		v := *p
		v.value = c.expr(v.value)
		n.payload = &v
	case *jsonSerializePayload:
		v := *p
		v.value.expr = c.expr(v.value.expr)
		n.payload = &v
	case *jsonIsPredicatePayload:
		v := *p
		v.value = c.expr(v.value)
		n.payload = &v
	default:
		return e
	}
	e.value = &n
	return e
}
