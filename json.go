package qs

// JSONInput is an encoded JSON document. Encoding and validation remain the
// caller's and PostgreSQL's responsibility; qs does not marshal Go values.
type JSONInput interface {
	~string | ~[]byte
}

// JSONDocument describes a json expression. Extraction can produce SQL NULL;
// the wrapper describes the SQL type, not a schema or nullability guarantee.
type JSONDocument struct{ expr Expr }

// JSONBDocument describes a jsonb expression and its PostgreSQL operators.
// Text extraction returns Field[string], whose payload can still be SQL NULL.
type JSONBDocument struct{ expr Expr }

func (JSONDocument) qxExpression()  {}
func (JSONBDocument) qxExpression() {}

// JSONCol returns a JSONDocument referring to a column.
func JSONCol(column string) JSONDocument { return JSONExpr(Col(column)) }

// JSONBCol returns a JSONBDocument referring to a column.
func JSONBCol(column string) JSONBDocument { return JSONBExpr(Col(column)) }

// JSONExpr asserts the SQL type of an existing expression without adding a
// cast. Use expr.Cast(JSON) when its SQL type is not already json.
func JSONExpr(expr Expr) JSONDocument { return JSONDocument{expr: expr} }

// JSONBExpr asserts the SQL type of an existing expression without adding a
// cast. Use expr.Cast(JSONB) when its SQL type is not already jsonb.
func JSONBExpr(expr Expr) JSONBDocument { return JSONBDocument{expr: expr} }

// JSONParam binds an encoded JSON value and casts the parameter to json.
func JSONParam[T JSONInput](value T) JSONDocument { return JSONExpr(Param(value).Cast(JSON)) }

// JSONBParam binds an encoded JSON value and casts the parameter to jsonb.
func JSONBParam[T JSONInput](value T) JSONBDocument { return JSONBExpr(Param(value).Cast(JSONB)) }

// Expr returns the underlying expression without adding a cast.
func (d JSONDocument) Expr() Expr { return d.expr }

// Expr returns the underlying expression without adding a cast.
func (d JSONBDocument) Expr() Expr { return d.expr }

// As returns the document expression with an SQL alias.
func (d JSONDocument) As(alias string) Expr { return d.expr.As(alias) }

// As returns the document expression with an SQL alias.
func (d JSONBDocument) As(alias string) Expr { return d.expr.As(alias) }

// IsNull returns a condition that tests whether the document is SQL NULL.
func (d JSONDocument) IsNull() Condition { return d.expr.IsNull() }

// IsNull returns a condition that tests whether the document is SQL NULL.
func (d JSONBDocument) IsNull() Condition { return d.expr.IsNull() }

// IsNotNull returns a condition that tests whether the document is not SQL NULL.
func (d JSONDocument) IsNotNull() Condition { return d.expr.IsNotNull() }

// IsNotNull returns a condition that tests whether the document is not SQL NULL.
func (d JSONBDocument) IsNotNull() Condition { return d.expr.IsNotNull() }

// Set returns an assignment that stores a JSON value in this document target.
func (d JSONDocument) Set(value JSONDocument) Assignment {
	return assignTarget(d.expr, value.expr)
}

// Set returns an assignment that stores a JSONB value in this document target.
func (d JSONBDocument) Set(value JSONBDocument) Assignment {
	return assignTarget(d.expr, value.expr)
}

// Key returns the JSON value at a text object key.
func (d JSONDocument) Key(key string) JSONDocument {
	return JSONExpr(JSONGet(d.expr, Param(key)))
}

// Key returns the JSONB value at a text object key.
func (d JSONBDocument) Key(key string) JSONBDocument {
	return JSONBExpr(JSONGet(d.expr, Param(key)))
}

// Index returns the JSON array element at index. PostgreSQL accepts negative
// indexes for elements counted from the end.
func (d JSONDocument) Index(index int) JSONDocument {
	return JSONExpr(JSONGet(d.expr, jsonIndex(index)))
}

// Index returns the JSONB array element at index. PostgreSQL accepts negative
// indexes for elements counted from the end.
func (d JSONBDocument) Index(index int) JSONBDocument {
	return JSONBExpr(JSONGet(d.expr, jsonIndex(index)))
}

// TextKey returns the text value at a JSON object key.
func (d JSONDocument) TextKey(key string) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, Param(key)))
}

// TextKey returns the text value at a JSONB object key.
func (d JSONBDocument) TextKey(key string) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, Param(key)))
}

// TextIndex returns the text value at a JSON array index.
func (d JSONDocument) TextIndex(index int) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, jsonIndex(index)))
}

// TextIndex returns the text value at a JSONB array index.
func (d JSONBDocument) TextIndex(index int) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, jsonIndex(index)))
}

// Path returns the JSON value selected by a sequence of object keys and array
// indexes using PostgreSQL's path extraction operator.
func (d JSONDocument) Path(parts ...string) JSONDocument {
	return JSONExpr(JSONGetPath(d.expr, jsonTextArray(parts)))
}

// Path returns the JSONB value selected by a sequence of object keys and array
// indexes using PostgreSQL's path extraction operator.
func (d JSONBDocument) Path(parts ...string) JSONBDocument {
	return JSONBExpr(JSONGetPath(d.expr, jsonTextArray(parts)))
}

// PathText returns the text value selected by a sequence of object keys and
// array indexes.
func (d JSONDocument) PathText(parts ...string) Field[string] {
	return TypedExpr[string](JSONGetPathText(d.expr, jsonTextArray(parts)))
}

// PathText returns the JSONB text value selected by a sequence of object keys
// and array indexes.
func (d JSONBDocument) PathText(parts ...string) Field[string] {
	return TypedExpr[string](JSONGetPathText(d.expr, jsonTextArray(parts)))
}

// Contains reports whether other is contained in this JSONB document.
func (d JSONBDocument) Contains(other JSONBDocument) Condition {
	return JSONContains(d.expr, other.expr)
}

// ContainedBy reports whether this JSONB document is contained in other.
func (d JSONBDocument) ContainedBy(other JSONBDocument) Condition {
	return JSONContainedBy(d.expr, other.expr)
}

// HasKey reports whether the JSONB object contains key.
func (d JSONBDocument) HasKey(key string) Condition { return JSONHas(d.expr, Param(key)) }

// HasAnyKeys reports whether the JSONB object contains at least one key.
func (d JSONBDocument) HasAnyKeys(keys ...string) Condition {
	return JSONHasAny(d.expr, jsonTextArray(keys))
}

// HasAllKeys reports whether the JSONB object contains every supplied key.
func (d JSONBDocument) HasAllKeys(keys ...string) Condition {
	return JSONHasAll(d.expr, jsonTextArray(keys))
}

// DeleteKey returns a JSONB expression with one object key removed.
func (d JSONBDocument) DeleteKey(key string) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, Param(key)))
}

// DeleteIndex returns a JSONB expression with one array element removed.
func (d JSONBDocument) DeleteIndex(index int) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, jsonIndex(index)))
}

// DeleteKeys returns a JSONB expression with the supplied object keys removed.
func (d JSONBDocument) DeleteKeys(keys ...string) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, jsonTextArray(keys)))
}

// DeletePath returns a JSONB expression with the value at a path removed.
func (d JSONBDocument) DeletePath(parts ...string) JSONBDocument {
	return JSONBExpr(JSONDeletePath(d.expr, jsonTextArray(parts)))
}

// Concat returns a JSONB expression that concatenates this document and other.
func (d JSONBDocument) Concat(other JSONBDocument) JSONBDocument {
	return JSONBExpr(d.expr.Concat(other.expr))
}

// PathExists returns a condition testing whether a JSONPath matches this
// document.
func (d JSONBDocument) PathExists(path string) Condition {
	return JSONPathExists(d.expr, Param(path).Cast(JSONPath))
}

// PathMatches returns a condition testing whether a JSONPath predicate matches
// this document.
func (d JSONBDocument) PathMatches(path string) Condition {
	return JSONPathMatches(d.expr, Param(path).Cast(JSONPath))
}

func jsonIndex(index int) Expr {
	if index < -1<<31 || index > 1<<31-1 {
		return invalidExpr("JSON index", "index must fit a PostgreSQL integer")
	}
	// The cast selects the array-index overload even when the driver sends an
	// unknown parameter type; an untyped bind can resolve to the text-key one.
	return Param(int32(index)).Cast(Int4)
}

func jsonTextArray(parts []string) Expr {
	expressions := make([]Expr, len(parts))
	for i, part := range parts {
		expressions[i] = Param(part)
	}
	// Individual binds avoid requiring a driver's array codec. The cast also
	// gives ARRAY[] a type; an empty extraction path is valid PostgreSQL syntax.
	return (Expr{kind: exprList, text: sqlArray, value: expressions}).Cast(ArrayType(Text))
}
