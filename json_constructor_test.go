package qs

import (
	"errors"
	"reflect"
	"testing"
)

type jsonConstructorEmbeddedExpr struct{ Expr }
type jsonConstructorEmbeddedRole struct{ JSONInputValue }

func TestJSONConstructorSQL(t *testing.T) {
	t.Parallel()

	cases := []renderCase{
		{
			name: "object",
			statement: Select(JSONObject(
				JSONPair(Param("id"), Param(7)),
				JSONPair(Param("raw"), JSONInputExpr(Param(`{"a":1}`)).FormatJSON()),
			).AbsentOnNull().WithUniqueKeys().Returning(JSONB).Expr()),
			sql:  `SELECT JSON_OBJECT($1 : $2, $3 : $4 FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb)`,
			args: []any{"id", 7, "raw", `{"a":1}`},
		},
		{
			name: "array",
			statement: Select(JSONArray(
				Param(1),
				JSONInputExpr(Param(`{"a":1}`)).FormatJSON(),
			).NullOnNull().Returning(Text).Expr()),
			sql:  `SELECT JSON_ARRAY($1, $2 FORMAT JSON NULL ON NULL RETURNING text)`,
			args: []any{1, `{"a":1}`},
		},
		{
			name:      "empty_object",
			statement: Select(JSONObject().Returning(JSON).Expr()),
			sql:       `SELECT JSON_OBJECT(RETURNING json)`,
		},
		{
			name:      "empty_array",
			statement: Select(JSONArray().Returning(JSONB).Expr()),
			sql:       `SELECT JSON_ARRAY(RETURNING jsonb)`,
		},
		{
			name:      "output_encoding",
			statement: Select(JSONObject().Returning(Bytea).Encoding(JSONEncodingUTF16).Expr()),
			sql:       `SELECT JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF16)`,
		},
		{
			name: "object_aggregate",
			statement: Select(JSONObjectAggregate(Col("k"), Col("v")).AbsentOnNull().WithUniqueKeys().Returning(JSONB).
				Filter(Gt("v", 1)).OverNamed("w").Expr()),
			sql:  `SELECT JSON_OBJECTAGG("k" : "v" ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb) FILTER (WHERE ("v" > $1)) OVER "w"`,
			args: []any{1},
		},
		{
			name: "array_aggregate",
			statement: Select(JSONArrayAggregate(Col("v")).OrderBy(Desc("v")).NullOnNull().Returning(Text).
				Filter(Gt("v", 1)).Over(Window().PartitionBy(Col("g"))).Expr()),
			sql:  `SELECT JSON_ARRAYAGG("v" ORDER BY "v" DESC NULL ON NULL RETURNING text) FILTER (WHERE ("v" > $1)) OVER (PARTITION BY "g")`,
			args: []any{1},
		},
		{
			name:      "array_query",
			statement: Select(JSONArrayQuery(Select(Col("v")).From("items")).InputFormatJSON().Returning(JSONB).Expr()),
			sql:       `SELECT JSON_ARRAY(SELECT "v" FROM "items" FORMAT JSON RETURNING jsonb)`,
		},
		{
			name:      "parse",
			statement: Select(JSONParse(JSONInputExpr(Param(`{"a":1}`)).FormatJSON()).WithUniqueKeys().Expr()),
			sql:       `SELECT JSON($1 FORMAT JSON WITH UNIQUE KEYS)`,
			args:      []any{`{"a":1}`},
		},
		{
			name:      "input_encoding",
			statement: Select(JSONParse(JSONInputExpr(Param([]byte(`{"a":1}`))).Encoding(JSONEncodingUTF32)).Expr()),
			sql:       `SELECT JSON($1 FORMAT JSON ENCODING UTF32)`,
			args:      []any{[]byte(`{"a":1}`)},
		},
		{
			name:      "scalar",
			statement: Select(JSONScalar(Param(7))),
			sql:       `SELECT JSON_SCALAR($1)`,
			args:      []any{7},
		},
		{
			name:      "serialize",
			statement: Select(JSONSerialize(JSONInputExpr(Param(`{"a":1}`)).FormatJSON()).Returning(Bytea).FormatJSON().EncodingUTF8().Expr()),
			sql:       `SELECT JSON_SERIALIZE($1 FORMAT JSON RETURNING bytea FORMAT JSON ENCODING UTF8)`,
			args:      []any{`{"a":1}`},
		},
		{
			name:      "predicate",
			statement: Select(IsJSON(Param(`[1]`)).Array().WithUniqueKeys().Not().Expr()),
			sql:       `SELECT $1 IS NOT JSON ARRAY WITH UNIQUE KEYS`,
			args:      []any{`[1]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSQL(t, tc.statement, tc.sql, tc.args...)
		})
	}

}

func TestJSONConstructorErrorsAndVersions(t *testing.T) {
	t.Parallel()

	bad := []struct {
		name string
		q    Statement
	}{
		{"object_empty_null_policy", Select(JSONObject().NullOnNull().Expr())},
		{"object_empty_unique_policy", Select(JSONObject().WithUniqueKeys().Expr())},
		{"array_empty_null_policy", Select(JSONArray().AbsentOnNull().Expr())},
		{"object_format_without_returning", Select(JSONObject().FormatJSON().Expr())},
		{"array_format_without_returning", Select(JSONArray(Param(1)).FormatJSON().Expr())},
		{"array_nil_input", Select(JSONArray(nil).Expr())},
		{"object_bad_encoding", Select(JSONObject().Returning(Bytea).Encoding(JSONEncoding(99)).Expr())},
		{"query_width", Select(JSONArrayQuery(Select(Param(1), Param(2))).Expr())},
		{"aggregate_empty_window_name", Select(JSONArrayAggregate(Param(1)).OverNamed("").Expr())},
		{"bad_null_policy", Select(JSONArray(Param(1)).OnNull(JSONNullPolicy(99)).Expr())},
		{"bad_unique_policy", Select(JSONObject(JSONPair(Param("a"), Param(1))).Returning(JSON).Expr()).Where(IsJSON(Param("x")).WithUniqueKeys().Type(JSONPredicateItem(99)).Condition())},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.q, ErrInvalid)
		})
	}

	for _, tc := range []struct {
		name string
		q    Statement
	}{
		{"object", Select(JSONObject(JSONPair(Param("a"), Param(1))).Expr())},
		{"array", Select(JSONArray(Param(1)).Expr())},
		{"object_aggregate", Select(JSONObjectAggregate(Param("a"), Param(1)).Expr())},
		{"array_aggregate", Select(JSONArrayAggregate(Param(1)).Expr())},
		{"array_query", Select(JSONArrayQuery(Select(Param(1))).Expr())},
		{"parse", Select(JSONParse(Param(`1`)).Expr())},
		{"scalar", Select(JSONScalar(Param(1)))},
		{"serialize", Select(JSONSerialize(Param(`1`)).Expr())},
		{"is_json", Select(IsJSON(Param(`1`)).Expr())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := ToSQLWith(tc.q, Options{PostgreSQL: PostgreSQL15})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("PostgreSQL 15 accepted SQL/JSON constructor: %v", err)
			}
			if _, _, err := ToSQLWith(tc.q, Options{PostgreSQL: PostgreSQL16}); err != nil {
				t.Fatalf("PostgreSQL 16 rejected SQL/JSON constructor: %v", err)
			}
		})
	}
}

func TestJSONConstructorImmutableListsAndCloneFreeze(t *testing.T) {
	t.Parallel()

	baseObject := JSONObject(JSONPair(Param("a"), Param(1)))
	branchObject := baseObject.Members(JSONPair(Param("b"), Param(2)))
	checkSQL(t, Select(baseObject.Expr()), `SELECT JSON_OBJECT($1 : $2)`, "a", 1)
	checkSQL(t, Select(branchObject.Expr()), `SELECT JSON_OBJECT($1 : $2, $3 : $4)`, "a", 1, "b", 2)

	baseArray := JSONArray(Param(1))
	branchArray := baseArray.Values(Param(2))
	checkSQL(t, Select(baseArray.Expr()), `SELECT JSON_ARRAY($1)`, 1)
	checkSQL(t, Select(branchArray.Expr()), `SELECT JSON_ARRAY($1, $2)`, 1, 2)
	input := JSONInputExpr(Param("raw"))
	formattedInput := input.FormatJSON()
	checkSQL(t, Select(JSONArray(input).Expr()), `SELECT JSON_ARRAY($1)`, "raw")
	checkSQL(t, Select(JSONArray(formattedInput).Expr()), `SELECT JSON_ARRAY($1 FORMAT JSON)`, "raw")

	baseAgg := JSONArrayAggregate(Param(1)).OrderBy(Asc("a"))
	branchAgg := baseAgg.OrderBy(Desc("b"))
	checkSQL(t, Select(baseAgg.Expr()), `SELECT JSON_ARRAYAGG($1 ORDER BY "a" ASC)`, 1)
	checkSQL(t, Select(branchAgg.Expr()), `SELECT JSON_ARRAYAGG($1 ORDER BY "a" ASC, "b" DESC)`, 1)
	checkSQL(t, Select(JSONArrayAggregate(Param(1)).OverNamed("").OverNamed("w").Expr()), `SELECT JSON_ARRAYAGG($1) OVER "w"`, 1)

	child := Select(Param("child"))
	original := Select(
		JSONObject(JSONPair(Param("object"), Scalar(child))).Expr(),
		JSONArray(Scalar(child)).Expr(),
		JSONObjectAggregate(Param("aggregate"), Scalar(child)).Expr(),
		JSONArrayAggregate(Scalar(child)).Expr(),
		JSONArrayQuery(child).Expr(),
		JSONParse(Scalar(child)).Expr(),
		JSONScalar(Scalar(child)),
		JSONSerialize(Scalar(child)).Expr(),
		IsJSON(Scalar(child)).Expr(),
	)
	clone := original.Clone()
	child.Columns(Param("changed"))

	checkSQL(t, clone,
		`SELECT JSON_OBJECT($1 : (SELECT $2)), JSON_ARRAY((SELECT $3)), JSON_OBJECTAGG($4 : (SELECT $5)), JSON_ARRAYAGG((SELECT $6)), JSON_ARRAY(SELECT $7), JSON((SELECT $8)), JSON_SCALAR((SELECT $9)), JSON_SERIALIZE((SELECT $10)), (SELECT $11) IS JSON`,
		"object", "child", "child", "aggregate", "child", "child", "child", "child", "child", "child", "child")
}

func TestJSONConstructorBoundValuesBorrowed(t *testing.T) {
	t.Parallel()

	value := []byte(`{"a":1}`)
	query := Select(JSONSerialize(JSONInputExpr(Param(value))).Expr())
	_, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	value[0] = '{'
	if got := args[0].([]byte); !reflect.DeepEqual(got, value) {
		t.Fatalf("bound JSON input was copied: got %#v want %#v", got, value)
	}
}

func TestJSONConstructorInputRoleValidation(t *testing.T) {
	t.Parallel()

	var nilExpr *Expr
	var nilInput *JSONConstructorInput
	foreignExpr := jsonConstructorEmbeddedExpr{Expr: Param("foreign")}
	foreignRole := jsonConstructorEmbeddedRole{}

	cases := []struct {
		name  string
		value JSONInputValue
		expr  func(JSONInputValue) Expr
	}{
		{
			name:  "typed_nil_expr_array",
			value: nilExpr,
			expr:  func(value JSONInputValue) Expr { return JSONArray(value).Expr() },
		},
		{
			name:  "typed_nil_input_object",
			value: nilInput,
			expr:  func(value JSONInputValue) Expr { return JSONObject(JSONPair(Param("key"), value)).Expr() },
		},
		{
			name:  "foreign_expr_parse",
			value: foreignExpr,
			expr:  func(value JSONInputValue) Expr { return JSONParse(value).Expr() },
		},
		{
			name:  "foreign_role_array",
			value: foreignRole,
			expr:  func(value JSONInputValue) Expr { return JSONArray(value).Expr() },
		},
		{
			name:  "foreign_expr_pointer_object",
			value: &foreignExpr,
			expr:  func(value JSONInputValue) Expr { return JSONObject(JSONPair(Param("key"), value)).Expr() },
		},
		{
			name:  "foreign_role_pointer_parse",
			value: &foreignRole,
			expr:  func(value JSONInputValue) Expr { return JSONParse(value).Expr() },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkError(t, Select(tc.expr(tc.value)), ErrInvalid)
		})
	}
}

func TestJSONConstructorInputPointerCopies(t *testing.T) {
	t.Parallel()

	expr := Param("original")
	input := JSONInputExpr(Param(`{"a":1}`)).FormatJSON()
	statement := Select(
		JSONArray(&expr, &input).Expr(),
		JSONObject(JSONPair(Param("key"), &input)).Expr(),
		JSONParse(&input).Expr(),
	)

	// Pointer inputs are copied as descriptors when attached to a constructor;
	// reassigning the caller's pointees cannot rewrite an already-built AST.
	expr = Param("changed")
	input = JSONInputExpr(Param(`{"b":2}`)).FormatJSON()
	checkSQL(t, statement,
		`SELECT JSON_ARRAY($1, $2 FORMAT JSON), JSON_OBJECT($3 : $4 FORMAT JSON), JSON($5 FORMAT JSON)`,
		"original", `{"a":1}`, "key", `{"a":1}`, `{"a":1}`)
}

func TestJSONConstructorInputRoleRollback(t *testing.T) {
	t.Parallel()

	var nilExpr *Expr
	statement := Select(JSONArray(Param("before"), nilExpr).Expr())
	buf := append(make([]byte, 0, 128), "prefix: "...)
	backing := make([]any, 4)
	backing[0] = "existing"
	args := backing[:1]

	gotSQL, gotArgs, err := AppendWith(buf, args, statement, Options{})
	if !errors.Is(err, ErrInvalid) || string(gotSQL) != "prefix: " || !reflect.DeepEqual(gotArgs, []any{"existing"}) {
		t.Fatalf("rollback SQL=%q args=%#v error=%v", gotSQL, gotArgs, err)
	}
	if &gotSQL[0] != &buf[0] || &gotArgs[0] != &args[0] {
		t.Fatal("rollback did not return caller-owned prefix slices")
	}
	if backing[1] != nil {
		t.Fatalf("rollback retained bound argument: %#v", backing[1])
	}
}

func TestJSONConstructorAppendAllocations(t *testing.T) {
	query := Select(
		JSONObject(JSONPair(Param("k"), Param(1))).Returning(JSONB).Expr(),
		JSONArrayAggregate(Col("value")).OrderBy(Asc("id")).Filter(Gt("value", 0)).Over(Window().PartitionBy(Col("group_id"))).Expr(),
	)
	buf, args, err := AppendSQL(make([]byte, 0, 512), make([]any, 0, 8), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) == 0 || len(args) != 3 {
		t.Fatalf("initial render SQL=%s args=%#v", buf, args)
	}
	allocations := testing.AllocsPerRun(100, func() {
		buf, args, err = AppendSQL(buf[:0], args[:0], query)
	})
	if err != nil || allocations != 0 {
		t.Fatalf("warmed SQL/JSON rendering allocated %g times: %v", allocations, err)
	}
}
