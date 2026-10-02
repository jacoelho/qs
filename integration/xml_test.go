//go:build postgres

package integration_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/jacoelho/qs"
)

func TestXMLConstructionEscapingAndNesting(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	nested := qs.XMLElement("inner", qs.Param(`<&`).Cast(qs.TypeText)).Expr()
	outer := qs.XMLElement("outer", nested, qs.Param(`<&`).Cast(qs.TypeText)).Attributes(
		qs.XMLAttr("a", qs.Param(`<>&"`).Cast(qs.TypeText)),
	).Expr()
	query := qs.Select(outer)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	want := `<outer a="&lt;&gt;&amp;&quot;"><inner>&lt;&amp;</inner>&lt;&amp;</outer>`
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestXMLDocumentAndContentSerialization(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.XMLParse(qs.XMLDocument, qs.Param(`<root/>`).Cast(qs.TypeText)).Expr()
	fragment := qs.XMLParse(qs.XMLContent, qs.Param(`<one/><two/>`).Cast(qs.TypeText)).Expr()
	query := qs.Select(
		qs.XMLSerialize(qs.XMLDocument, document, qs.TypeText).Expr(),
		qs.XMLSerialize(qs.XMLContent, fragment, qs.TypeText).Expr(),
	)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var gotDocument, gotContent string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&gotDocument, &gotContent); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if gotDocument != `<root/>` || gotContent != `<one/><two/>` {
		t.Fatalf("document=%q content=%q", gotDocument, gotContent)
	}
}

func TestXMLDocumentPredicate(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.XMLParse(qs.XMLDocument, qs.Param(`<root/>`).Cast(qs.TypeText)).Expr()
	content := qs.XMLParse(qs.XMLContent, qs.Param(`<one/><two/>`).Cast(qs.TypeText)).Expr()
	query := qs.Select(qs.XMLIsDocument(document).Expr(), qs.XMLIsNotDocument(content).Expr())
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var documentResult, contentResult bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&documentResult, &contentResult); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if !documentResult || !contentResult {
		t.Fatalf("document=%t content=%t", documentResult, contentResult)
	}
}

func TestXMLExists(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.Param(`<root><item/></root>`).Cast(qs.TypeXML)
	query := qs.Select(
		qs.XMLExists(qs.LiteralString(`/root/item`), document).Expr(),
		qs.XMLExists(qs.LiteralString(`/root/missing`), document).ByRef().Expr(),
	)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var present, absent bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&present, &absent); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if !present || absent {
		t.Fatalf("present=%t absent=%t", present, absent)
	}
}

func TestXMLTableMissingPathDefault(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.Param(`<root><item><name>Ada</name></item><item/></root>`).Cast(qs.TypeXML)
	table := qs.XMLTable(
		qs.LiteralString(`/root/item`),
		document,
		qs.XMLColumn("name", qs.TypeText).Path(qs.LiteralString(`name`)).Default(qs.Param("missing").Cast(qs.TypeText)),
	).As("items")
	got := queryStrings(t, ctx, conn, qs.Select(qs.Col("items.name")).FromExpr(table))
	sort.Strings(got)
	want := []string{"Ada", "missing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestXMLTableNamespaceSelection(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.Param(`<root xmlns="urn:book"><item>Ada</item></root>`).Cast(qs.TypeXML)
	table := qs.XMLTable(
		qs.LiteralString(`/b:root/b:item`),
		document,
		qs.XMLColumn("name", qs.TypeText).Path(qs.LiteralString(`string(.)`)),
	).Namespaces(qs.XMLNamespace(qs.Param("urn:book").Cast(qs.TypeText), "b")).As("items")
	got := queryStrings(t, ctx, conn, qs.Select(qs.Col("items.name")).FromExpr(table))
	if !reflect.DeepEqual(got, []string{"Ada"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestXMLTableOrdinality(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qs.Param(`<root><item>Ada</item><item>Grace</item></root>`).Cast(qs.TypeXML)
	table := qs.XMLTable(
		qs.LiteralString(`/root/item`),
		document,
		qs.XMLOrdinality("position"),
		qs.XMLColumn("name", qs.TypeText).Path(qs.LiteralString(`string(.)`)),
	).As("items")
	positionAndName := qs.Col("items.position").Cast(qs.TypeText).Concat(qs.LiteralString(":")).Concat(qs.Col("items.name"))
	query := qs.Select(positionAndName).FromExpr(table).OrderBy(qs.Asc("items.position"))
	got := queryStrings(t, ctx, conn, query)
	want := []string{"1:Ada", "2:Grace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
