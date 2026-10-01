//go:build postgres

package integration_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/jacoelho/qx"
)

func TestXMLConstructionEscapingAndNesting(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	nested := qx.XMLElement("inner", qx.Param(`<&`).Cast(qx.Text)).Expr()
	outer := qx.XMLElement("outer", nested, qx.Param(`<&`).Cast(qx.Text)).Attributes(
		qx.XMLAttr("a", qx.Param(`<>&"`).Cast(qx.Text)),
	).Expr()
	query := qx.Select(outer)
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
	document := qx.XMLParse(qx.XMLDocument, qx.Param(`<root/>`).Cast(qx.Text)).Expr()
	fragment := qx.XMLParse(qx.XMLContent, qx.Param(`<one/><two/>`).Cast(qx.Text)).Expr()
	query := qx.Select(
		qx.XMLSerialize(qx.XMLDocument, document, qx.Text).Expr(),
		qx.XMLSerialize(qx.XMLContent, fragment, qx.Text).Expr(),
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
	document := qx.XMLParse(qx.XMLDocument, qx.Param(`<root/>`).Cast(qx.Text)).Expr()
	content := qx.XMLParse(qx.XMLContent, qx.Param(`<one/><two/>`).Cast(qx.Text)).Expr()
	query := qx.Select(qx.XMLIsDocument(document).Expr(), qx.XMLIsNotDocument(content).Expr())
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
	document := qx.Param(`<root><item/></root>`).Cast(qx.XML)
	query := qx.Select(
		qx.XMLExists(qx.LiteralString(`/root/item`), document).Expr(),
		qx.XMLExists(qx.LiteralString(`/root/missing`), document).ByRef().Expr(),
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
	document := qx.Param(`<root><item><name>Ada</name></item><item/></root>`).Cast(qx.XML)
	table := qx.XMLTable(
		qx.LiteralString(`/root/item`),
		document,
		qx.XMLColumn("name", qx.Text).Path(qx.LiteralString(`name`)).Default(qx.Param("missing").Cast(qx.Text)),
	).As("items")
	got := queryStrings(t, ctx, conn, qx.Select(qx.Col("items.name")).FromExpr(table))
	sort.Strings(got)
	want := []string{"Ada", "missing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestXMLTableNamespaceSelection(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qx.Param(`<root xmlns="urn:book"><item>Ada</item></root>`).Cast(qx.XML)
	table := qx.XMLTable(
		qx.LiteralString(`/b:root/b:item`),
		document,
		qx.XMLColumn("name", qx.Text).Path(qx.LiteralString(`string(.)`)),
	).Namespaces(qx.XMLNamespace(qx.Param("urn:book").Cast(qx.Text), "b")).As("items")
	got := queryStrings(t, ctx, conn, qx.Select(qx.Col("items.name")).FromExpr(table))
	if !reflect.DeepEqual(got, []string{"Ada"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestXMLTableOrdinality(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	document := qx.Param(`<root><item>Ada</item><item>Grace</item></root>`).Cast(qx.XML)
	table := qx.XMLTable(
		qx.LiteralString(`/root/item`),
		document,
		qx.XMLOrdinality("position"),
		qx.XMLColumn("name", qx.Text).Path(qx.LiteralString(`string(.)`)),
	).As("items")
	positionAndName := qx.Col("items.position").Cast(qx.Text).Concat(qx.LiteralString(":")).Concat(qx.Col("items.name"))
	query := qx.Select(positionAndName).FromExpr(table).OrderBy(qx.Asc("items.position"))
	got := queryStrings(t, ctx, conn, query)
	want := []string{"1:Ada", "2:Grace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}
