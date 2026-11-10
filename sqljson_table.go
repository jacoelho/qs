package qs

// This file owns JSON_TABLE. Its four column kinds are four types, so a column
// has only the clauses its kind accepts.

// JSONTableColumn is one entry of a JSON_TABLE COLUMNS list: a value column
// from JSONColumn, an EXISTS column from JSONExistsColumn, a FOR ORDINALITY
// column from JSONOrdinality, or a NESTED PATH group from JSONNested.
type JSONTableColumn interface{ jsonTableColumn() }

func (JSONTableValueColumn) jsonTableColumn()      {}
func (JSONTableExistsColumn) jsonTableColumn()     {}
func (JSONTableOrdinalityColumn) jsonTableColumn() {}
func (JSONTableNestedColumns) jsonTableColumn()    {}

// jsonColumnPath is a column's optional PATH. Without one PostgreSQL uses
// $.name.
type jsonColumnPath struct {
	path string
	set  bool
}

func (w *renderer) jsonColumnPath(p jsonColumnPath) {
	if p.set {
		w.text(" PATH ")
		w.jsonPath(p.path)
	}
}

// jsonPath writes a path constant. The empty string is not a jsonpath.
func (w *renderer) jsonPath(path string) {
	if w.require(path != "", "PATH", "requires a path") {
		w.renderStringLiteral(path)
	}
}

// JSONTableValueColumn is a JSON_TABLE column that extracts a value. It is an
// immutable value.
//
// PostgreSQL evaluates the column as JSON_QUERY when it has FORMAT JSON, a
// wrapper or quotes clause, or a JSON, array or composite type, and as
// JSON_VALUE otherwise. qs cannot see the type, so the behavior methods are
// the union of both functions' sets: PostgreSQL rejects an EMPTY ARRAY or
// EMPTY OBJECT behavior on a column it evaluates as JSON_VALUE.
type JSONTableValueColumn struct {
	name    string
	onEmpty jsonBehavior
	onError jsonBehavior
	path    jsonColumnPath
	typ     DataType
	shape   jsonShape
	format  jsonFormat
}

// JSONColumn declares a JSON_TABLE column of the given type.
func JSONColumn(name string, typ DataType) JSONTableValueColumn {
	return JSONTableValueColumn{name: name, typ: typ}
}

// Path replaces the column's SQL/JSON path, a string constant.
func (b JSONTableValueColumn) Path(path string) JSONTableValueColumn {
	b.path = jsonColumnPath{path: path, set: true}
	return b
}

// FormatJSON evaluates the column as JSON_QUERY, returning JSON text.
func (b JSONTableValueColumn) FormatJSON() JSONTableValueColumn {
	b.format = jsonFormatJSON
	return b
}

// FormatJSONUTF8 is FormatJSON for a bytea column, returning UTF-8 JSON text.
func (b JSONTableValueColumn) FormatJSONUTF8() JSONTableValueColumn {
	b.format = jsonFormatJSONUTF8
	return b
}

// JSONTableExistsColumn is a JSON_TABLE column that tests whether its path
// matches. It is an immutable value.
type JSONTableExistsColumn struct {
	name    string
	onError jsonBehavior
	path    jsonColumnPath
	typ     DataType
}

// JSONExistsColumn declares a JSON_TABLE EXISTS column of the given type.
func JSONExistsColumn(name string, typ DataType) JSONTableExistsColumn {
	return JSONTableExistsColumn{name: name, typ: typ}
}

// Path replaces the column's SQL/JSON path, a string constant.
func (b JSONTableExistsColumn) Path(path string) JSONTableExistsColumn {
	b.path = jsonColumnPath{path: path, set: true}
	return b
}

// JSONTableOrdinalityColumn is a JSON_TABLE FOR ORDINALITY column, which
// numbers the rows of its level from one.
type JSONTableOrdinalityColumn struct{ name string }

// JSONOrdinality declares a JSON_TABLE FOR ORDINALITY column.
func JSONOrdinality(name string) JSONTableOrdinalityColumn {
	return JSONTableOrdinalityColumn{name: name}
}

// JSONTableNestedColumns is a JSON_TABLE NESTED PATH group, whose columns are
// evaluated for each item its path selects. It is an immutable value.
type JSONTableNestedColumns struct {
	path     string
	pathName string
	columns  []JSONTableColumn
}

// JSONNested declares a NESTED PATH group with its columns.
func JSONNested(path string, first JSONTableColumn, rest ...JSONTableColumn) JSONTableNestedColumns {
	return JSONTableNestedColumns{path: path, columns: ownedList(first, rest)}
}

// PathName names the group's path, as in NESTED PATH '$.a' AS name.
func (b JSONTableNestedColumns) PathName(name string) JSONTableNestedColumns {
	b.pathName = name
	return b
}

// JSONTableBuilder is a JSON_TABLE row source. It is an immutable value;
// Ref or As makes it a Relation.
type JSONTableBuilder struct {
	path     string
	pathName string
	columns  []JSONTableColumn
	passing  []jsonPassing
	document jsonOperand
	onError  jsonBehavior
}

// JSONTable constructs JSON_TABLE, which turns the items a SQL/JSON path
// selects from document into rows. The path is a string constant: PostgreSQL
// accepts no other expression there. Column and path names must be distinct
// in their first 63 bytes, which is what PostgreSQL keeps of a name; a
// repeated name fails rendering. So does the name json_table_path_0 while the
// row path has no PathName, because PostgreSQL gives that name to the row
// path.
func JSONTable(document JSONOperand, path string, first JSONTableColumn, rest ...JSONTableColumn) JSONTableBuilder {
	return JSONTableBuilder{document: operandOf(document), path: path, columns: ownedList(first, rest)}
}

// PathName names the row path, as in JSON_TABLE(doc, '$[*]' AS name ...).
func (b JSONTableBuilder) PathName(name string) JSONTableBuilder {
	b.pathName = name
	return b
}

// Ref returns the table as a Relation without an alias.
func (b JSONTableBuilder) Ref() Relation { return Relation{kind: relationSource, source: &b} }

// As returns the table as a Relation with alias and optional column aliases,
// which are copied. More aliases than the table has columns fail rendering.
func (b JSONTableBuilder) As(alias string, columns ...string) Relation {
	return b.Ref().As(alias, columns...)
}

func (w *renderer) jsonTable(b *JSONTableBuilder) {
	const clause = "JSON_TABLE"
	w.text("JSON_TABLE(")
	w.jsonOperand(b.document)
	if w.stopped("document", 0) {
		return
	}
	w.text(", ")
	w.jsonPath(b.path)
	names := make(jsonTableNames)
	if b.pathName == "" {
		names[jsonTableGeneratedRootPath] = "json_table_path_0 is the name PostgreSQL gives the unnamed row path"
	}
	w.jsonPathName(b.pathName, names)
	w.jsonPassing(b.passing)
	if w.err != nil {
		return
	}
	w.jsonColumns(b.columns, names)
	if w.err != nil {
		w.errorPath(clause, 0)
		return
	}
	w.jsonBehavior(b.onError, jsonOnError)
	w.byte(')')
}

// jsonTableWidth counts the columns a JSON_TABLE returns: every column of
// every level except the NESTED PATH groups themselves. It returns -1 when the
// tree is deeper than depth, which rendering reports on its own.
func jsonTableWidth(columns []JSONTableColumn, depth int) int {
	if depth <= 0 {
		return -1
	}
	width := 0
	for _, column := range columns {
		nested, ok := column.(JSONTableNestedColumns)
		if !ok {
			width++
			continue
		}
		inner := jsonTableWidth(nested.columns, depth-1)
		if inner < 0 {
			return -1
		}
		width += inner
	}
	return width
}

// jsonTableNames holds every column and path name of one JSON_TABLE, which
// PostgreSQL requires to be distinct. A key is the name as the server keeps
// it; its value is the failure a second use reports.
type jsonTableNames map[string]string

// jsonTableGeneratedRootPath is the name PostgreSQL gives a row path that has
// none. It takes that name before it checks the explicit ones, so an explicit
// use is then a duplicate. The names it generates for unnamed NESTED PATH
// groups come after that check and collide with nothing.
const jsonTableGeneratedRootPath = "json_table_path_0"

// jsonPathName writes AS name for a named path.
func (w *renderer) jsonPathName(name string, names jsonTableNames) {
	if name == "" || w.err != nil {
		return
	}
	w.text(" AS ")
	w.jsonTableName(name, names)
}

func (w *renderer) jsonTableName(name string, names jsonTableNames) {
	key := truncatedIdentifier(name)
	if detail, taken := names[key]; taken {
		w.fail(ErrInvalid, "JSON_TABLE", detail)
		return
	}
	names[key] = "duplicate column or path name"
	w.identifierPart(name)
}

func (w *renderer) jsonColumns(columns []JSONTableColumn, names jsonTableNames) {
	if !w.enter() {
		return
	}
	defer func() { w.depth-- }()
	if !w.require(len(columns) > 0, "COLUMNS", "requires at least one column") {
		return
	}
	w.text(" COLUMNS (")
	for i, column := range columns {
		if i != 0 {
			w.text(", ")
		}
		w.jsonColumn(column, names)
		if w.stopped("column", i+1) {
			return
		}
	}
	w.byte(')')
}

func (w *renderer) jsonColumn(column JSONTableColumn, names jsonTableNames) {
	switch c := column.(type) {
	case JSONTableOrdinalityColumn:
		w.jsonTableName(c.name, names)
		w.text(" FOR ORDINALITY")
	case JSONTableValueColumn:
		w.jsonTableName(c.name, names)
		w.byte(' ')
		w.dataType(c.typ)
		w.jsonFormat(c.format)
		w.jsonColumnPath(c.path)
		w.jsonShape(c.shape)
		w.jsonBehavior(c.onEmpty, jsonOnEmpty)
		w.jsonBehavior(c.onError, jsonOnError)
	case JSONTableExistsColumn:
		w.jsonTableName(c.name, names)
		w.byte(' ')
		w.dataType(c.typ)
		w.text(" EXISTS")
		w.jsonColumnPath(c.path)
		w.jsonBehavior(c.onError, jsonOnError)
	case JSONTableNestedColumns:
		w.text("NESTED PATH ")
		w.jsonPath(c.path)
		w.jsonPathName(c.pathName, names)
		w.jsonColumns(c.columns, names)
	default:
		w.fail(ErrInvalid, "COLUMNS", "nil column")
	}
}
