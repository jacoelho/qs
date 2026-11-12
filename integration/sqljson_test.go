//go:build postgres

package integration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jacoelho/qs"
)

// SQL/JSON constructors and IS JSON arrived in PostgreSQL 16; JSON(),
// JSON_SCALAR, JSON_SERIALIZE, the query functions and JSON_TABLE in 17.
const (
	sqlJSONConstructors = 16
	sqlJSONQueries      = 17
)

func text(value string) qs.Expr { return qs.Param(value).Cast(qs.TypeText) }

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestSQLJSONConstructors(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONConstructors)

	null := qs.NullExpr(qs.TypeInt4)
	one := func(expression qs.Expr) qs.Statement { return qs.Select(expression.Cast(qs.TypeText)) }
	for _, tc := range []struct {
		name      string
		statement qs.Statement
		want      string
	}{
		{"object_default_keeps_null", one(qs.JSONObject(qs.JSONPair(text("key"), null)).Expr()), `{"key" : null}`},
		{"object_absent", one(qs.JSONObject(qs.JSONPair(text("key"), null)).AbsentOnNull().Expr()), `{}`},
		{"array_default_drops_null", one(qs.JSONArray(null).Expr()), `[]`},
		{"array_null", one(qs.JSONArray(null).NullOnNull().Expr()), `[null]`},
		{"array_aggregate_default_drops_null", one(qs.JSONArrayAggregate(null).Expr()), `[]`},
		{"array_aggregate_null", one(qs.JSONArrayAggregate(null).NullOnNull().Expr()), `[null]`},
		{"object_aggregate_default_keeps_null", one(qs.JSONObjectAggregate(text("key"), null).Expr()), `{ "key" : null }`},
		{"object_aggregate_absent", one(qs.JSONObjectAggregate(text("key"), null).AbsentOnNull().Expr()), `{  }`},
		{"empty_object", one(qs.JSONObject().Returning(qs.TypeJSONB).Expr()), `{}`},
		{"empty_array", one(qs.JSONArray().Expr()), `[]`},
		{
			// FORMAT JSON embeds the text as JSON; without it the text is a string.
			"format_json_operand",
			one(qs.JSONArray(text(`{"a":1}`), qs.FormatJSON(text(`{"a":1}`))).Returning(qs.TypeJSONB).Expr()),
			`["{\"a\":1}", {"a": 1}]`,
		},
		{
			"array_query",
			one(
				qs.JSONArrayQuery(
					qs.Select(qs.Col("v")).
						FromExpr(
							qs.ValuesExpr(qs.LiteralInt(2)).RowExpr(qs.LiteralInt(1)).As("t", "v"),
						).
						OrderBy(qs.Col("v").Asc()),
				).Returning(
					qs.TypeJSONB,
				).Expr(),
			),
			`[1, 2]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := queryStrings(t, session, tc.statement); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSQLJSONUniqueKeys(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONConstructors)

	pair := func(key string, value int) qs.JSONMember {
		return qs.JSONPair(text(key), qs.Param(value).Cast(qs.TypeInt4))
	}
	requireDuplicateKey := func(t *testing.T, statement qs.Statement) {
		t.Helper()
		sql, args, err := session.render(t, statement)
		if err != nil {
			t.Fatal(err)
		}
		var discard string
		err = session.QueryRow(session, sql, args...).Scan(&discard)
		var failure *pgconn.PgError
		// 22030 is duplicate_json_object_key_value.
		if !errors.As(err, &failure) || failure.Code != "22030" {
			t.Fatalf("%s: got %v, want SQLSTATE 22030", sql, err)
		}
	}

	// The json type keeps duplicate members unless uniqueness is requested.
	duplicate := qs.JSONObject(pair("a", 1), pair("a", 2))
	if got := queryStrings(t, session, qs.Select(duplicate.Expr().Cast(qs.TypeText))); !reflect.DeepEqual(got, []string{`{"a" : 1, "a" : 2}`}) {
		t.Fatalf("duplicate members: %q", got)
	}
	requireDuplicateKey(t, qs.Select(duplicate.WithUniqueKeys().Expr().Cast(qs.TypeText)))
	distinct := qs.JSONObject(pair("a", 1), pair("b", 2)).WithUniqueKeys()
	if got := queryStrings(t, session, qs.Select(distinct.Expr().Cast(qs.TypeText))); !reflect.DeepEqual(got, []string{`{"a" : 1, "b" : 2}`}) {
		t.Fatalf("distinct members: %q", got)
	}

	// IS JSON reports the same condition as a truth value instead of failing.
	repeated := text(`{"a":1,"a":2}`)
	predicates := qs.Select(qs.Array(
		qs.IsJSON(repeated).Expr(),
		qs.IsJSON(repeated).WithUniqueKeys().Expr(),
		qs.IsJSON(repeated).Object().Expr(),
		qs.IsJSON(repeated).Array().Expr(),
		qs.IsJSON(repeated).Scalar().Expr(),
		qs.IsJSON(text(`{`)).Condition().Not().Expr(),
	).Cast(qs.TypeText))
	if got := queryStrings(t, session, predicates); !reflect.DeepEqual(got, []string{`{t,f,t,f,f,t}`}) {
		t.Fatalf("IS JSON: %q", got)
	}

	if session.major >= sqlJSONQueries {
		requireDuplicateKey(
			t,
			qs.Select(
				qs.JSONParse(qs.FormatJSON(repeated)).WithUniqueKeys().Expr().Cast(qs.TypeText),
			),
		)
	}
}

func TestSQLJSONAggregates(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONConstructors)
	if _, err := session.Exec(session, `CREATE TEMP TABLE qs_sqljson_values(g text, v int, keep boolean);
		INSERT INTO qs_sqljson_values VALUES ('a', 3, true), ('a', 1, false), ('b', 5, true), ('b', 2, false)`); err != nil {
		t.Fatal(err)
	}

	array := qs.JSONArrayAggregate(qs.Col("v")).OrderBy(qs.Col("v").Desc()).Filter(
		qs.Col("keep").Eq(qs.Param(true)),
	).Expr().Cast(
		qs.TypeText,
	)
	if got := queryStrings(t, session, qs.Select(array).From("qs_sqljson_values")); !reflect.DeepEqual(got, []string{`[5, 3]`}) {
		t.Fatalf("ordered filtered array aggregate: %q", got)
	}

	object := qs.JSONObjectAggregate(qs.Col("v"), qs.Col("g")).Filter(
		qs.Col("keep").Eq(qs.Param(true)),
	).Returning(
		qs.TypeJSONB,
	).Expr().Cast(
		qs.TypeText,
	)
	got := queryStrings(t, session, qs.Select(object).From("qs_sqljson_values"))
	if len(got) != 1 {
		t.Fatalf("object aggregate rows: %q", got)
	}
	var members map[string]string
	if err := json.Unmarshal([]byte(got[0]), &members); err != nil {
		t.Fatalf("object aggregate %q: %v", got[0], err)
	}
	if want := map[string]string{"3": "a", "5": "b"}; !reflect.DeepEqual(members, want) {
		t.Fatalf("filtered object aggregate: got %v, want %v", members, want)
	}

	// As a window function the aggregate sees its whole partition on every row.
	windowed := qs.Select(
		qs.JSONArrayAggregate(qs.Col("v")).Returning(qs.TypeJSONB).
			Over(
				qs.Window().PartitionBy(qs.Col("g")).OrderBy(qs.Col("v").Asc()).RowsBetween(
					qs.UnboundedPreceding(),
					qs.UnboundedFollowing(),
				),
			).Expr().Cast(
			qs.TypeText,
		),
	).
		From("qs_sqljson_values").
		OrderBy(qs.Col("g").Asc(), qs.Col("v").Asc())
	if got := queryStrings(t, session, windowed); !reflect.DeepEqual(got, []string{`[1, 3]`, `[1, 3]`, `[2, 5]`, `[2, 5]`}) {
		t.Fatalf("window aggregate: %q", got)
	}
}

func TestSQLJSONParseScalarAndSerialize(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONQueries)

	// JSON() parses text as a document; JSON_SCALAR makes the same text one
	// JSON string; JSON_SERIALIZE returns the document's text unchanged.
	query := qs.Select(qs.Array(
		qs.JSONParse(text(` [2,1] `)).Expr().Cast(qs.TypeJSONB).Cast(qs.TypeText),
		qs.JSONScalar(text(` [2,1] `)).Cast(qs.TypeText),
		qs.JSONSerialize(qs.FormatJSON(text(` { "a" : 1 } `))).Returning(qs.TypeText).Expr(),
	).Cast(qs.TypeText))
	want := `{"[2, 1]","\" [2,1] \""," { \"a\" : 1 } "}`
	if got := queryStrings(t, session, query); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestSQLJSONQueryFunctions(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONQueries)

	doc := qs.Param(`{"name": "Ada", "tags": ["x", "y"], "n": 7}`).Cast(qs.TypeJSONB)
	path := qs.LiteralString
	for _, tc := range []struct {
		name       string
		expression qs.Expr
		want       string
	}{
		{"exists", qs.JSONExists(doc, path("$.tags[*] ? (@ == $tag)")).Passing("tag", text("y")).Expr(), "true"},
		{
			"exists_no_match",
			qs.JSONExists(doc, path("$.tags[*] ? (@ == $tag)")).Passing("tag", text("z")).Expr(),
			"false",
		},
		{"exists_error_as_true", qs.JSONExists(doc, path("strict $.missing")).TrueOnError().Expr(), "true"},
		{"value", qs.JSONValue(doc, path("$.n")).Returning(qs.TypeInt4).Expr(), "7"},
		{
			"value_default_on_empty",
			qs.JSONValue(doc, path("$.missing")).Returning(qs.TypeInt4).DefaultOnEmpty(
				qs.LiteralInt(-1),
			).Expr(),
			"-1",
		},
		{
			"value_default_on_error",
			qs.JSONValue(doc, path("$.name")).Returning(qs.TypeInt4).DefaultOnError(
				qs.LiteralInt(-2),
			).Expr(),
			"-2",
		},
		{
			"value_passing",
			qs.JSONValue(doc, path("$.n + $more")).Passing("more", qs.Param(3).Cast(qs.TypeInt4)).Returning(
				qs.TypeInt4,
			).Expr(),
			"10",
		},
		{"query", qs.JSONQuery(doc, path("$.tags")).Expr(), `["x", "y"]`},
		{"query_wrapper", qs.JSONQuery(doc, path("$.tags[*]")).WithWrapper().Expr(), `["x", "y"]`},
		{"query_conditional_wrapper", qs.JSONQuery(doc, path("$.tags")).WithConditionalWrapper().Expr(), `["x", "y"]`},
		{"query_keep_quotes", qs.JSONQuery(doc, path("$.name")).Returning(qs.TypeText).KeepQuotes().Expr(), `"Ada"`},
		{"query_omit_quotes", qs.JSONQuery(doc, path("$.name")).Returning(qs.TypeText).OmitQuotes().Expr(), `Ada`},
		{"query_empty_array", qs.JSONQuery(doc, path("$.missing")).EmptyArrayOnEmpty().Expr(), `[]`},
		{"query_empty_object_on_error", qs.JSONQuery(doc, path("strict $.missing")).EmptyObjectOnError().Expr(), `{}`},
		{
			"query_default",
			qs.JSONQuery(doc, path("$.missing")).DefaultOnEmpty(
				qs.LiteralString(`{"d": 1}`).Cast(qs.TypeJSONB),
			).Expr(),
			`{"d": 1}`,
		},
		// A bound value reaches DEFAULT through an operator expression.
		{
			"value_default_from_parameter",
			qs.JSONValue(doc, path("$.missing")).Returning(qs.TypeInt4).DefaultOnEmpty(
				qs.Param(40).Cast(qs.TypeInt4).Add(qs.LiteralInt(2)),
			).Expr(),
			"42",
		},
		{
			"format_json_document",
			qs.JSONValue(qs.FormatJSON(text(`{"a": 5}`)), path("$.a")).Returning(qs.TypeInt4).Expr(),
			"5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statement := qs.Select(tc.expression.Cast(qs.TypeText))
			if got := queryStrings(t, session, statement); !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("error_on_error", func(t *testing.T) {
		sql, args, err := session.render(
			t,
			qs.Select(qs.JSONValue(doc, path("strict $.missing")).ErrorOnError().Expr()),
		)
		if err != nil {
			t.Fatal(err)
		}
		var discard *string
		err = session.QueryRow(session, sql, args...).Scan(&discard)
		var failure *pgconn.PgError
		// 2203A is sql_json_member_not_found.
		if !errors.As(err, &failure) || failure.Code != "2203A" {
			t.Fatalf("%s: got %v, want SQLSTATE 2203A", sql, err)
		}
	})
}

func TestSQLJSONTable(t *testing.T) {
	t.Parallel()
	session := connect(t)
	requireVersion(t, session, sqlJSONQueries)
	if _, err := session.Exec(session, `CREATE TEMP TABLE qs_sqljson_orders(id int, doc jsonb);
		INSERT INTO qs_sqljson_orders VALUES
			(1, '{"items": [{"sku": "a", "qty": 2, "parts": [{"name": "p1"}, {"name": "p2"}]}, {"sku": "b", "gift": true}]}'),
			(2, '{"items": [{"sku": "c", "qty": 9}]}')`); err != nil {
		t.Fatal(err)
	}

	items := qs.JSONTable(qs.Col("o", "doc"), "$.items[*] ? (@.sku != $skip)",
		qs.JSONOrdinality("n"),
		qs.JSONColumn("sku", qs.TypeText),
		qs.JSONColumn("qty", qs.TypeInt4).DefaultOnEmpty(qs.LiteralInt(0)),
		qs.JSONExistsColumn("gift", qs.TypeBool),
		qs.JSONColumn("parts", qs.TypeText).FormatJSON().Path("$.parts[*].name").WithWrapper().EmptyArrayOnEmpty(),
		qs.JSONNested("$.parts[*]", qs.JSONColumn("part", qs.TypeText).Path("$.name")),
	).Passing("skip", text("c")).ErrorOnError()

	row := qs.Row(
		qs.Col("o", "id"),
		qs.Col("jt", "n"),
		qs.Col("jt", "sku"),
		qs.Col("jt", "qty"),
		qs.Col("jt", "gift"),
		qs.Col("jt", "parts"),
		qs.Col("jt", "part"),
	)
	query := qs.Select(row.Expr().Cast(qs.TypeText)).
		FromExpr(qs.Table("qs_sqljson_orders").As("o")).
		CrossJoinExpr(qs.Lateral(items.As("jt"))).
		OrderBy(qs.Col("o", "id").Asc(), qs.Col("jt", "n").Asc(), qs.Col("jt", "part").Asc())
	want := []string{
		`(1,1,a,2,f,"[""p1"", ""p2""]",p1)`,
		`(1,1,a,2,f,"[""p1"", ""p2""]",p2)`,
		`(1,2,b,0,t,[],)`,
	}
	if got := queryStrings(t, session, query); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	// Column aliases rename the table's columns positionally.
	renamed := qs.Select(qs.Col("x", "first").Cast(qs.TypeText)).
		FromExpr(
			qs.JSONTable(
				qs.Param(`[1, 2]`).Cast(qs.TypeJSONB),
				"$[*]",
				qs.JSONColumn("v", qs.TypeInt4).Path("$"),
			).As(
				"x",
				"first",
			),
		).
		OrderBy(qs.Col("x", "first").Desc())
	if got := queryStrings(t, session, renamed); !reflect.DeepEqual(got, []string{"2", "1"}) {
		t.Fatalf("column aliases: %q", got)
	}

	// PostgreSQL names an unnamed row path json_table_path_0 and rejects that
	// name elsewhere, so qs refuses it too. These uses of generated-looking
	// names are the ones the server accepts, and qs must keep rendering them.
	document := qs.Param(`[{"a": 1}]`).Cast(qs.TypeJSONB)
	for name, table := range map[string]qs.JSONTableBuilder{
		"named_row_path": qs.JSONTable(document, "$[*]",
			qs.JSONColumn("json_table_path_0", qs.TypeInt4).Path("$.a"),
			qs.JSONNested("$", qs.JSONColumn("a", qs.TypeInt4)),
		).PathName("p"),
		"nested_path_name": qs.JSONTable(document, "$[*]",
			qs.JSONColumn("json_table_path_0", qs.TypeInt4).Path("$.a"),
			qs.JSONNested("$", qs.JSONColumn("a", qs.TypeInt4)).PathName("json_table_path_1"),
		).PathName("p"),
		"later_generated_name": qs.JSONTable(document, "$[*]",
			qs.JSONColumn("v", qs.TypeInt4).Path("$.a"),
			qs.JSONNested("$", qs.JSONColumn("a", qs.TypeInt4)).PathName("json_table_path_1"),
		),
	} {
		query := qs.Select(qs.Row(qs.Star("jt")).Expr().Cast(qs.TypeText)).FromExpr(table.As("jt"))
		if got := queryStrings(t, session, query); !reflect.DeepEqual(got, []string{"(1,1)"}) {
			t.Fatalf("%s: %q", name, got)
		}
	}
}
