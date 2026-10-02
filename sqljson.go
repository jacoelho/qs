package qs

import "slices"

type jsonBehaviorKind uint8

const (
	jsonBehaviorDefault jsonBehaviorKind = iota
	jsonBehaviorError
	jsonBehaviorNull
	jsonBehaviorEmptyArray
	jsonBehaviorEmptyObject
	jsonBehaviorDefaultValue
	jsonBehaviorTrue
	jsonBehaviorFalse
	jsonBehaviorUnknown
)

// JSONBehavior is an explicit SQL/JSON empty/error result. Its zero value means
// use the PostgreSQL default. Constructors constrain expression-bearing defaults.
type JSONBehavior struct {
	value Expr
	kind  jsonBehaviorKind
}

// JSONError requests an error when the selected SQL/JSON condition occurs.
func JSONError() JSONBehavior { return JSONBehavior{kind: jsonBehaviorError} }

// JSONNull requests an SQL NULL result when the selected SQL/JSON condition occurs.
func JSONNull() JSONBehavior { return JSONBehavior{kind: jsonBehaviorNull} }

// JSONEmptyArray requests an empty JSON array when the selected condition occurs.
func JSONEmptyArray() JSONBehavior { return JSONBehavior{kind: jsonBehaviorEmptyArray} }

// JSONEmptyObject requests an empty JSON object when the selected condition occurs.
func JSONEmptyObject() JSONBehavior { return JSONBehavior{kind: jsonBehaviorEmptyObject} }

// JSONDefault supplies an expression as the result for the selected condition.
func JSONDefault(value Expr) JSONBehavior {
	return JSONBehavior{kind: jsonBehaviorDefaultValue, value: value}
}

// JSONTrue requests TRUE for a JSON_EXISTS or JSON_TABLE EXISTS error policy.
func JSONTrue() JSONBehavior { return JSONBehavior{kind: jsonBehaviorTrue} }

// JSONFalse requests FALSE for a JSON_EXISTS or JSON_TABLE EXISTS error policy.
func JSONFalse() JSONBehavior { return JSONBehavior{kind: jsonBehaviorFalse} }

// JSONUnknown requests SQL NULL for a JSON_EXISTS or JSON_TABLE EXISTS error policy.
func JSONUnknown() JSONBehavior { return JSONBehavior{kind: jsonBehaviorUnknown} }

type jsonPassing struct {
	name  string
	value Expr
}

type jsonQueryKind uint8

const (
	jsonQueryValue jsonQueryKind = iota + 1
	jsonQueryQuery
	jsonQueryExists
	jsonQueryTable
)

type jsonWrapper uint8

const (
	jsonWrapperDefault jsonWrapper = iota
	jsonWrapperWithout
	jsonWrapperWithUnconditional
	jsonWrapperWithConditional
)

type jsonQuotes uint8

const (
	jsonQuotesDefault jsonQuotes = iota
	jsonQuotesKeep
	jsonQuotesOmit
)

type jsonOptions struct {
	empty        JSONBehavior
	onError      JSONBehavior
	returning    DataType
	hasReturning bool
	format       bool
	encoding     JSONEncoding
	wrapper      jsonWrapper
	quotes       jsonQuotes
}

// JSONQueryBuilder is an immutable SQL/JSON function descriptor. Call Expr to
// freeze it into an expression. These query functions require PostgreSQL 17.
type JSONQueryBuilder struct {
	document Expr
	path     Expr
	passing  []jsonPassing
	options  jsonOptions
	kind     jsonQueryKind
}

// JSONValue constructs a JSON_VALUE descriptor for document and path.
func JSONValue(document, path Expr) JSONQueryBuilder {
	return JSONQueryBuilder{kind: jsonQueryValue, document: document, path: path}
}

// JSONQuery constructs a JSON_QUERY descriptor for document and path.
func JSONQuery(document, path Expr) JSONQueryBuilder {
	return JSONQueryBuilder{kind: jsonQueryQuery, document: document, path: path}
}

// JSONExists constructs a JSON_EXISTS descriptor for document and path.
func JSONExists(document, path Expr) JSONQueryBuilder {
	return JSONQueryBuilder{kind: jsonQueryExists, document: document, path: path}
}

// Passing adds a named variable to the SQL/JSON PASSING clause.
func (b JSONQueryBuilder) Passing(name string, value Expr) JSONQueryBuilder {
	b.passing = slices.Concat(b.passing, []jsonPassing{{name: name, value: value}})
	return b
}

// Returning selects the SQL type returned by the SQL/JSON query function.
func (b JSONQueryBuilder) Returning(typ DataType) JSONQueryBuilder {
	b.options.returning = typ
	b.options.hasReturning = true
	return b
}

// FormatJSON marks JSON_VALUE or JSON_QUERY output as formatted JSON.
func (b JSONQueryBuilder) FormatJSON() JSONQueryBuilder { b.options.format = true; return b }

// EncodingUTF8 selects UTF8 for formatted SQL/JSON query output.
func (b JSONQueryBuilder) EncodingUTF8() JSONQueryBuilder {
	b.options.format = true
	b.options.encoding = JSONEncodingUTF8
	return b
}

// WithoutWrapper selects WITHOUT ARRAY WRAPPER for JSON_QUERY output and
// replaces any previous wrapper selection.
func (b JSONQueryBuilder) WithoutWrapper() JSONQueryBuilder {
	b.options.wrapper = jsonWrapperWithout
	return b
}

// WithWrapper selects an unconditional array wrapper for JSON_QUERY output and
// replaces any previous wrapper selection.
func (b JSONQueryBuilder) WithWrapper() JSONQueryBuilder {
	b.options.wrapper = jsonWrapperWithUnconditional
	return b
}

// WithConditionalWrapper selects a conditional array wrapper for JSON_QUERY
// output and replaces any previous wrapper selection.
func (b JSONQueryBuilder) WithConditionalWrapper() JSONQueryBuilder {
	b.options.wrapper = jsonWrapperWithConditional
	return b
}

// KeepQuotes preserves quotes around scalar strings in JSON_QUERY output and
// replaces any previous quote selection.
func (b JSONQueryBuilder) KeepQuotes() JSONQueryBuilder {
	b.options.quotes = jsonQuotesKeep
	return b
}

// OmitQuotes removes quotes around scalar strings in JSON_QUERY output. It is
// rejected when an array wrapper is selected and replaces any previous choice.
func (b JSONQueryBuilder) OmitQuotes() JSONQueryBuilder {
	b.options.quotes = jsonQuotesOmit
	return b
}

// OnEmpty sets the policy for an empty JSON path result, replacing the prior
// policy. The renderer rejects policies unsupported by the selected operation.
func (b JSONQueryBuilder) OnEmpty(v JSONBehavior) JSONQueryBuilder { b.options.empty = v; return b }

// OnError sets the policy for a JSON path or conversion error, replacing the
// prior policy. The renderer rejects policies unsupported by the operation.
func (b JSONQueryBuilder) OnError(v JSONBehavior) JSONQueryBuilder { b.options.onError = v; return b }

// Expr captures the immutable SQL/JSON descriptor as an expression.
func (b JSONQueryBuilder) Expr() Expr { return Expr{kind: exprSQLJSON, value: &b} }

// As captures the SQL/JSON descriptor as an aliased expression.
func (b JSONQueryBuilder) As(name string) Expr { return b.Expr().As(name) }

// Condition converts JSON_EXISTS to a condition. Other SQL/JSON query kinds
// produce an invalid condition descriptor.
func (b JSONQueryBuilder) Condition() Condition {
	if b.kind != jsonQueryExists {
		return AsCondition(invalidExpr("SQL/JSON", "only JSON_EXISTS is a boolean predicate"))
	}
	return AsCondition(b.Expr())
}

func (w *renderer) jsonPassing(values []jsonPassing) {
	if len(values) == 0 {
		return
	}
	w.text(" PASSING ")
	for i, p := range values {
		for j := range i {
			if values[j].name == p.name {
				w.fail(ErrInvalid, "JSON PASSING", "duplicate variable name")
				return
			}
		}
		if i > 0 {
			w.text(", ")
		}
		w.expr(p.value)
		w.text(" AS ")
		w.identifierPart(p.name)
	}
}
func (w *renderer) jsonBehaviour(b JSONBehavior, clause string, kind jsonQueryKind) {
	if b.kind == jsonBehaviorDefault {
		return
	}
	valid := b.kind == jsonBehaviorError
	switch kind {
	case jsonQueryValue:
		valid = valid || b.kind == jsonBehaviorNull || b.kind == jsonBehaviorDefaultValue
	case jsonQueryQuery:
		valid = valid || b.kind == jsonBehaviorNull || b.kind == jsonBehaviorEmptyArray || b.kind == jsonBehaviorEmptyObject || b.kind == jsonBehaviorDefaultValue
	case jsonQueryExists:
		valid = valid || b.kind == jsonBehaviorTrue || b.kind == jsonBehaviorFalse || b.kind == jsonBehaviorUnknown
	case jsonQueryTable:
		valid = valid || b.kind == jsonBehaviorEmptyArray
	}
	if !w.require(valid, clause, "unsupported SQL/JSON behaviour for this operation") {
		return
	}
	w.byte(' ')
	switch b.kind {
	case jsonBehaviorError:
		w.text("ERROR")
	case jsonBehaviorNull:
		w.text("NULL")
	case jsonBehaviorEmptyArray:
		w.text("EMPTY ARRAY")
	case jsonBehaviorEmptyObject:
		w.text("EMPTY OBJECT")
	case jsonBehaviorDefaultValue:
		w.text("DEFAULT ")
		w.expr(b.value)
	case jsonBehaviorTrue:
		w.text(sqlTrue)
	case jsonBehaviorFalse:
		w.text("FALSE")
	case jsonBehaviorUnknown:
		w.text("UNKNOWN")
	default:
		w.fail(ErrInvalid, clause, "unknown SQL/JSON behaviour")
		return
	}
	w.byte(' ')
	w.text(clause)
}
func (w *renderer) jsonOptions(o jsonOptions, kind jsonQueryKind, column bool) {
	if kind != jsonQueryQuery && (o.wrapper != jsonWrapperDefault || o.quotes != jsonQuotesDefault || o.format || o.encoding != JSONEncodingDefault) {
		w.fail(ErrInvalid, "SQL/JSON", "wrappers, quotes and FORMAT JSON require JSON_QUERY semantics")
		return
	}
	if o.encoding != JSONEncodingDefault {
		if !w.require(o.encoding == JSONEncodingUTF8, "SQL/JSON", "only UTF8 encoding is supported") {
			return
		}
		if !w.require(o.format, "SQL/JSON", "encoding requires FORMAT JSON") {
			return
		}
	}
	if kind == jsonQueryExists && (o.hasReturning || o.empty.kind != jsonBehaviorDefault) {
		w.fail(ErrInvalid, "JSON_EXISTS", "RETURNING and ON EMPTY are not supported")
		return
	}
	if o.hasReturning && !column {
		w.text(" RETURNING ")
		w.dataType(o.returning)
	}
	if (o.format || o.encoding != JSONEncodingDefault) && !column && !o.hasReturning {
		w.fail(ErrInvalid, "SQL/JSON", "FORMAT JSON requires RETURNING")
		return
	}
	if o.format && !column {
		w.text(" FORMAT JSON")
		if o.encoding != JSONEncodingDefault {
			w.text(" ENCODING UTF8")
		}
	}
	w.jsonDecorations(o)
	w.jsonBehaviour(o.empty, "ON EMPTY", kind)
	w.jsonBehaviour(o.onError, "ON ERROR", kind)
}
func (w *renderer) jsonDecorations(o jsonOptions) {
	compatible := o.quotes != jsonQuotesOmit || (o.wrapper != jsonWrapperWithUnconditional && o.wrapper != jsonWrapperWithConditional)
	if !w.require(compatible, "JSON_QUERY", "OMIT QUOTES is incompatible with WITH WRAPPER") {
		return
	}
	switch o.wrapper {
	case jsonWrapperDefault:
	case jsonWrapperWithout:
		w.text(" WITHOUT ARRAY WRAPPER")
	case jsonWrapperWithUnconditional:
		w.text(" WITH UNCONDITIONAL ARRAY WRAPPER")
	case jsonWrapperWithConditional:
		w.text(" WITH CONDITIONAL ARRAY WRAPPER")
	default:
		w.fail(ErrInvalid, "JSON_QUERY", "invalid wrapper")
	}
	switch o.quotes {
	case jsonQuotesDefault:
	case jsonQuotesKeep:
		w.text(" KEEP QUOTES")
	case jsonQuotesOmit:
		w.text(" OMIT QUOTES")
	default:
		w.fail(ErrInvalid, "JSON_QUERY", "invalid quotes policy")
	}
}
func (w *renderer) sqlJSON(e Expr) {
	b := ownedPayload[*JSONQueryBuilder](e.value)
	w.feature(PostgreSQL17, "SQL/JSON query functions")
	switch b.kind {
	case jsonQueryValue:
		w.text("JSON_VALUE(")
	case jsonQueryQuery:
		w.text("JSON_QUERY(")
	case jsonQueryExists:
		w.text("JSON_EXISTS(")
	default:
		w.fail(ErrInvalid, "SQL/JSON", "zero JSON function")
		return
	}
	w.expr(b.document)
	w.text(", ")
	w.expr(b.path)
	w.jsonPassing(b.passing)
	w.jsonOptions(b.options, b.kind, false)
	w.byte(')')
}

// JSONTableColumn describes one SQL/JSON table column or a nested column group.
// Paths in JSON_TABLE are SQL string literals, escaped by the library, rather
// than interpolated SQL. Runtime path variables belong in Passing.
type JSONTableColumn struct {
	name     string
	path     string
	pathName string
	columns  []JSONTableColumn
	typ      DataType
	options  jsonOptions
	kind     jsonTableColumnKind
	hasPath  bool
}

type jsonTableColumnKind uint8

const (
	jsonTableValueColumn jsonTableColumnKind = iota + 1
	jsonTableOrdinalityColumn
	jsonTableExistsColumn
	jsonTableNestedColumn
)

// JSONColumn creates a JSON_TABLE value column with a SQL type.
func JSONColumn(name string, typ DataType) JSONTableColumn {
	return JSONTableColumn{kind: jsonTableValueColumn, name: name, typ: typ}
}

// JSONOrdinality creates a JSON_TABLE ordinality column. Paths and value
// options are invalid for this column kind.
func JSONOrdinality(name string) JSONTableColumn {
	return JSONTableColumn{kind: jsonTableOrdinalityColumn, name: name}
}

// JSONExistsColumn creates a JSON_TABLE EXISTS column with a SQL type.
func JSONExistsColumn(name string, typ DataType) JSONTableColumn {
	return JSONTableColumn{kind: jsonTableExistsColumn, name: name, typ: typ}
}

// JSONNested creates a nested JSON_TABLE column group at path.
func JSONNested(path string, columns ...JSONTableColumn) JSONTableColumn {
	return JSONTableColumn{kind: jsonTableNestedColumn, path: path, hasPath: true, columns: cloneSlice(columns)}
}

// Path selects the JSON path used to populate a JSON_TABLE value column.
func (c JSONTableColumn) Path(path string) JSONTableColumn { c.path = path; c.hasPath = true; return c }

// PathName gives a nested JSON_TABLE path an SQL identifier.
func (c JSONTableColumn) PathName(name string) JSONTableColumn { c.pathName = name; return c }

// FormatJSON marks a JSON_TABLE value column as formatted JSON.
func (c JSONTableColumn) FormatJSON() JSONTableColumn { c.options.format = true; return c }

// EncodingUTF8 selects UTF8 for a formatted JSON_TABLE value column.
func (c JSONTableColumn) EncodingUTF8() JSONTableColumn {
	c.options.format = true
	c.options.encoding = JSONEncodingUTF8
	return c
}

// WithoutWrapper selects WITHOUT ARRAY WRAPPER for a JSON_TABLE value column
// and replaces any previous wrapper selection.
func (c JSONTableColumn) WithoutWrapper() JSONTableColumn {
	c.options.wrapper = jsonWrapperWithout
	return c
}

// WithWrapper selects an unconditional array wrapper for a JSON_TABLE value
// column and replaces any previous wrapper selection.
func (c JSONTableColumn) WithWrapper() JSONTableColumn {
	c.options.wrapper = jsonWrapperWithUnconditional
	return c
}

// WithConditionalWrapper selects a conditional array wrapper for a JSON_TABLE
// value column and replaces any previous wrapper selection.
func (c JSONTableColumn) WithConditionalWrapper() JSONTableColumn {
	c.options.wrapper = jsonWrapperWithConditional
	return c
}

// KeepQuotes preserves scalar string quotes for a JSON_TABLE value column and
// replaces any previous quote selection.
func (c JSONTableColumn) KeepQuotes() JSONTableColumn {
	c.options.quotes = jsonQuotesKeep
	return c
}

// OmitQuotes removes scalar string quotes for a JSON_TABLE value column. It is
// rejected with an array wrapper and replaces any previous quote selection.
func (c JSONTableColumn) OmitQuotes() JSONTableColumn {
	c.options.quotes = jsonQuotesOmit
	return c
}

// OnEmpty sets a JSON_TABLE value-column empty-result policy, replacing the
// prior policy. The renderer validates it for the column's SQL/JSON role.
func (c JSONTableColumn) OnEmpty(v JSONBehavior) JSONTableColumn { c.options.empty = v; return c }

// OnError sets a JSON_TABLE column error policy, replacing the prior policy.
// The renderer validates it for the column's SQL/JSON role.
func (c JSONTableColumn) OnError(v JSONBehavior) JSONTableColumn { c.options.onError = v; return c }

// JSONTableBuilder is an immutable relation descriptor, requiring PostgreSQL 17.
type JSONTableBuilder struct {
	path     string
	pathName string
	document Expr
	passing  []jsonPassing
	columns  []JSONTableColumn
	onError  JSONBehavior
}

// JSONTable constructs a JSON_TABLE relation descriptor. JSON_TABLE requires
// PostgreSQL 17 or later and at least one column at render time.
func JSONTable(document Expr, path string, columns ...JSONTableColumn) JSONTableBuilder {
	return JSONTableBuilder{document: document, path: path, columns: cloneSlice(columns)}
}

// Columns appends output columns to JSON_TABLE.
func (b JSONTableBuilder) Columns(columns ...JSONTableColumn) JSONTableBuilder {
	b.columns = slices.Concat(b.columns, columns)
	return b
}

// Passing adds a named variable to JSON_TABLE's PASSING clause.
func (b JSONTableBuilder) Passing(name string, value Expr) JSONTableBuilder {
	b.passing = slices.Concat(b.passing, []jsonPassing{{name: name, value: value}})
	return b
}

// PathName gives JSON_TABLE's row path an SQL identifier.
func (b JSONTableBuilder) PathName(name string) JSONTableBuilder { b.pathName = name; return b }

// OnError sets JSON_TABLE's row-level error policy.
func (b JSONTableBuilder) OnError(v JSONBehavior) JSONTableBuilder { b.onError = v; return b }

// Ref freezes JSON_TABLE as a relation.
func (b JSONTableBuilder) Ref() Relation { return Relation{kind: relationJSONTable, value: &b} }

// As freezes JSON_TABLE as a relation with a table alias.
func (b JSONTableBuilder) As(name string) Relation { return b.Ref().As(name) }
func (w *renderer) jsonTable(b *JSONTableBuilder) {
	w.feature(PostgreSQL17, "JSON_TABLE")
	w.text("JSON_TABLE(")
	w.expr(b.document)
	w.text(", ")
	w.expr(LiteralString(b.path))
	if b.pathName != "" {
		w.text(" AS ")
		w.identifierPart(b.pathName)
	}
	w.jsonPassing(b.passing)
	w.text(" COLUMNS (")
	w.jsonColumns(b.columns)
	w.byte(')')
	w.jsonBehaviour(b.onError, "ON ERROR", jsonQueryTable)
	w.byte(')')
}
func (w *renderer) jsonColumns(columns []JSONTableColumn) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	if !w.require(len(columns) > 0, "JSON_TABLE", "requires columns") {
		return
	}
	for i, c := range columns {
		if i > 0 {
			w.text(", ")
		}
		if c.kind < jsonTableValueColumn || c.kind > jsonTableNestedColumn {
			w.fail(ErrInvalid, "JSON_TABLE", "zero column")
			return
		}
		// Local duplicate names are rejected without allocating a name map. Cross-
		// level collisions and JSON path validity are checked by PostgreSQL.
		if c.kind != jsonTableNestedColumn {
			for j := range i {
				if columns[j].kind != jsonTableNestedColumn && columns[j].name == c.name {
					w.fail(ErrInvalid, "JSON_TABLE", "duplicate column name")
					return
				}
			}
		}
		if c.kind == jsonTableNestedColumn {
			if !w.require(!hasJSONOptions(c.options), "JSON_TABLE", "nested groups cannot have value-column options") {
				return
			}
			w.text("NESTED PATH ")
			w.expr(LiteralString(c.path))
			if c.pathName != "" {
				w.text(" AS ")
				w.identifierPart(c.pathName)
			}
			w.text(" COLUMNS (")
			w.jsonColumns(c.columns)
			w.byte(')')
			continue
		}
		w.identifierPart(c.name)
		if c.kind == jsonTableOrdinalityColumn {
			if c.hasPath || c.pathName != "" || hasJSONOptions(c.options) {
				w.fail(ErrInvalid, "JSON_TABLE", "ordinality cannot have path or value options")
				return
			}
			w.text(" FOR ORDINALITY")
			continue
		}
		if c.pathName != "" {
			w.fail(ErrInvalid, "JSON_TABLE", "only nested groups can name their paths")
			return
		}
		w.byte(' ')
		w.dataType(c.typ)
		if c.kind == jsonTableExistsColumn {
			w.text(" EXISTS")
		} else if c.options.format {
			w.text(" FORMAT JSON")
			if c.options.encoding != JSONEncodingDefault {
				if !w.require(c.options.encoding == JSONEncodingUTF8, "JSON_TABLE", "only UTF8 encoding is supported") {
					return
				}
				w.text(" ENCODING UTF8")
			}
		}
		if c.hasPath {
			w.text(" PATH ")
			w.expr(LiteralString(c.path))
		}
		kind := jsonQueryValue
		if c.kind == jsonTableExistsColumn {
			kind = jsonQueryExists
		} else if c.options.format || c.typ.name == "json" || c.typ.name == "jsonb" || c.options.wrapper != jsonWrapperDefault || c.options.quotes != jsonQuotesDefault {
			kind = jsonQueryQuery
		}
		w.jsonOptions(c.options, kind, true)
	}
}
func hasJSONOptions(o jsonOptions) bool {
	return o.hasReturning || o.format || o.encoding != JSONEncodingDefault || o.wrapper != jsonWrapperDefault || o.quotes != jsonQuotesDefault || o.empty.kind != jsonBehaviorDefault || o.onError.kind != jsonBehaviorDefault
}
