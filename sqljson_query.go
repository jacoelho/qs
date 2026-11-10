package qs

// This file owns the SQL/JSON query functions JSON_EXISTS, JSON_VALUE and
// JSON_QUERY and the clauses they share with JSON_TABLE. Each function is its
// own builder type: the behaviors, wrappers and RETURNING forms PostgreSQL
// accepts differ per function, and sqljson_methods.go gives each type only
// the methods of its own set.

type jsonPassing struct {
	name  string
	value jsonOperand
}

func passed(passing []jsonPassing, name string, value JSONOperand) []jsonPassing {
	return extend(passing, jsonPassing{name: name, value: operandOf(value)})
}

// jsonPassing writes PASSING value AS name, .... PostgreSQL accepts a repeated
// name, so none is rejected here.
func (w *renderer) jsonPassing(passing []jsonPassing) {
	for i, p := range passing {
		if i == 0 {
			w.text(" PASSING ")
		} else {
			w.text(", ")
		}
		w.jsonOperand(p.value)
		w.text(" AS ")
		w.identifierPart(p.name)
		if w.stopped("PASSING", i+1) {
			return
		}
	}
}

type jsonBehaviorKind uint8

const (
	jsonBehaviorDefault jsonBehaviorKind = iota // the clause is omitted
	jsonBehaviorError
	jsonBehaviorNull
	jsonBehaviorTrue
	jsonBehaviorFalse
	jsonBehaviorUnknown
	jsonBehaviorEmptyArray
	jsonBehaviorEmptyObject
	jsonBehaviorValue // DEFAULT value
)

// jsonBehavior is an ON EMPTY or ON ERROR clause. Which kinds a function
// accepts is decided by the methods its builder has, not here.
type jsonBehavior struct {
	value Expr
	kind  jsonBehaviorKind
}

func behavior(kind jsonBehaviorKind) jsonBehavior { return jsonBehavior{kind: kind} }

func defaultBehavior(value Expr) jsonBehavior {
	return jsonBehavior{kind: jsonBehaviorValue, value: value}
}

func (w *renderer) jsonBehavior(b jsonBehavior, event string) {
	if w.err != nil {
		return
	}
	switch b.kind {
	case jsonBehaviorDefault:
		return
	case jsonBehaviorError:
		w.text(" ERROR")
	case jsonBehaviorNull:
		w.text(" NULL")
	case jsonBehaviorTrue:
		w.text(" TRUE")
	case jsonBehaviorFalse:
		w.text(" FALSE")
	case jsonBehaviorUnknown:
		w.text(" UNKNOWN")
	case jsonBehaviorEmptyArray:
		w.text(" EMPTY ARRAY")
	case jsonBehaviorEmptyObject:
		w.text(" EMPTY OBJECT")
	case jsonBehaviorValue:
		if !w.require(!jsonDefaultColumn(b.value, w.options.MaxDepth), "DEFAULT", "cannot reference a column") {
			return
		}
		w.text(" DEFAULT ")
		w.expr(b.value)
		if w.stopped("DEFAULT", 0) {
			return
		}
	}
	w.text(event)
}

// jsonDefaultColumn reports whether value is a column reference, bare or
// behind casts and parentheses. PostgreSQL rejects a column anywhere in a
// DEFAULT value; this catches the one that is the whole value and leaves the
// rest to the server. A bound parameter is not checked: PostgreSQL refuses it
// under the extended query protocol and accepts it when the driver sends it
// as a literal, which qs cannot know.
func jsonDefaultColumn(value Expr, depth int) bool {
	for ; depth > 0; depth-- {
		switch n := value.node().(type) {
		case *castExpression:
			value = n.expr
			continue
		case *groupExpression:
			if len(n.items) == 1 {
				value = n.items[0]
				continue
			}
		}
		break
	}
	return value.isColumn()
}

const (
	jsonOnEmpty = " ON EMPTY"
	jsonOnError = " ON ERROR"
)

type jsonWrapper uint8

const (
	jsonWrapperDefault jsonWrapper = iota
	jsonWithoutWrapper
	jsonWithWrapper
	jsonWithConditionalWrapper
)

type jsonQuotes uint8

const (
	jsonQuotesDefault jsonQuotes = iota
	jsonKeepQuotes
	jsonOmitQuotes
)

// jsonShape is the wrapper and quotes of JSON_QUERY and of a JSON_TABLE
// column. PostgreSQL rejects OMIT QUOTES together with WITH WRAPPER; KEEP
// QUOTES combines with any wrapper.
type jsonShape struct {
	wrapper jsonWrapper
	quotes  jsonQuotes
}

func (w *renderer) jsonShape(s jsonShape) {
	wraps := s.wrapper == jsonWithWrapper || s.wrapper == jsonWithConditionalWrapper
	if !w.require(!wraps || s.quotes != jsonOmitQuotes, "OMIT QUOTES", "cannot combine with WITH WRAPPER") {
		return
	}
	switch s.wrapper {
	case jsonWrapperDefault:
	case jsonWithoutWrapper:
		w.text(" WITHOUT WRAPPER")
	case jsonWithWrapper:
		w.text(" WITH WRAPPER")
	case jsonWithConditionalWrapper:
		w.text(" WITH CONDITIONAL WRAPPER")
	}
	switch s.quotes {
	case jsonQuotesDefault:
	case jsonKeepQuotes:
		w.text(" KEEP QUOTES")
	case jsonOmitQuotes:
		w.text(" OMIT QUOTES")
	}
}

// jsonPathCall writes name(document, path PASSING ..., leaving the call open.
func (w *renderer) jsonPathCall(name string, document jsonOperand, path Expr, passing []jsonPassing) {
	w.text(name)
	w.byte('(')
	w.jsonOperand(document)
	if w.stopped("document", 0) {
		return
	}
	w.text(", ")
	w.expr(path)
	if w.stopped("path", 0) {
		return
	}
	w.jsonPassing(passing)
}

// JSONExistsBuilder is a JSON_EXISTS test. It is an immutable value.
type JSONExistsBuilder struct {
	path     Expr
	passing  []jsonPassing
	document jsonOperand
	onError  jsonBehavior
}

// JSONExists tests whether the SQL/JSON path matches any item of document.
// The path is a jsonpath expression, commonly LiteralString("$.a").
func JSONExists(document JSONOperand, path Expr) JSONExistsBuilder {
	return JSONExistsBuilder{document: operandOf(document), path: path}
}

func (w *renderer) jsonExists(b *JSONExistsBuilder) {
	w.jsonPathCall("JSON_EXISTS", b.document, b.path, b.passing)
	w.jsonBehavior(b.onError, jsonOnError)
	w.byte(')')
}

// JSONValueBuilder is a JSON_VALUE extraction of one SQL scalar. It is an
// immutable value.
type JSONValueBuilder struct {
	path     Expr
	passing  []jsonPassing
	document jsonOperand
	onEmpty  jsonBehavior
	onError  jsonBehavior
	output   jsonOutput
}

// JSONValue extracts the scalar the SQL/JSON path selects from document. The
// path is a jsonpath expression, commonly LiteralString("$.a").
func JSONValue(document JSONOperand, path Expr) JSONValueBuilder {
	return JSONValueBuilder{document: operandOf(document), path: path}
}

func (w *renderer) jsonValue(b *JSONValueBuilder) {
	w.jsonPathCall("JSON_VALUE", b.document, b.path, b.passing)
	w.jsonOutput(b.output, true)
	w.jsonBehavior(b.onEmpty, jsonOnEmpty)
	w.jsonBehavior(b.onError, jsonOnError)
	w.byte(')')
}

// JSONQueryBuilder is a JSON_QUERY extraction of a JSON value. It is an
// immutable value.
type JSONQueryBuilder struct {
	path     Expr
	passing  []jsonPassing
	document jsonOperand
	onEmpty  jsonBehavior
	onError  jsonBehavior
	output   jsonOutput
	shape    jsonShape
}

// JSONQuery extracts the JSON value the SQL/JSON path selects from document.
// The path is a jsonpath expression, commonly LiteralString("$.a").
func JSONQuery(document JSONOperand, path Expr) JSONQueryBuilder {
	return JSONQueryBuilder{document: operandOf(document), path: path}
}

func (w *renderer) jsonQuery(b *JSONQueryBuilder) {
	w.jsonPathCall("JSON_QUERY", b.document, b.path, b.passing)
	w.jsonOutput(b.output, true)
	w.jsonShape(b.shape)
	w.jsonBehavior(b.onEmpty, jsonOnEmpty)
	w.jsonBehavior(b.onError, jsonOnError)
	w.byte(')')
}
