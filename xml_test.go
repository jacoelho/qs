package qx

import (
	"errors"
	"testing"
)

func TestXMLExpressions(t *testing.T) {
	t.Parallel()

	element := XMLElement("book", Param("title")).Attributes(XMLAttr("id", Param(7)))
	runCases(t, []renderCase{
		{
			"concat",
			Select(XMLConcat(Param("left"), XMLElement("right").Expr())),
			`SELECT XMLCONCAT($1, XMLELEMENT(NAME "right"))`,
			[]any{"left"},
		},
		{
			"element_attributes_and_content",
			Select(element.Expr()),
			`SELECT XMLELEMENT(NAME "book", XMLATTRIBUTES($1 AS "id"), $2)`,
			[]any{7, "title"},
		},
		{
			"forest",
			Select(XMLForest(XMLAttr("name", Param("Ada")), XMLValue(Col("title"))).Expr()),
			`SELECT XMLFOREST($1 AS "name", "title")`,
			[]any{"Ada"},
		},
		{
			"parse_document_preserve",
			Select(XMLParse(XMLDocument, Param(`<book/>`)).PreserveWhitespace().Expr()),
			`SELECT XMLPARSE(DOCUMENT $1 PRESERVE WHITESPACE)`,
			[]any{`<book/>`},
		},
		{
			"parse_content_strip",
			Select(XMLParse(XMLContent, Param(`<book/>`)).StripWhitespace().Expr()),
			`SELECT XMLPARSE(CONTENT $1 STRIP WHITESPACE)`,
			[]any{`<book/>`},
		},
		{
			"processing_instruction",
			Select(XMLPI(`pi"name`, Param("payload")).Expr()),
			`SELECT XMLPI(NAME "pi""name", $1)`,
			[]any{"payload"},
		},
		{
			"root_version_and_standalone",
			Select(XMLRoot(Param(`<book/>`)).Version(Param("1.0")).Standalone(XMLStandaloneNoValue).Expr()),
			`SELECT XMLROOT($1, VERSION $2, STANDALONE NO VALUE)`,
			[]any{`<book/>`, "1.0"},
		},
		{
			"root_version_no_value",
			Select(XMLRoot(Param(`<book/>`)).VersionNoValue().Standalone(XMLStandaloneYes).Expr()),
			`SELECT XMLROOT($1, VERSION NO VALUE, STANDALONE YES)`,
			[]any{`<book/>`},
		},
		{
			"serialize_document_indent",
			Select(XMLSerialize(XMLDocument, Param(`<book/>`), Text).Indent().Expr()),
			`SELECT XMLSERIALIZE(DOCUMENT $1 AS text INDENT)`,
			[]any{`<book/>`},
		},
		{
			"serialize_content_no_indent",
			Select(XMLSerialize(XMLContent, Param(`<book/>`), Varchar(32)).NoIndent().Expr()),
			`SELECT XMLSERIALIZE(CONTENT $1 AS varchar(32) NO INDENT)`,
			[]any{`<book/>`},
		},
		{
			"exists_by_value",
			Select(XMLExists(LiteralString(`/book`), Param(`<book/>`)).ByValue().Expr()),
			`SELECT XMLEXISTS((E'/book') PASSING BY VALUE ($1))`,
			[]any{`<book/>`},
		},
		{
			"exists_cast_and_arithmetic_c_expr",
			Select(XMLExists(Param(`/root`).Cast(Text).Concat(LiteralString(`/item`)), Param(`<root><item/></root>`).Cast(XML)).Expr()),
			`SELECT XMLEXISTS(((($1)::text || E'/item')) PASSING (($2)::xml))`,
			[]any{`/root`, `<root><item/></root>`},
		},
		{
			"is_document",
			Select(XMLIsDocument(Param(`<book/>`)).Expr(), XMLIsNotDocument(Param(`<book/><other/>`)).Expr()),
			`SELECT $1 IS DOCUMENT, $2 IS NOT DOCUMENT`,
			[]any{`<book/>`, `<book/><other/>`},
		},
	})
}

func TestXMLTableRendering(t *testing.T) {
	t.Parallel()

	table := XMLTable(
		LiteralString(`/book`),
		Param(`<book><title>Ada</title></book>`),
		XMLOrdinality(`position`),
		XMLColumn(`title`, Text).
			Path(LiteralString(`string(title)`)).
			Default(Param(`missing`)).
			NotNull(),
	).Namespaces(
		XMLNamespace(Param(`urn:book`), `b`),
		XMLDefaultNamespace(Param(`urn:default`)),
	).ByValue().As(`books`)
	checkSQL(t, Select(Col(`books.title`)).FromExpr(table),
		`SELECT "books"."title" FROM XMLTABLE(XMLNAMESPACES($1 AS "b", DEFAULT $2), (E'/book') PASSING BY VALUE ($3) COLUMNS "position" FOR ORDINALITY, "title" text PATH E'string(title)' DEFAULT $4 NOT NULL) AS "books"`,
		`urn:book`, `urn:default`, `<book><title>Ada</title></book>`, `missing`)

	noOptions := XMLTable(LiteralString(`/book`), Param(`<book/>`), XMLColumn(`title`, Text)).Ref()
	checkSQL(t, Select(Star()).FromExpr(noOptions),
		`SELECT * FROM XMLTABLE((E'/book') PASSING ($1) COLUMNS "title" text)`, `<book/>`)

	castTable := XMLTable(
		Param(`/root`).Cast(Text),
		Param(`<root/>`).Cast(XML),
		XMLColumn(`title`, Text),
	).Ref()
	checkSQL(t, Select(Star()).FromExpr(castTable),
		`SELECT * FROM XMLTABLE((($1)::text) PASSING (($2)::xml) COLUMNS "title" text)`,
		`/root`, `<root/>`)

	namespaceCastTable := XMLTable(
		LiteralString(`/p:root`),
		Param(`<root/>`).Cast(XML),
		XMLColumn(`title`, Text),
	).Namespaces(XMLNamespace(Param(`urn:book`).Cast(Text), `p`)).Ref()
	checkSQL(t, Select(Star()).FromExpr(namespaceCastTable),
		`SELECT * FROM XMLTABLE(XMLNAMESPACES(($1)::text AS "p"), (E'/p:root') PASSING (($2)::xml) COLUMNS "title" text)`,
		`urn:book`, `<root/>`)
}

func TestXMLValidationAndAtomicErrors(t *testing.T) {
	t.Parallel()

	cases := []Statement{
		Select(XMLConcat()),
		Select(XMLForest().Expr()),
		Select(XMLParse(XMLMode(0), Param(`<x/>`)).Expr()),
		Select(XMLSerialize(XMLMode(0), Param(`<x/>`), Text).Expr()),
		Select(XMLPI(`pi`, Param(`a`), Param(`b`)).Expr()),
		Select(XMLRoot(Param(`<x/>`)).Expr()),
		Select(XMLRoot(Param(`<x/>`)).Standalone(XMLStandalone(99)).VersionNoValue().Expr()),
		Select(XMLExists(LiteralString(`/x`), Param(`<x/>`)).Passing(XMLPassingMode(99)).Expr()),
		Select(Star()).FromExpr(XMLTable(LiteralString(`/x`), Param(`<x/>`)).Ref()),
		Select(Star()).FromExpr(XMLTable(LiteralString(`/x`), Param(`<x/>`), XMLColumn(`x`, Text), XMLColumn(`x`, Text)).Ref()),
		Select(Star()).FromExpr(XMLTable(LiteralString(`/x`), Param(`<x/>`), XMLOrdinality(`a`), XMLOrdinality(`b`)).Ref()),
		Select(Star()).FromExpr(XMLTable(LiteralString(`/x`), Param(`<x/>`), XMLOrdinality(`a`).Path(LiteralString(`.`))).Ref()),
		Select(Star()).FromExpr(XMLTable(LiteralString(`/x`), Param(`<x/>`), XMLColumn(`x`, Text)).Passing(XMLPassingMode(99)).Ref()),
	}
	for i, statement := range cases {
		t.Run(testName(i), func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
	if _, _, err := ToSQLWith(Select(XMLSerialize(XMLDocument, Param(`<x/>`), Text).Indent().Expr()), Options{PostgreSQL: PostgreSQL15}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("XMLSERIALIZE INDENT on PostgreSQL 15: %v", err)
	}
}

func TestXMLOwnershipAndClone(t *testing.T) {
	t.Parallel()

	values := []Expr{Param(`one`)}
	concat := XMLConcat(values...)
	values[0] = Param(`changed`)
	checkSQL(t, Select(concat), `SELECT XMLCONCAT($1)`, `one`)

	items := []XMLItem{XMLAttr(`name`, Param(`Ada`))}
	forest := XMLForest(items...)
	items[0] = XMLAttr(`name`, Param(`Grace`))
	checkSQL(t, Select(forest.Expr()), `SELECT XMLFOREST($1 AS "name")`, `Ada`)

	columns := []XMLTableColumn{XMLColumn(`title`, Text)}
	table := XMLTable(LiteralString(`/book`), Param(`<book/>`), columns...)
	columns[0] = XMLColumn(`other`, Text)
	checkSQL(t, Select(Star()).FromExpr(table.Ref()), `SELECT * FROM XMLTABLE((E'/book') PASSING ($1) COLUMNS "title" text)`, `<book/>`)

	child := Select(Param(`<book/>`))
	parent := Select(XMLElement(`book`, Scalar(child)).Expr())
	clone := parent.Clone()
	child.Columns(Param(`<other/>`))
	checkSQL(t, clone, `SELECT XMLELEMENT(NAME "book", (SELECT $1))`, `<book/>`)
	checkError(t, parent, ErrInvalid)

	childTable := Select(Param(`<book/>`))
	parentTable := Select(Star()).FromExpr(XMLTable(LiteralString(`/book`), Scalar(childTable), XMLColumn(`title`, Text)).As(`books`))
	cloneTable := parentTable.Clone()
	childTable.Columns(Param(`<other/>`))
	checkSQL(t, cloneTable, `SELECT * FROM XMLTABLE((E'/book') PASSING ((SELECT $1)) COLUMNS "title" text) AS "books"`, `<book/>`)
	checkError(t, parentTable, ErrInvalid)
}

func TestXMLReusableAppendSQL(t *testing.T) {
	query := Select(
		XMLElement(`book`, XMLParse(XMLContent, Param(`<title>Ada</title>`)).Expr()).
			Attributes(XMLAttr(`id`, Param(7))).
			Expr(),
		XMLSerialize(XMLContent, Param(`<book/>`), Text).NoIndent().Expr(),
	).FromExpr(XMLTable(LiteralString(`/book`), Param(`<book/>`), XMLColumn(`title`, Text)).As(`books`))
	buf := make([]byte, 0, 512)
	args := make([]any, 0, 8)
	var err error
	allocations := testing.AllocsPerRun(100, func() {
		buf, args, err = query.AppendSQL(buf[:0], args[:0])
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocations != 0 {
		t.Fatalf("XML warm renderer allocations: %g", allocations)
	}
}

func testName(i int) string {
	return "case_" + string(rune('a'+i))
}
