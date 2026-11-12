package qs

import (
	"errors"
	"slices"
	"testing"
)

// Expected SQL follows PostgreSQL's grammar for each SQL/JSON form. The frozen
// regression corpus checks the same renderings against PostgreSQL's parser.
func TestSQLJSONForms(t *testing.T) {
	t.Parallel()

	doc := Col("doc")
	formatted := FormatJSON(Col("s"))
	path := LiteralString
	runCases(t, []renderCase{
		{"object_empty", Select(JSONObject().Expr()), `SELECT JSON_OBJECT()`, nil},
		{"object_empty_returning", Select(JSONObject().Returning(TypeJSONB).Expr()), `SELECT JSON_OBJECT(RETURNING jsonb)`, nil},
		{
			"object_clauses",
			Select(
				JSONObject(
					JSONPair(LiteralString("id"), Param(7)),
					JSONPair(Col("k"), FormatJSON(Col("v"))),
				).
					AbsentOnNull().WithUniqueKeys().ReturningFormatJSON(TypeText).As("o"),
			),
			`SELECT JSON_OBJECT(E'id' : $1, "k" : "v" FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING text FORMAT JSON) AS "o"`,
			[]any{7},
		},
		{
			"object_explicit_defaults",
			Select(JSONObject(JSONPair(Col("k"), Col("v"))).NullOnNull().WithoutUniqueKeys().Expr()),
			`SELECT JSON_OBJECT("k" : "v" NULL ON NULL WITHOUT UNIQUE KEYS)`, nil,
		},
		{
			"object_members_append",
			Select(
				JSONObject(JSONPair(Col("a"), Col("b"))).Members(JSONPair(Col("c"), Col("d"))).Expr(),
			),
			`SELECT JSON_OBJECT("a" : "b", "c" : "d")`, nil,
		},
		{"array_empty", Select(JSONArray().Expr()), `SELECT JSON_ARRAY()`, nil},
		{"array_empty_returning", Select(JSONArray().Returning(TypeJSONB).Expr()), `SELECT JSON_ARRAY(RETURNING jsonb)`, nil},
		{
			"array_clauses",
			Select(
				JSONArray(Param(1), JSONBExpr(Col("j")), FormatJSONUTF8(Col("raw"))).Values(Col("z")).
					NullOnNull().ReturningFormatJSONUTF8().Expr(),
			),
			`SELECT JSON_ARRAY($1, "j", "raw" FORMAT JSON ENCODING UTF8, "z" NULL ON NULL RETURNING bytea FORMAT JSON ENCODING UTF8)`,
			[]any{1},
		},
		{"array_absent", Select(JSONArray(Col("a")).AbsentOnNull().Expr()), `SELECT JSON_ARRAY("a" ABSENT ON NULL)`, nil},
		{
			"array_query",
			Select(
				JSONArrayQuery(Select("v").From("t").Where(Col("id").Eq(Param(3)))).FormatJSON().Returning(
					TypeJSONB,
				).Expr(),
			),
			`SELECT JSON_ARRAY(SELECT "v" FROM "t" WHERE ("id" = $1) FORMAT JSON RETURNING jsonb)`,
			[]any{3},
		},
		{
			"object_aggregate",
			Select(
				JSONObjectAggregate(Col("k"), Col("v")).AbsentOnNull().WithUniqueKeys().Returning(
					TypeJSONB,
				).
					Filter(Col("v").IsNotNull()).Over(Window().PartitionBy(Col("g"))).Expr(),
			),
			`SELECT JSON_OBJECTAGG("k" : "v" ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb) FILTER (WHERE ("v" IS NOT NULL)) OVER (PARTITION BY "g")`,
			nil,
		},
		{
			"array_aggregate",
			Select(
				JSONArrayAggregate(FormatJSON(Col("v"))).OrderBy(Col("n").Desc()).NullOnNull().Returning(
					TypeJSONB,
				).Filter(
					Col("v").IsNotNull(),
				).Expr(),
			),
			`SELECT JSON_ARRAYAGG("v" FORMAT JSON ORDER BY "n" DESC NULL ON NULL RETURNING jsonb) FILTER (WHERE ("v" IS NOT NULL))`,
			nil,
		},
		{
			"array_aggregate_window",
			Select(JSONArrayAggregate(Col("v")).AbsentOnNull().OverNamed("w").Expr()),
			`SELECT JSON_ARRAYAGG("v" ABSENT ON NULL) OVER "w"`,
			nil,
		},
		{
			"array_query_bytea",
			Select(JSONArrayQuery(Select("raw").From("t")).FormatJSONUTF8().Expr()),
			`SELECT JSON_ARRAY(SELECT "raw" FROM "t" FORMAT JSON ENCODING UTF8)`, nil,
		},
		{"parse", Select(JSONParse(FormatJSON(Param(`{"a":1}`))).WithUniqueKeys().Expr()), `SELECT JSON($1 FORMAT JSON WITH UNIQUE KEYS)`, []any{`{"a":1}`}},
		{"parse_without_unique", Select(JSONParse(Col("s")).WithoutUniqueKeys().Expr()), `SELECT JSON("s" WITHOUT UNIQUE KEYS)`, nil},
		{"scalar", Select(JSONScalar(Param(1.5))), `SELECT JSON_SCALAR($1)`, []any{1.5}},
		{"serialize", Select(JSONSerialize(Col("j")).Returning(TypeText).Expr()), `SELECT JSON_SERIALIZE("j" RETURNING text)`, nil},
		{"is_json", Select("id").From("t").Where(IsJSON(Col("s")).Condition()), `SELECT "id" FROM "t" WHERE ("s" IS JSON)`, nil},
		{"is_json_item", Select(IsJSON(Col("s")).Object().WithUniqueKeys().Expr()), `SELECT ("s" IS JSON OBJECT WITH UNIQUE KEYS)`, nil},
		{"is_json_array", Select(IsJSON(Col("s")).Array().WithoutUniqueKeys().Expr()), `SELECT ("s" IS JSON ARRAY WITHOUT UNIQUE KEYS)`, nil},
		{"is_json_scalar", Select(IsJSON(Col("s")).Scalar().Expr()), `SELECT ("s" IS JSON SCALAR)`, nil},
		{"is_not_json", Select("id").From("t").Where(IsJSON(Col("s")).Condition().Not()), `SELECT "id" FROM "t" WHERE (NOT ("s" IS JSON))`, nil},
		{
			"exists",
			Select("id").
				From("t").
				Where(
					JSONExists(doc, path("$.a ? (@ > $min)")).Passing("min", Param(2)).FalseOnError().Condition(),
				),
			`SELECT "id" FROM "t" WHERE JSON_EXISTS("doc", E'$.a ? (@ > $min)' PASSING $1 AS "min" FALSE ON ERROR)`,
			[]any{2},
		},
		{"exists_true", Select(JSONExists(doc, path("$")).TrueOnError().Expr()), `SELECT JSON_EXISTS("doc", E'$' TRUE ON ERROR)`, nil},
		{"exists_unknown", Select(JSONExists(doc, path("$")).UnknownOnError().Expr()), `SELECT JSON_EXISTS("doc", E'$' UNKNOWN ON ERROR)`, nil},
		{"exists_error", Select(JSONExists(doc, path("$")).ErrorOnError().Expr()), `SELECT JSON_EXISTS("doc", E'$' ERROR ON ERROR)`, nil},
		{
			"value",
			Select(
				JSONValue(FormatJSON(Col("s")), path("$.n")).Passing("a", Col("x")).Passing(
					"b",
					FormatJSON(Col("y")),
				).
					Returning(TypeInt4).DefaultOnEmpty(LiteralInt(0)).ErrorOnError().Expr(),
			),
			`SELECT JSON_VALUE("s" FORMAT JSON, E'$.n' PASSING "x" AS "a", "y" FORMAT JSON AS "b" RETURNING integer DEFAULT 0 ON EMPTY ERROR ON ERROR)`,
			nil,
		},
		{"value_null", Select(JSONValue(doc, path("$")).NullOnEmpty().NullOnError().Expr()), `SELECT JSON_VALUE("doc", E'$' NULL ON EMPTY NULL ON ERROR)`, nil},
		{
			"value_error_default",
			Select(
				JSONValue(doc, path("$")).ErrorOnEmpty().DefaultOnError(LiteralString("x")).Expr(),
			),
			`SELECT JSON_VALUE("doc", E'$' ERROR ON EMPTY DEFAULT E'x' ON ERROR)`, nil,
		},
		{
			"query",
			Select(JSONQuery(doc, path("$.a")).ReturningFormatJSON(TypeText).WithConditionalWrapper().KeepQuotes().
				EmptyArrayOnEmpty().EmptyObjectOnError().Expr()),
			`SELECT JSON_QUERY("doc", E'$.a' RETURNING text FORMAT JSON WITH CONDITIONAL WRAPPER KEEP QUOTES EMPTY ARRAY ON EMPTY EMPTY OBJECT ON ERROR)`,
			nil,
		},
		{
			"query_defaults",
			Select(
				JSONQuery(doc, path("$")).Returning(TypeJSONB).WithoutWrapper().OmitQuotes().
					DefaultOnEmpty(LiteralString("[]").Cast(TypeJSONB)).DefaultOnError(
					Call("jsonb_build_object"),
				).Expr(),
			),
			`SELECT JSON_QUERY("doc", E'$' RETURNING jsonb WITHOUT WRAPPER OMIT QUOTES DEFAULT (E'[]')::jsonb ON EMPTY DEFAULT "jsonb_build_object"() ON ERROR)`,
			nil,
		},
		{
			"query_behaviors",
			Select(
				JSONQuery(doc, path("$")).WithWrapper().ErrorOnEmpty().NullOnError().Expr(),
				JSONQuery(doc, path("$")).NullOnEmpty().ErrorOnError().Expr(),
				JSONQuery(doc, path("$")).EmptyObjectOnEmpty().EmptyArrayOnError().Expr(),
			),
			`SELECT JSON_QUERY("doc", E'$' WITH WRAPPER ERROR ON EMPTY NULL ON ERROR), JSON_QUERY("doc", E'$' NULL ON EMPTY ERROR ON ERROR), JSON_QUERY("doc", E'$' EMPTY OBJECT ON EMPTY EMPTY ARRAY ON ERROR)`,
			nil,
		},
		{
			// A parameter is accepted inside an operator expression, where
			// PostgreSQL accepts it too.
			"default_operator_over_parameter",
			Select(JSONValue(doc, path("$")).DefaultOnEmpty(Param(5).Add(LiteralInt(0))).Expr()),
			`SELECT JSON_VALUE("doc", E'$' DEFAULT ($1 + 0) ON EMPTY)`, []any{5},
		},
		{
			// Whether PostgreSQL accepts a parameter alone depends on how the
			// driver sends it, so qs renders it.
			"default_parameter",
			Select(JSONValue(doc, path("$")).DefaultOnEmpty(Param(5)).Expr()),
			`SELECT JSON_VALUE("doc", E'$' DEFAULT $1 ON EMPTY)`, []any{5},
		},
		{
			"pointer_operands",
			Select(JSONArray(&doc, &formatted).Expr()),
			`SELECT JSON_ARRAY("doc", "s" FORMAT JSON)`, nil,
		},
		{
			// PostgreSQL accepts a repeated PASSING name; qs does not second-guess it.
			"passing_repeated_name",
			Select(JSONExists(doc, path("$")).Passing("x", Param(1)).Passing("x", Param(2)).Expr()),
			`SELECT JSON_EXISTS("doc", E'$' PASSING $1 AS "x", $2 AS "x")`, []any{1, 2},
		},
	})
}

func TestSQLJSONTable(t *testing.T) {
	t.Parallel()

	table := JSONTable(Col("doc"), "$.items[*] ? (@.qty > $min)",
		JSONOrdinality("n"),
		JSONColumn("sku", TypeText),
		JSONColumn("qty", TypeInt4).Path("$.qty").DefaultOnEmpty(LiteralInt(0)).ErrorOnError(),
		JSONColumn("tags", TypeJSONB).FormatJSON().Path("$.tags").WithConditionalWrapper().KeepQuotes().EmptyArrayOnEmpty().EmptyObjectOnError(),
		JSONColumn("raw", TypeText).OmitQuotes().NullOnEmpty().NullOnError(),
		JSONExistsColumn("gift", TypeBool).Path("$.gift").FalseOnError(),
		JSONNested("$.parts[*]",
			JSONColumn("part", TypeText).Path("$.name").WithoutWrapper(),
			JSONNested("$.bins[*]", JSONOrdinality("bin")),
		).PathName("parts"),
	).PathName("items").Passing("min", Param(1)).ErrorOnError()

	const call = `JSON_TABLE("doc", E'$.items[*] ? (@.qty > $min)' AS "items" PASSING $1 AS "min" COLUMNS (` +
		`"n" FOR ORDINALITY, "sku" text, "qty" integer PATH E'$.qty' DEFAULT 0 ON EMPTY ERROR ON ERROR, ` +
		`"tags" jsonb FORMAT JSON PATH E'$.tags' WITH CONDITIONAL WRAPPER KEEP QUOTES EMPTY ARRAY ON EMPTY EMPTY OBJECT ON ERROR, ` +
		`"raw" text OMIT QUOTES NULL ON EMPTY NULL ON ERROR, "gift" boolean EXISTS PATH E'$.gift' FALSE ON ERROR, ` +
		`NESTED PATH E'$.parts[*]' AS "parts" COLUMNS ("part" text PATH E'$.name' WITHOUT WRAPPER, NESTED PATH E'$.bins[*]' COLUMNS ("bin" FOR ORDINALITY))) ERROR ON ERROR)`

	runCases(t, []renderCase{
		{"unaliased", Select(Star()).From(table.Ref()), `SELECT * FROM ` + call, []any{1}},
		{
			// NESTED PATH groups contribute their columns, not themselves.
			"aliases_for_every_column",
			Select(Star()).From(
				JSONTable(
					Col("doc"),
					"$",
					JSONColumn("a", TypeText),
					JSONNested("$", JSONOrdinality("n"), JSONExistsColumn("e", TypeBool)),
				).As(
					"jt",
					"x",
					"y",
					"z",
				),
			),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" text, NESTED PATH E'$' COLUMNS ("n" FOR ORDINALITY, "e" boolean EXISTS))) AS "jt" ("x", "y", "z")`,
			nil,
		},
		{
			// PostgreSQL takes json_table_path_0 only for a row path without a name.
			"generated_name_with_a_named_row_path",
			Select(Star()).From(
				JSONTable(
					Col("doc"),
					"$",
					JSONColumn("json_table_path_0", TypeText),
					JSONNested("$", JSONColumn("a", TypeText)),
				).PathName(
					"p",
				).Ref(),
			),
			`SELECT * FROM JSON_TABLE("doc", E'$' AS "p" COLUMNS ("json_table_path_0" text, NESTED PATH E'$' COLUMNS ("a" text)))`,
			nil,
		},
		{
			// The names PostgreSQL generates for NESTED PATH groups collide with nothing.
			"generated_name_of_a_nested_path",
			Select(Star()).From(
				JSONTable(
					Col("doc"),
					"$",
					JSONNested("$", JSONColumn("a", TypeText)),
					JSONNested("$", JSONColumn("b", TypeText)).PathName("json_table_path_1"),
				).Ref(),
			),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS (NESTED PATH E'$' COLUMNS ("a" text), NESTED PATH E'$' AS "json_table_path_1" COLUMNS ("b" text)))`,
			nil,
		},
		{"aliased", Select(Star()).From(table.As("jt", "a", "b")), `SELECT * FROM ` + call + ` AS "jt" ("a", "b")`, []any{1}},
		{
			// PostgreSQL lets JSON_TABLE read an earlier FROM item either way;
			// Lateral writes the keyword.
			"lateral",
			Select(Col("o", "id"), Col("jt", "sku")).
				From(Table("orders").As("o")).
				CrossJoin(
					Lateral(
						JSONTable(Col("o", "doc"), "$[*]", JSONColumn("sku", TypeText)).EmptyArrayOnError().As(
							"jt",
						),
					),
				),
			`SELECT "o"."id", "jt"."sku" FROM ("orders" AS "o" CROSS JOIN LATERAL JSON_TABLE("o"."doc", E'$[*]' COLUMNS ("sku" text) EMPTY ARRAY ON ERROR) AS "jt")`,
			nil,
		},
		{
			"exists_column_behaviors",
			Select(Star()).From(JSONTable(Col("doc"), "$",
				JSONExistsColumn("a", TypeBool).ErrorOnError(),
				JSONExistsColumn("b", TypeBool).TrueOnError(),
				JSONExistsColumn("c", TypeInt4).UnknownOnError(),
			).Ref()),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" boolean EXISTS ERROR ON ERROR, "b" boolean EXISTS TRUE ON ERROR, "c" integer EXISTS UNKNOWN ON ERROR))`,
			nil,
		},
		{
			"value_column_behaviors",
			Select(Star()).From(JSONTable(Col("doc"), "$",
				JSONColumn("a", TypeJSONB).WithWrapper().ErrorOnEmpty().EmptyArrayOnError(),
				JSONColumn("b", TypeJSONB).EmptyObjectOnEmpty().DefaultOnError(LiteralString("{}")),
			).Ref()),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" jsonb WITH WRAPPER ERROR ON EMPTY EMPTY ARRAY ON ERROR, "b" jsonb EMPTY OBJECT ON EMPTY DEFAULT E'{}' ON ERROR))`,
			nil,
		},
	})
}

// Each clause holds one choice, which a later call for the same clause
// replaces. Calls for different clauses never change each other.
func TestSQLJSONLaterChoiceReplacesEarlier(t *testing.T) {
	t.Parallel()

	doc, path := Col("doc"), LiteralString("$")
	runCases(t, []renderCase{
		{"wrapper", Select(JSONQuery(doc, path).WithWrapper().WithConditionalWrapper().Expr()), `SELECT JSON_QUERY("doc", E'$' WITH CONDITIONAL WRAPPER)`, nil},
		{
			"wrapper_removed_before_omit_quotes",
			Select(JSONQuery(doc, path).WithWrapper().WithoutWrapper().OmitQuotes().Expr()),
			`SELECT JSON_QUERY("doc", E'$' WITHOUT WRAPPER OMIT QUOTES)`,
			nil,
		},
		{"quotes", Select(JSONQuery(doc, path).OmitQuotes().KeepQuotes().WithWrapper().Expr()), `SELECT JSON_QUERY("doc", E'$' WITH WRAPPER KEEP QUOTES)`, nil},
		{
			"omit_quotes_keeps_without_wrapper",
			Select(JSONQuery(doc, path).WithoutWrapper().OmitQuotes().Expr()),
			`SELECT JSON_QUERY("doc", E'$' WITHOUT WRAPPER OMIT QUOTES)`,
			nil,
		},
		{
			"without_wrapper_keeps_omit_quotes",
			Select(JSONQuery(doc, path).OmitQuotes().WithoutWrapper().Expr()),
			`SELECT JSON_QUERY("doc", E'$' WITHOUT WRAPPER OMIT QUOTES)`,
			nil,
		},
		{"wrapper_keeps_keep_quotes", Select(JSONQuery(doc, path).KeepQuotes().WithWrapper().Expr()), `SELECT JSON_QUERY("doc", E'$' WITH WRAPPER KEEP QUOTES)`, nil},
		{
			"behavior",
			Select(
				JSONValue(doc, path).NullOnError().DefaultOnError(LiteralInt(1)).ErrorOnError().Expr(),
			),
			`SELECT JSON_VALUE("doc", E'$' ERROR ON ERROR)`,
			nil,
		},
		{
			"returning",
			Select(JSONQuery(doc, path).ReturningFormatJSON(TypeText).Returning(TypeJSONB).Expr()),
			`SELECT JSON_QUERY("doc", E'$' RETURNING jsonb)`,
			nil,
		},
		{"null_clause", Select(JSONArray(doc).NullOnNull().AbsentOnNull().Expr()), `SELECT JSON_ARRAY("doc" ABSENT ON NULL)`, nil},
		{
			"uniqueness",
			Select(JSONObject(JSONPair(path, doc)).WithUniqueKeys().WithoutUniqueKeys().Expr()),
			`SELECT JSON_OBJECT(E'$' : "doc" WITHOUT UNIQUE KEYS)`,
			nil,
		},
		{"item_type", Select(IsJSON(doc).Object().Array().Expr()), `SELECT ("doc" IS JSON ARRAY)`, nil},
		{"window", Select(JSONArrayAggregate(doc).Over(Window()).OverNamed("w").Expr()), `SELECT JSON_ARRAYAGG("doc") OVER "w"`, nil},
		{
			"column_format",
			Select(Star()).From(
				JSONTable(
					doc,
					"$",
					JSONColumn("a", TypeNamed("bytea")).FormatJSON().FormatJSONUTF8(),
				).Ref(),
			),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" "bytea" FORMAT JSON ENCODING UTF8))`, nil,
		},
		{
			"column_path",
			Select(Star()).From(
				JSONTable(doc, "$", JSONColumn("a", TypeText).Path("$.x").Path("$.y")).Ref(),
			),
			`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" text PATH E'$.y'))`,
			nil,
		},
		{
			"filter_appends",
			Select(
				JSONArrayAggregate(doc).Filter(Col("a").IsNull()).Filter(Col("b").IsNull()).Expr(),
			),
			`SELECT JSON_ARRAYAGG("doc") FILTER (WHERE ("a" IS NULL) AND ("b" IS NULL))`,
			nil,
		},
	})
}

func TestSQLJSONRenderErrors(t *testing.T) {
	t.Parallel()

	doc := Col("doc")
	var noQuery Rowset
	var noOperand JSONOperand
	var noExpr *Expr
	var noFormatted *JSONFormatted
	var noDocument *JSONBDocument
	table := func(first JSONTableColumn, rest ...JSONTableColumn) Statement {
		return Select(Star()).From(JSONTable(doc, "$", first, rest...).Ref())
	}
	for _, tc := range []struct {
		name      string
		statement Statement
		clause    string
		detail    string
	}{
		{"empty_object_null_clause", Select(JSONObject().AbsentOnNull().Expr()), "JSON_OBJECT", "an empty constructor accepts RETURNING only"},
		{"empty_object_uniqueness", Select(JSONObject().WithUniqueKeys().Expr()), "JSON_OBJECT", "an empty constructor accepts RETURNING only"},
		{"empty_array_null_clause", Select(JSONArray().NullOnNull().Expr()), "JSON_ARRAY", "an empty constructor accepts RETURNING only"},
		{"array_query_missing", Select(JSONArrayQuery(noQuery).Expr()), "JSON_ARRAY", "requires a query"},
		{"array_query_two_columns", Select(JSONArrayQuery(Select("a", "b").From("t")).Expr()), "JSON_ARRAY", "query must return exactly one column"},
		{"array_query_no_columns", Select(JSONArrayQuery(SelectNoColumns().From("t")).Expr()), "JSON_ARRAY", "query must return exactly one column"},
		{"nil_operand", Select(JSONArray(doc, noOperand).Expr()), "SQL/JSON", "requires a value"},
		{"nil_document", Select(JSONValue(noOperand, LiteralString("$")).Expr()), "SQL/JSON", "requires a value"},
		{"nil_passing", Select(JSONExists(doc, LiteralString("$")).Passing("x", noOperand).Expr()), "SQL/JSON", "requires a value"},
		{"table_duplicate_columns", table(JSONColumn("a", TypeText), JSONExistsColumn("a", TypeBool)), "JSON_TABLE", "duplicate column or path name"},
		{"table_duplicate_ordinality", table(JSONColumn("a", TypeText), JSONOrdinality("a")), "JSON_TABLE", "duplicate column or path name"},
		{
			"table_duplicate_nested_column",
			table(JSONColumn("a", TypeText), JSONNested("$", JSONColumn("a", TypeText))),
			"JSON_TABLE",
			"duplicate column or path name",
		},
		{
			"table_column_repeats_path_name",
			table(JSONColumn("a", TypeText), JSONNested("$", JSONColumn("b", TypeText)).PathName("a")),
			"JSON_TABLE",
			"duplicate column or path name",
		},
		{
			"table_path_names_repeat",
			Select(Star()).From(
				JSONTable(doc, "$", JSONNested("$", JSONColumn("b", TypeText)).PathName("p")).PathName(
					"p",
				).Ref(),
			),
			"JSON_TABLE", "duplicate column or path name",
		},
		{
			"table_column_takes_generated_row_path_name",
			table(JSONColumn("json_table_path_0", TypeText)),
			"JSON_TABLE",
			"json_table_path_0 is the name PostgreSQL gives the unnamed row path",
		},
		{
			"table_nested_path_takes_generated_row_path_name",
			table(JSONNested("$", JSONColumn("a", TypeText)).PathName("json_table_path_0")),
			"JSON_TABLE", "json_table_path_0 is the name PostgreSQL gives the unnamed row path",
		},
		{
			"default_column",
			Select(JSONValue(doc, LiteralString("$")).DefaultOnEmpty(Col("fallback")).Expr()),
			"DEFAULT",
			"cannot reference a column",
		},
		{
			"default_parenthesized_column",
			Select(
				JSONQuery(doc, LiteralString("$")).DefaultOnError(Col("fallback").Parenthesized()).Expr(),
			),
			"DEFAULT",
			"cannot reference a column",
		},
		{
			"omit_quotes_with_wrapper",
			Select(JSONQuery(doc, LiteralString("$")).WithWrapper().OmitQuotes().Expr()),
			"OMIT QUOTES",
			"cannot combine with WITH WRAPPER",
		},
		{
			"conditional_wrapper_with_omit_quotes",
			Select(JSONQuery(doc, LiteralString("$")).OmitQuotes().WithConditionalWrapper().Expr()),
			"OMIT QUOTES",
			"cannot combine with WITH WRAPPER",
		},
		{"column_omit_quotes_with_wrapper", table(JSONColumn("a", TypeText).WithWrapper().OmitQuotes()), "OMIT QUOTES", "cannot combine with WITH WRAPPER"},
		{
			"ordered_aggregate_over_window",
			Select(JSONArrayAggregate(doc).OrderBy(Col("n").Asc()).Over(Window()).Expr()),
			"OVER",
			"an aggregate with ORDER BY cannot be a window function",
		},
		{
			"ordered_aggregate_over_named_window",
			Select(JSONArrayAggregate(doc).OverNamed("w").OrderBy(Col("n").Asc()).Expr()),
			"OVER",
			"an aggregate with ORDER BY cannot be a window function",
		},
		{
			"ordered_call_over_window",
			Select(Call("array_agg", doc).OrderBy(Col("n").Asc()).Over(Window())),
			"OVER",
			"an aggregate with ORDER BY cannot be a window function",
		},
		{"nil_expression_pointer", Select(JSONArray(noExpr).Expr()), "SQL/JSON", "requires a value"},
		{"nil_formatted_pointer", Select(JSONParse(noFormatted).Expr()), "SQL/JSON", "requires a value"},
		{"nil_document_pointer", Select(JSONValue(noDocument, LiteralString("$")).Expr()), "SQL/JSON", "requires a value"},
		{
			"table_path_empty",
			Select(Star()).From(JSONTable(doc, "", JSONColumn("a", TypeText)).Ref()),
			"PATH",
			"requires a path",
		},
		{"column_path_empty", table(JSONColumn("a", TypeText).Path("")), "PATH", "requires a path"},
		{"exists_column_path_empty", table(JSONExistsColumn("a", TypeBool).Path("")), "PATH", "requires a path"},
		{"nested_path_empty", table(JSONNested("", JSONColumn("a", TypeText))), "PATH", "requires a path"},
		{
			"table_too_many_aliases",
			Select(Star()).From(
				JSONTable(doc, "$", JSONColumn("a", TypeText), JSONNested("$", JSONOrdinality("n"))).As(
					"jt",
					"x",
					"y",
					"z",
				),
			),
			"JSON_TABLE", "too many column aliases",
		},
		{
			"default_qualified_column",
			table(JSONColumn("a", TypeText).DefaultOnEmpty(Col("t", "fallback").Cast(TypeText))),
			"DEFAULT",
			"cannot reference a column",
		},
		{
			"default_schema_qualified_column",
			Select(
				JSONValue(doc, LiteralString("$")).DefaultOnEmpty(Col("public", "t", "fallback")).Expr(),
			),
			"DEFAULT",
			"cannot reference a column",
		},
		{
			"default_relation_column",
			Select(
				JSONQuery(doc, LiteralString("$")).DefaultOnError(
					Table("public", "t").Col("fallback"),
				).Expr(),
			),
			"DEFAULT",
			"cannot reference a column",
		},
		{"table_zero_value", Select(Star()).From(JSONTableBuilder{}.Ref()), "", ""},
		{"table_nil_column", table(JSONColumn("a", TypeText), nil), "COLUMNS", "nil column"},
		{"nested_zero_value", table(JSONTableNestedColumns{}), "PATH", "requires a path"},
		{"zero_value_function", Select(JSONValueBuilder{}.Expr()), "", ""},
		{"zero_value_predicate", Select(JSONPredicateBuilder{}.Expr()), "", ""},
		{"window_name_empty", Select(JSONArrayAggregate(doc).OverNamed("").Expr()), "", ""},
		{"passing_name_empty", Select(JSONExists(doc, LiteralString("$")).Passing("", doc).Expr()), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.statement, ErrInvalid)
			if tc.clause == "" {
				return
			}
			_, _, err := tc.statement.ToSQL()
			got, _ := errors.AsType[*RenderError](err)
			if got.Clause != tc.clause || got.Detail != tc.detail {
				t.Fatalf("got (%s) %q, want (%s) %q", got.Clause, got.Detail, tc.clause, tc.detail)
			}
		})
	}
}

// A nested JSON_TABLE is bounded by the same depth limit as any other
// structure, so a deep column tree cannot exhaust the stack.
func TestSQLJSONTableNestingIsBounded(t *testing.T) {
	t.Parallel()

	var column JSONTableColumn = JSONColumn("leaf", TypeText)
	for range 64 {
		column = JSONNested("$", column)
	}
	statement := Select(Star()).From(JSONTable(Col("doc"), "$", column).Ref())
	checkErrorWithOptions(t, statement, Options{MaxDepth: 16}, ErrDepth)
	if _, _, err := statement.ToSQLWith(Options{MaxDepth: 128}); err != nil {
		t.Fatalf("within the limit: %v", err)
	}
}

// Builders are values: a method call returns a new builder and neither it nor
// a sibling made from the same base can change what the base renders, even
// when the base's backing array has spare capacity.
func TestSQLJSONBuildersShareNoState(t *testing.T) {
	t.Parallel()

	doc, path := Col("doc"), LiteralString("$")

	t.Run("object_members", func(t *testing.T) {
		t.Parallel()
		base := JSONObject(JSONPair(Col("k"), Col("v")))
		base.members = append(make([]JSONMember, 0, 8), base.members...)
		left := base.Members(JSONPair(LiteralString("l"), Param("left")))
		right := base.Members(JSONPair(LiteralString("r"), Param("right")))
		checkSQL(t, Select(base.Expr()), `SELECT JSON_OBJECT("k" : "v")`)
		checkSQL(t, Select(left.Expr()), `SELECT JSON_OBJECT("k" : "v", E'l' : $1)`, "left")
		checkSQL(t, Select(right.Expr()), `SELECT JSON_OBJECT("k" : "v", E'r' : $1)`, "right")
	})
	t.Run("array_values", func(t *testing.T) {
		t.Parallel()
		base := JSONArray(doc)
		base.values = append(make([]jsonOperand, 0, 8), base.values...)
		left, right := base.Values(Param("left")), base.Values(Param("right"))
		checkSQL(t, Select(left.Expr()), `SELECT JSON_ARRAY("doc", $1)`, "left")
		checkSQL(t, Select(right.Expr()), `SELECT JSON_ARRAY("doc", $1)`, "right")
	})
	t.Run("passing", func(t *testing.T) {
		t.Parallel()
		base := JSONValue(doc, path).Passing("a", Param("base"))
		base.passing = append(make([]jsonPassing, 0, 8), base.passing...)
		left, right := base.Passing("b", Param("left")), base.Passing("b", Param("right"))
		checkSQL(t, Select(base.Expr()), `SELECT JSON_VALUE("doc", E'$' PASSING $1 AS "a")`, "base")
		checkSQL(t, Select(left.Expr()), `SELECT JSON_VALUE("doc", E'$' PASSING $1 AS "a", $2 AS "b")`, "base", "left")
		checkSQL(t, Select(right.Expr()), `SELECT JSON_VALUE("doc", E'$' PASSING $1 AS "a", $2 AS "b")`, "base", "right")
	})
	t.Run("aggregate_order_and_filter", func(t *testing.T) {
		t.Parallel()
		base := JSONArrayAggregate(doc).OrderBy(Col("a").Asc()).Filter(Col("a").IsNotNull())
		base.order = append(make([]Order, 0, 8), base.order...)
		base.tail.filter = append(make([]Condition, 0, 8), base.tail.filter...)
		left := base.OrderBy(Col("l").Asc()).Filter(Col("l").IsNull())
		right := base.OrderBy(Col("r").Asc()).Filter(Col("r").IsNull())
		checkSQL(t, Select(left.Expr()), `SELECT JSON_ARRAYAGG("doc" ORDER BY "a" ASC, "l" ASC) FILTER (WHERE ("a" IS NOT NULL) AND ("l" IS NULL))`)
		checkSQL(t, Select(right.Expr()), `SELECT JSON_ARRAYAGG("doc" ORDER BY "a" ASC, "r" ASC) FILTER (WHERE ("a" IS NOT NULL) AND ("r" IS NULL))`)
	})
	t.Run("caller_slices", func(t *testing.T) {
		t.Parallel()
		members := []JSONMember{JSONPair(Col("k"), Col("v"))}
		values := []JSONOperand{Col("a")}
		columns := []JSONTableColumn{JSONColumn("b", TypeText)}
		object, array := JSONObject(members...), JSONArray(values...)
		table := JSONTable(doc, "$", JSONColumn("a", TypeText), columns...)
		nested := JSONNested("$", JSONColumn("a", TypeText), columns...)
		members[0], values[0], columns[0] = JSONPair(Col("x"), Col("x")), Col("x"), JSONOrdinality(
			"x",
		)
		checkSQL(t, Select(object.Expr(), array.Expr()), `SELECT JSON_OBJECT("k" : "v"), JSON_ARRAY("a")`)
		checkSQL(t, Select(Star()).From(table.Ref()), `SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" text, "b" text))`)
		checkSQL(t, Select(Star()).From(JSONTable(doc, "$", nested).Ref()), `SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS (NESTED PATH E'$' COLUMNS ("a" text, "b" text)))`)
	})
	t.Run("expression_snapshots_builder", func(t *testing.T) {
		t.Parallel()
		builder := JSONQuery(doc, path)
		expression := builder.Expr()
		relation := JSONTable(doc, "$", JSONColumn("a", TypeText))
		source := relation.Ref()
		// Later calls return new values; the expression and relation keep theirs.
		_ = builder.WithWrapper().ErrorOnError()
		_ = relation.ErrorOnError().PathName("p")
		checkSQL(t, Select(expression), `SELECT JSON_QUERY("doc", E'$')`)
		checkSQL(t, Select(Star()).From(source), `SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("a" text))`)
	})
}

// A failure anywhere in a JSON_TABLE is located inside it, so that a query
// with several tells which one failed.
func TestSQLJSONTableFailuresAreLocatedInJSONTable(t *testing.T) {
	t.Parallel()

	doc := Col("doc")
	var noOperand JSONOperand
	column := JSONColumn("a", TypeText)
	for name, table := range map[string]JSONTableBuilder{
		"document":  JSONTable(noOperand, "$", column),
		"row_path":  JSONTable(doc, "", column),
		"path_name": JSONTable(doc, "$", column).PathName("\x00"),
		"passing":   JSONTable(doc, "$", column).Passing("x", noOperand),
		"column":    JSONTable(doc, "$", column.Path("")),
		"behavior":  JSONTable(doc, "$", JSONColumn("b", TypeText).DefaultOnEmpty(Col("c"))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := Select(Star()).From(table.Ref()).ToSQL()
			renderError, ok := errors.AsType[*RenderError](err)
			if !ok {
				t.Fatalf("expected a render error, got %v", err)
			}
			if !slices.Contains(renderError.Path, "JSON_TABLE") {
				t.Fatalf("path %q does not locate the failure in JSON_TABLE", renderError.Path)
			}
		})
	}
}
