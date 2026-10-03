package qs

import "slices"

// XMLMode selects whether an XML value is treated as a document or as content.
// PostgreSQL uses the same distinction for XMLPARSE, XMLSERIALIZE and XMLROOT.
type XMLMode uint8

const (
	// XMLContent selects XML content rather than a complete XML document.
	XMLContent XMLMode = iota + 1
	// XMLDocument selects a complete XML document.
	XMLDocument
)

// XMLWhitespaceMode controls XMLPARSE whitespace handling. The zero value means
// that no whitespace clause is emitted, which selects PostgreSQL's default.
type XMLWhitespaceMode uint8

const (
	// XMLWhitespaceDefault omits a whitespace clause and uses PostgreSQL's default.
	XMLWhitespaceDefault XMLWhitespaceMode = iota
	// XMLPreserveWhitespace preserves whitespace while parsing XML.
	XMLPreserveWhitespace
	// XMLStripWhitespace strips whitespace while parsing XML.
	XMLStripWhitespace
)

// XMLStandalone controls the optional XMLROOT STANDALONE clause.
type XMLStandalone uint8

const (
	// XMLStandaloneOmitted omits the XMLROOT STANDALONE clause.
	XMLStandaloneOmitted XMLStandalone = iota
	// XMLStandaloneYes emits STANDALONE YES.
	XMLStandaloneYes
	// XMLStandaloneNo emits STANDALONE NO.
	XMLStandaloneNo
	// XMLStandaloneNoValue emits STANDALONE NO VALUE.
	XMLStandaloneNoValue
)

// XMLPassingMode controls the optional XML PASSING mechanism. PostgreSQL
// accepts the clauses but currently ignores their by-reference/value meaning.
type XMLPassingMode uint8

const (
	// XMLPassingDefault omits an XML PASSING mode clause.
	XMLPassingDefault XMLPassingMode = iota
	// XMLPassingByRef emits PASSING BY REF.
	XMLPassingByRef
	// XMLPassingByValue emits PASSING BY VALUE.
	XMLPassingByValue
)

type xmlKind uint8

const (
	xmlConcat xmlKind = iota + 1
	xmlElement
	xmlForest
	xmlParse
	xmlPI
	xmlRoot
	xmlSerialize
	xmlExists
	xmlIsDocument
)

// XMLItem is an expression optionally paired with a SQL/XML name. It is used
// for XMLATTRIBUTES and XMLFOREST; unnamed items retain PostgreSQL's derived
// name behaviour.
type XMLItem struct {
	name    string
	value   Expr
	hasName bool
}

// XMLValue creates an unnamed XML item. Use As to supply the XML name.
func XMLValue(value Expr) XMLItem { return XMLItem{value: value} }

// XMLAttr creates a named XMLATTRIBUTES/XMLFOREST item.
func XMLAttr(name string, value Expr) XMLItem {
	return XMLItem{value: value, name: name, hasName: true}
}

// As supplies the SQL/XML name for an item. The returned descriptor is a new
// value, so branches built from one item cannot mutate each other.
func (v XMLItem) As(name string) XMLItem {
	v.name = name
	v.hasName = true
	return v
}

type xmlExpression struct {
	name        string
	value       Expr
	path        Expr
	document    Expr
	version     Expr
	items       []XMLItem
	values      []Expr
	typ         DataType
	hasVersion  bool
	noVersion   bool
	mode        XMLMode
	whitespace  XMLWhitespaceMode
	standalone  XMLStandalone
	passing     XMLPassingMode
	notDocument bool
	kind        xmlKind
	hasIndent   bool
	indent      bool
}

func xmlExpr(x xmlExpression) Expr {
	return Expr{kind: exprXML, value: &x}
}

// XMLConcat constructs XMLCONCAT.
func XMLConcat(values ...Expr) Expr {
	if len(values) == 0 {
		return invalidExpr("XMLCONCAT", "requires at least one expression")
	}
	return xmlExpr(xmlExpression{kind: xmlConcat, values: cloneSlice(values)})
}

// XMLElementBuilder is an immutable XMLELEMENT descriptor.
type XMLElementBuilder struct {
	name       string
	attributes []XMLItem
	content    []Expr
}

// XMLElement starts an XMLELEMENT descriptor. Content can also be supplied
// later with Content; attributes always render before content as required by
// PostgreSQL's grammar.
func XMLElement(name string, content ...Expr) XMLElementBuilder {
	return XMLElementBuilder{name: name, content: cloneSlice(content)}
}

// Attributes appends XMLATTRIBUTES entries to the XMLELEMENT descriptor.
func (b XMLElementBuilder) Attributes(attributes ...XMLItem) XMLElementBuilder {
	b.attributes = slices.Concat(b.attributes, attributes)
	return b
}

// Content appends content expressions to the XMLELEMENT descriptor.
func (b XMLElementBuilder) Content(content ...Expr) XMLElementBuilder {
	b.content = slices.Concat(b.content, content)
	return b
}

// Expr freezes the XMLELEMENT descriptor as an expression.
func (b XMLElementBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{
		kind:   xmlElement,
		name:   b.name,
		items:  b.attributes,
		values: b.content,
	})
}

// As freezes the XMLELEMENT descriptor as an aliased expression.
func (b XMLElementBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLForestBuilder is an immutable XMLFOREST descriptor.
type XMLForestBuilder struct{ items []XMLItem }

// XMLForest constructs an XMLFOREST descriptor from named or unnamed items.
func XMLForest(items ...XMLItem) XMLForestBuilder {
	return XMLForestBuilder{items: cloneSlice(items)}
}

// XMLForestExpr is a convenience for a forest whose names are all derived from
// the input expressions.
func XMLForestExpr(values ...Expr) Expr {
	items := make([]XMLItem, len(values))
	for i := range values {
		items[i] = XMLValue(values[i])
	}
	return xmlExpr(xmlExpression{kind: xmlForest, items: items})
}

// Values appends items to the XMLFOREST descriptor.
func (b XMLForestBuilder) Values(items ...XMLItem) XMLForestBuilder {
	b.items = slices.Concat(b.items, items)
	return b
}

// Expr freezes the XMLFOREST descriptor as an expression.
func (b XMLForestBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{kind: xmlForest, items: b.items})
}

// As freezes the XMLFOREST descriptor as an aliased expression.
func (b XMLForestBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLParseBuilder is an immutable XMLPARSE descriptor.
type XMLParseBuilder struct {
	value      Expr
	mode       XMLMode
	whitespace XMLWhitespaceMode
}

// XMLParse constructs an XMLPARSE descriptor for value.
func XMLParse(mode XMLMode, value Expr) XMLParseBuilder {
	return XMLParseBuilder{mode: mode, value: value}
}

// PreserveWhitespace requests PRESERVE WHITESPACE for XMLPARSE.
func (b XMLParseBuilder) PreserveWhitespace() XMLParseBuilder {
	b.whitespace = XMLPreserveWhitespace
	return b
}

// StripWhitespace requests STRIP WHITESPACE for XMLPARSE.
func (b XMLParseBuilder) StripWhitespace() XMLParseBuilder {
	b.whitespace = XMLStripWhitespace
	return b
}

// Expr freezes the XMLPARSE descriptor as an expression.
func (b XMLParseBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{
		kind:       xmlParse,
		mode:       b.mode,
		value:      b.value,
		whitespace: b.whitespace,
	})
}

// As freezes the XMLPARSE descriptor as an aliased expression.
func (b XMLParseBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLPIBuilder is an immutable XMLPI descriptor.
type XMLPIBuilder struct {
	name  string
	value []Expr
}

// XMLPI constructs an XML processing-instruction descriptor.
func XMLPI(name string, value ...Expr) XMLPIBuilder {
	return XMLPIBuilder{name: name, value: cloneSlice(value)}
}

// Content appends the optional content expression to XMLPI.
func (b XMLPIBuilder) Content(value Expr) XMLPIBuilder {
	b.value = slices.Concat(b.value, []Expr{value})
	return b
}

// Expr freezes the XMLPI descriptor as an expression.
func (b XMLPIBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{kind: xmlPI, name: b.name, values: b.value})
}

// As freezes the XMLPI descriptor as an aliased expression.
func (b XMLPIBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLRootBuilder is an immutable XMLROOT descriptor. PostgreSQL requires a
// VERSION expression or VERSION NO VALUE; the renderer rejects an omitted one.
type XMLRootBuilder struct {
	value      Expr
	version    Expr
	hasVersion bool
	noVersion  bool
	standalone XMLStandalone
}

// XMLRoot constructs an XMLROOT descriptor. At most one VERSION expression may
// be supplied; use VersionNoValue to emit VERSION NO VALUE.
func XMLRoot(value Expr, version ...Expr) XMLRootBuilder {
	b := XMLRootBuilder{value: value}
	if len(version) == 1 {
		b.version = version[0]
		b.hasVersion = true
	}
	if len(version) > 1 {
		b.hasVersion = true
		b.version = invalidExpr("XMLROOT", "accepts at most one VERSION expression")
	}
	return b
}

// Version sets the XMLROOT VERSION expression.
func (b XMLRootBuilder) Version(version Expr) XMLRootBuilder {
	b.version = version
	b.hasVersion = true
	b.noVersion = false
	return b
}

// VersionNoValue selects XMLROOT VERSION NO VALUE.
func (b XMLRootBuilder) VersionNoValue() XMLRootBuilder {
	b.version = Expr{}
	b.hasVersion = true
	b.noVersion = true
	return b
}

// Standalone selects the XMLROOT STANDALONE clause.
func (b XMLRootBuilder) Standalone(value XMLStandalone) XMLRootBuilder {
	b.standalone = value
	return b
}

// Expr freezes the XMLROOT descriptor as an expression.
func (b XMLRootBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{
		kind:       xmlRoot,
		value:      b.value,
		version:    b.version,
		hasVersion: b.hasVersion,
		noVersion:  b.noVersion,
		standalone: b.standalone,
	})
}

// As freezes the XMLROOT descriptor as an aliased expression.
func (b XMLRootBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLSerializeBuilder is an immutable XMLSERIALIZE descriptor.
type XMLSerializeBuilder struct {
	value     Expr
	typ       DataType
	mode      XMLMode
	hasIndent bool
	indent    bool
}

// XMLSerialize constructs an XMLSERIALIZE descriptor for value.
func XMLSerialize(mode XMLMode, value Expr, typ DataType) XMLSerializeBuilder {
	return XMLSerializeBuilder{mode: mode, value: value, typ: typ}
}

// Indent requests INDENT for XMLSERIALIZE.
func (b XMLSerializeBuilder) Indent() XMLSerializeBuilder {
	b.hasIndent = true
	b.indent = true
	return b
}

// NoIndent requests NO INDENT for XMLSERIALIZE.
func (b XMLSerializeBuilder) NoIndent() XMLSerializeBuilder {
	b.hasIndent = true
	b.indent = false
	return b
}

// Expr freezes the XMLSERIALIZE descriptor as an expression.
func (b XMLSerializeBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{
		kind:      xmlSerialize,
		mode:      b.mode,
		value:     b.value,
		typ:       b.typ,
		hasIndent: b.hasIndent,
		indent:    b.indent,
	})
}

// As freezes the XMLSERIALIZE descriptor as an aliased expression.
func (b XMLSerializeBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLExistsBuilder is an immutable XMLEXISTS descriptor. Use Expr in a
// projection or Condition in WHERE/JOIN predicates.
type XMLExistsBuilder struct {
	path     Expr
	document Expr
	passing  XMLPassingMode
}

// XMLExists constructs an XMLEXISTS descriptor for path and document.
func XMLExists(path, document Expr) XMLExistsBuilder {
	return XMLExistsBuilder{path: path, document: document}
}

// ByRef selects PASSING BY REF for XMLEXISTS.
func (b XMLExistsBuilder) ByRef() XMLExistsBuilder {
	b.passing = XMLPassingByRef
	return b
}

// ByValue selects PASSING BY VALUE for XMLEXISTS.
func (b XMLExistsBuilder) ByValue() XMLExistsBuilder {
	b.passing = XMLPassingByValue
	return b
}

// Passing selects the PASSING mode for XMLEXISTS.
func (b XMLExistsBuilder) Passing(mode XMLPassingMode) XMLExistsBuilder {
	b.passing = mode
	return b
}

// Expr freezes the XMLEXISTS descriptor as an expression.
func (b XMLExistsBuilder) Expr() Expr {
	return xmlExpr(xmlExpression{
		kind:     xmlExists,
		path:     b.path,
		document: b.document,
		passing:  b.passing,
	})
}

// Condition converts XMLEXISTS to a condition for WHERE or JOIN predicates.
func (b XMLExistsBuilder) Condition() Condition { return AsCondition(b.Expr()) }

// As freezes the XMLEXISTS descriptor as an aliased expression.
func (b XMLExistsBuilder) As(alias string) Expr { return b.Expr().As(alias) }

// XMLIsDocument tests whether an XML value has document shape. PostgreSQL
// evaluates XML validity and document shape; qs only owns the SQL syntax.
func XMLIsDocument(value Expr) Condition {
	return AsCondition(xmlExpr(xmlExpression{kind: xmlIsDocument, value: value}))
}

// XMLIsNotDocument tests whether an XML value is not a document.
func XMLIsNotDocument(value Expr) Condition {
	return AsCondition(xmlExpr(xmlExpression{kind: xmlIsDocument, value: value, notDocument: true}))
}

// IsDocument tests whether the expression has XML document shape.
func (e Expr) IsDocument() Condition { return XMLIsDocument(e) }

// IsNotDocument tests whether the expression does not have XML document shape.
func (e Expr) IsNotDocument() Condition { return XMLIsNotDocument(e) }

// XMLNamespaceSpec describes one XMLNAMESPACES entry. The defaultNS flag
// selects the DEFAULT form accepted by PostgreSQL's grammar.
type XMLNamespaceSpec struct {
	name      string
	uri       Expr
	defaultNS bool
}

// XMLNamespace creates a prefixed XML namespace declaration. The URI is an
// expression so callers can bind it instead of interpolating a value.
func XMLNamespace(uri Expr, name string) XMLNamespaceSpec {
	return XMLNamespaceSpec{uri: uri, name: name}
}

// XMLDefaultNamespace creates a default XML namespace declaration.
func XMLDefaultNamespace(uri Expr) XMLNamespaceSpec {
	return XMLNamespaceSpec{uri: uri, defaultNS: true}
}

// XMLTableColumn describes one XMLTABLE output column.
type XMLTableColumn struct {
	name        string
	path        Expr
	defaultExpr Expr
	typ         DataType
	kind        xmlTableColumnKind
	hasPath     bool
	hasDefault  bool
	nullability xmlNullability
}

type xmlTableColumnKind uint8

const (
	xmlTableValueColumn xmlTableColumnKind = iota + 1
	xmlTableOrdinality
)

func (k xmlTableColumnKind) valid() bool {
	return k == xmlTableValueColumn || k == xmlTableOrdinality
}

type xmlNullability uint8

const (
	xmlNullabilityUnspecified xmlNullability = iota
	xmlNullable
	xmlNotNull
)

func (n xmlNullability) valid() bool {
	return n >= xmlNullabilityUnspecified && n <= xmlNotNull
}

// XMLColumn creates an XMLTABLE value column with a SQL type.
func XMLColumn(name string, typ DataType) XMLTableColumn {
	return XMLTableColumn{kind: xmlTableValueColumn, name: name, typ: typ}
}

// XMLOrdinality creates an XMLTABLE ordinality column.
func XMLOrdinality(name string) XMLTableColumn {
	return XMLTableColumn{kind: xmlTableOrdinality, name: name}
}

// Path sets the XPath expression used to populate an XMLTABLE value column.
func (c XMLTableColumn) Path(path Expr) XMLTableColumn {
	c.path = path
	c.hasPath = true
	return c
}

// Default sets the expression used when an XMLTABLE value column is absent.
func (c XMLTableColumn) Default(value Expr) XMLTableColumn {
	c.defaultExpr = value
	c.hasDefault = true
	return c
}

// NotNull requires an XMLTABLE value column to be NOT NULL.
func (c XMLTableColumn) NotNull() XMLTableColumn {
	c.nullability = xmlNotNull
	return c
}

// Null explicitly marks an XMLTABLE value column as nullable.
func (c XMLTableColumn) Null() XMLTableColumn {
	c.nullability = xmlNullable
	return c
}

// XMLTableBuilder is an immutable XMLTABLE relation descriptor.
type XMLTableBuilder struct {
	rowPath    Expr
	document   Expr
	namespaces []XMLNamespaceSpec
	columns    []XMLTableColumn
	passing    XMLPassingMode
}

// XMLTable constructs an XMLTABLE relation descriptor.
func XMLTable(rowPath, document Expr, columns ...XMLTableColumn) XMLTableBuilder {
	return XMLTableBuilder{rowPath: rowPath, document: document, columns: cloneSlice(columns)}
}

// Columns appends output columns to XMLTABLE.
func (b XMLTableBuilder) Columns(columns ...XMLTableColumn) XMLTableBuilder {
	b.columns = slices.Concat(b.columns, columns)
	return b
}

// Namespaces appends XMLNAMESPACES declarations to XMLTABLE.
func (b XMLTableBuilder) Namespaces(namespaces ...XMLNamespaceSpec) XMLTableBuilder {
	b.namespaces = slices.Concat(b.namespaces, namespaces)
	return b
}

// ByRef selects PASSING BY REF for XMLTABLE.
func (b XMLTableBuilder) ByRef() XMLTableBuilder {
	b.passing = XMLPassingByRef
	return b
}

// ByValue selects PASSING BY VALUE for XMLTABLE.
func (b XMLTableBuilder) ByValue() XMLTableBuilder {
	b.passing = XMLPassingByValue
	return b
}

// Passing selects the PASSING mode for XMLTABLE.
func (b XMLTableBuilder) Passing(mode XMLPassingMode) XMLTableBuilder {
	b.passing = mode
	return b
}

// Ref freezes XMLTABLE as a relation.
func (b XMLTableBuilder) Ref() Relation {
	return Relation{kind: relationXMLTable, value: &b}
}

// As freezes XMLTABLE as a relation with a table alias and optional column aliases.
func (b XMLTableBuilder) As(name string, columns ...string) Relation {
	return b.Ref().As(name, columns...)
}

func (w *renderer) xml(e Expr) {
	x := ownedPayload[*xmlExpression](e.value)
	switch x.kind {
	case xmlConcat:
		w.xmlConcat(x)
	case xmlElement:
		w.xmlElement(x)
	case xmlForest:
		w.xmlForest(x)
	case xmlParse:
		w.xmlParse(x)
	case xmlPI:
		w.xmlPI(x)
	case xmlRoot:
		w.xmlRoot(x)
	case xmlSerialize:
		w.xmlSerialize(x)
	case xmlExists:
		w.xmlExists(x)
	case xmlIsDocument:
		w.xmlDocumentPredicate(x)
	default:
		w.fail(ErrInvalid, "XML", "unknown XML expression")
	}
}

func (w *renderer) xmlConcat(x *xmlExpression) {
	if !w.require(len(x.values) > 0, "XMLCONCAT", "requires at least one expression") {
		return
	}
	w.text("XMLCONCAT(")
	w.exprs(x.values, ", ")
	w.byte(')')
}

func (w *renderer) xmlElement(x *xmlExpression) {
	w.text("XMLELEMENT(NAME ")
	w.identifierPart(x.name)
	if len(x.items) > 0 {
		w.text(", XMLATTRIBUTES(")
		w.xmlItems(x.items)
		w.byte(')')
	}
	if len(x.values) > 0 {
		w.text(", ")
		w.exprs(x.values, ", ")
	}
	w.byte(')')
}

func (w *renderer) xmlForest(x *xmlExpression) {
	if !w.require(len(x.items) > 0, "XMLFOREST", "requires at least one expression") {
		return
	}
	w.text("XMLFOREST(")
	w.xmlItems(x.items)
	w.byte(')')
}

func (w *renderer) xmlParse(x *xmlExpression) {
	if !w.xmlMode(x.mode, "XMLPARSE") {
		return
	}
	w.text("XMLPARSE(")
	w.xmlModeText(x.mode)
	w.byte(' ')
	w.expr(x.value)
	switch x.whitespace {
	case XMLWhitespaceDefault:
	case XMLPreserveWhitespace:
		w.text(" PRESERVE WHITESPACE")
	case XMLStripWhitespace:
		w.text(" STRIP WHITESPACE")
	default:
		w.fail(ErrInvalid, "XMLPARSE", "unknown whitespace mode")
	}
	w.byte(')')
}

func (w *renderer) xmlPI(x *xmlExpression) {
	if !w.require(len(x.values) <= 1, "XMLPI", "accepts at most one expression") {
		return
	}
	w.text("XMLPI(NAME ")
	w.identifierPart(x.name)
	if len(x.values) == 1 {
		w.text(", ")
		w.expr(x.values[0])
	}
	w.byte(')')
}

func (w *renderer) xmlRoot(x *xmlExpression) {
	if !w.require(x.hasVersion, "XMLROOT", "requires VERSION or VERSION NO VALUE") {
		return
	}
	w.text("XMLROOT(")
	w.expr(x.value)
	w.text(", VERSION ")
	if x.noVersion {
		w.text("NO VALUE")
	} else {
		w.expr(x.version)
	}
	switch x.standalone {
	case XMLStandaloneOmitted:
	case XMLStandaloneYes:
		w.text(", STANDALONE YES")
	case XMLStandaloneNo:
		w.text(", STANDALONE NO")
	case XMLStandaloneNoValue:
		w.text(", STANDALONE NO VALUE")
	default:
		w.fail(ErrInvalid, "XMLROOT", "unknown STANDALONE mode")
	}
	w.byte(')')
}

func (w *renderer) xmlSerialize(x *xmlExpression) {
	if !w.xmlMode(x.mode, "XMLSERIALIZE") {
		return
	}
	w.text("XMLSERIALIZE(")
	w.xmlModeText(x.mode)
	w.byte(' ')
	w.expr(x.value)
	w.text(" AS ")
	w.dataType(x.typ)
	if x.hasIndent {
		w.feature(PostgreSQL16, "XMLSERIALIZE INDENT")
		if x.indent {
			w.text(" INDENT")
		} else {
			w.text(" NO INDENT")
		}
	}
	w.byte(')')
}

func (w *renderer) xmlExists(x *xmlExpression) {
	w.text("XMLEXISTS(")
	w.xmlCExpr(x.path)
	w.text(" PASSING")
	switch x.passing {
	case XMLPassingDefault:
	case XMLPassingByRef:
		w.text(" BY REF")
	case XMLPassingByValue:
		w.text(" BY VALUE")
	default:
		w.fail(ErrInvalid, "XMLEXISTS", "unknown PASSING mode")
		return
	}
	w.byte(' ')
	w.xmlCExpr(x.document)
	w.byte(')')
}

func (w *renderer) xmlDocumentPredicate(x *xmlExpression) {
	w.expr(x.value)
	if x.notDocument {
		w.text(" IS NOT DOCUMENT")
	} else {
		w.text(" IS DOCUMENT")
	}
}

func (w *renderer) xmlMode(mode XMLMode, clause string) bool {
	if mode != XMLContent && mode != XMLDocument {
		w.fail(ErrInvalid, clause, "unknown document/content mode")
		return false
	}
	return true
}

func (w *renderer) xmlModeText(mode XMLMode) {
	if mode == XMLDocument {
		w.text("DOCUMENT")
	} else {
		w.text("CONTENT")
	}
}

// xmlCExpr groups an expression in a PostgreSQL c_expr grammar slot. A cast
// rendered as ($1)::type is an a_expr, so the complete expression must be
// parenthesized before it can precede PASSING or COLUMNS.
func (w *renderer) xmlCExpr(e Expr) {
	w.byte('(')
	w.expr(e)
	w.byte(')')
}

func (w *renderer) xmlItems(items []XMLItem) {
	for i, item := range items {
		if i > 0 {
			w.text(", ")
		}
		w.expr(item.value)
		if item.hasName {
			w.text(" AS ")
			w.identifierPart(item.name)
		}
		if w.err != nil {
			return
		}
	}
}

func (w *renderer) xmlTable(b *XMLTableBuilder) {
	if !w.validXMLTable(b) {
		return
	}

	w.text("XMLTABLE(")
	w.xmlTableNamespaces(b.namespaces)
	w.xmlCExpr(b.rowPath)
	w.text(" PASSING")
	switch b.passing {
	case XMLPassingDefault:
	case XMLPassingByRef:
		w.text(" BY REF")
	case XMLPassingByValue:
		w.text(" BY VALUE")
	}
	w.byte(' ')
	w.xmlCExpr(b.document)
	w.text(" COLUMNS ")
	w.xmlTableColumns(b.columns)
	w.byte(')')
}

func (w *renderer) validXMLTable(b *XMLTableBuilder) bool {
	if !w.require(b != nil, "XMLTABLE", "nil descriptor") {
		return false
	}
	if !w.require(len(b.columns) > 0, "XMLTABLE", "requires columns") {
		return false
	}
	for i, column := range b.columns {
		if column.name == "" {
			w.fail(ErrInvalid, "XMLTABLE", "column name is required")
			return false
		}
		for j := range i {
			if b.columns[j].name == column.name {
				w.fail(ErrInvalid, "XMLTABLE", "duplicate column name")
				return false
			}
		}
		if !column.kind.valid() {
			w.fail(ErrInvalid, "XMLTABLE", "unknown column kind")
			return false
		}
		if !column.nullability.valid() {
			w.fail(ErrInvalid, "XMLTABLE", "unknown column nullability")
			return false
		}
		if column.kind == xmlTableOrdinality {
			if !w.require(!column.hasPath && !column.hasDefault && column.nullability == xmlNullabilityUnspecified, "XMLTABLE", "ordinality cannot have path, default or nullability options") {
				return false
			}
		}
	}
	ordinality := 0
	for _, column := range b.columns {
		if column.kind == xmlTableOrdinality {
			ordinality++
		}
	}
	if !w.require(ordinality <= 1, "XMLTABLE", "at most one ordinality column is allowed") {
		return false
	}
	switch b.passing {
	case XMLPassingDefault, XMLPassingByRef, XMLPassingByValue:
	default:
		w.fail(ErrInvalid, "XMLTABLE", "unknown PASSING mode")
		return false
	}
	return true
}

func (w *renderer) xmlTableNamespaces(namespaces []XMLNamespaceSpec) {
	if len(namespaces) > 0 {
		w.text("XMLNAMESPACES(")
		for i, namespace := range namespaces {
			if i > 0 {
				w.text(", ")
			}
			if namespace.defaultNS {
				w.text("DEFAULT ")
				w.expr(namespace.uri)
			} else {
				w.expr(namespace.uri)
				w.text(" AS ")
				w.identifierPart(namespace.name)
			}
		}
		w.text("), ")
	}
}

func (w *renderer) xmlTableColumns(columns []XMLTableColumn) {
	for i, column := range columns {
		if i > 0 {
			w.text(", ")
		}
		w.identifierPart(column.name)
		if column.kind == xmlTableOrdinality {
			w.text(" FOR ORDINALITY")
			continue
		}
		w.byte(' ')
		w.dataType(column.typ)
		if column.hasPath {
			w.text(" PATH ")
			w.expr(column.path)
		}
		if column.hasDefault {
			w.text(" DEFAULT ")
			w.expr(column.defaultExpr)
		}
		switch column.nullability {
		case xmlNullabilityUnspecified:
		case xmlNullable:
			w.text(" NULL")
		case xmlNotNull:
			w.text(" NOT NULL")
		default:
			w.fail(ErrInvalid, "XMLTABLE", "unknown column nullability")
		}
	}
}
