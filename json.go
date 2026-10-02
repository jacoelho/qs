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

func JSONCol(column string) JSONDocument   { return JSONExpr(Col(column)) }
func JSONBCol(column string) JSONBDocument { return JSONBExpr(Col(column)) }

// JSONExpr asserts the SQL type of an existing expression without adding a
// cast. Use expr.Cast(JSON) when its SQL type is not already json.
func JSONExpr(expr Expr) JSONDocument { return JSONDocument{expr: expr} }

// JSONBExpr asserts the SQL type of an existing expression without adding a
// cast. Use expr.Cast(JSONB) when its SQL type is not already jsonb.
func JSONBExpr(expr Expr) JSONBDocument { return JSONBDocument{expr: expr} }

func JSONParam[T JSONInput](value T) JSONDocument   { return JSONExpr(Param(value).Cast(JSON)) }
func JSONBParam[T JSONInput](value T) JSONBDocument { return JSONBExpr(Param(value).Cast(JSONB)) }

func (d JSONDocument) Expr() Expr            { return d.expr }
func (d JSONBDocument) Expr() Expr           { return d.expr }
func (d JSONDocument) As(alias string) Expr  { return d.expr.As(alias) }
func (d JSONBDocument) As(alias string) Expr { return d.expr.As(alias) }

func (d JSONDocument) IsNull() Condition     { return d.expr.IsNull() }
func (d JSONBDocument) IsNull() Condition    { return d.expr.IsNull() }
func (d JSONDocument) IsNotNull() Condition  { return d.expr.IsNotNull() }
func (d JSONBDocument) IsNotNull() Condition { return d.expr.IsNotNull() }
func (d JSONDocument) Set(value JSONDocument) Assignment {
	return assignTarget(d.expr, value.expr)
}
func (d JSONBDocument) Set(value JSONBDocument) Assignment {
	return assignTarget(d.expr, value.expr)
}

func (d JSONDocument) Key(key string) JSONDocument {
	return JSONExpr(JSONGet(d.expr, Param(key)))
}
func (d JSONBDocument) Key(key string) JSONBDocument {
	return JSONBExpr(JSONGet(d.expr, Param(key)))
}
func (d JSONDocument) Index(index int) JSONDocument {
	return JSONExpr(JSONGet(d.expr, jsonIndex(index)))
}
func (d JSONBDocument) Index(index int) JSONBDocument {
	return JSONBExpr(JSONGet(d.expr, jsonIndex(index)))
}
func (d JSONDocument) TextKey(key string) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, Param(key)))
}
func (d JSONBDocument) TextKey(key string) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, Param(key)))
}
func (d JSONDocument) TextIndex(index int) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, jsonIndex(index)))
}
func (d JSONBDocument) TextIndex(index int) Field[string] {
	return TypedExpr[string](JSONGetText(d.expr, jsonIndex(index)))
}
func (d JSONDocument) Path(parts ...string) JSONDocument {
	return JSONExpr(JSONGetPath(d.expr, jsonTextArray(parts)))
}
func (d JSONBDocument) Path(parts ...string) JSONBDocument {
	return JSONBExpr(JSONGetPath(d.expr, jsonTextArray(parts)))
}
func (d JSONDocument) PathText(parts ...string) Field[string] {
	return TypedExpr[string](JSONGetPathText(d.expr, jsonTextArray(parts)))
}
func (d JSONBDocument) PathText(parts ...string) Field[string] {
	return TypedExpr[string](JSONGetPathText(d.expr, jsonTextArray(parts)))
}

func (d JSONBDocument) Contains(other JSONBDocument) Condition {
	return JSONContains(d.expr, other.expr)
}
func (d JSONBDocument) ContainedBy(other JSONBDocument) Condition {
	return JSONContainedBy(d.expr, other.expr)
}
func (d JSONBDocument) HasKey(key string) Condition { return JSONHas(d.expr, Param(key)) }
func (d JSONBDocument) HasAnyKeys(keys ...string) Condition {
	return JSONHasAny(d.expr, jsonTextArray(keys))
}
func (d JSONBDocument) HasAllKeys(keys ...string) Condition {
	return JSONHasAll(d.expr, jsonTextArray(keys))
}
func (d JSONBDocument) DeleteKey(key string) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, Param(key)))
}
func (d JSONBDocument) DeleteIndex(index int) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, jsonIndex(index)))
}
func (d JSONBDocument) DeleteKeys(keys ...string) JSONBDocument {
	return JSONBExpr(JSONDelete(d.expr, jsonTextArray(keys)))
}
func (d JSONBDocument) DeletePath(parts ...string) JSONBDocument {
	return JSONBExpr(JSONDeletePath(d.expr, jsonTextArray(parts)))
}
func (d JSONBDocument) Concat(other JSONBDocument) JSONBDocument {
	return JSONBExpr(d.expr.Concat(other.expr))
}
func (d JSONBDocument) PathExists(path string) Condition {
	return JSONPathExists(d.expr, Param(path).Cast(JSONPath))
}
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
	return (Expr{kind: exprList, text: "ARRAY", value: expressions}).Cast(ArrayType(Text))
}
