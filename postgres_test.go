package qs

import "testing"

func TestPostgreSQLJSON(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"value", Select(JSONValue(Col("doc"), Param("$.price")).Returning(Numeric).OnEmpty(JSONNull()).OnError(JSONDefault(Param(0))).As("price")).From("orders"), `SELECT JSON_VALUE("doc", $1 RETURNING numeric NULL ON EMPTY DEFAULT $2 ON ERROR) AS "price" FROM "orders"`, []any{"$.price", 0}},
		{"query", Select(JSONQuery(Col("doc"), Param("$.items[*]")).Returning(JSONB).WithConditionalWrapper().OnEmpty(JSONEmptyArray()).OnError(JSONEmptyObject()).Expr()).From("orders"), `SELECT JSON_QUERY("doc", $1 RETURNING jsonb WITH CONDITIONAL ARRAY WRAPPER EMPTY ARRAY ON EMPTY EMPTY OBJECT ON ERROR) FROM "orders"`, []any{"$.items[*]"}},
		{"exists_passing", Select(Star()).From("orders").Where(JSONExists(Col("doc"), Param("$.items[*] ? (@.price > $minimum)")).Passing("minimum", Param(10)).OnError(JSONFalse()).Condition()), `SELECT * FROM "orders" WHERE JSON_EXISTS("doc", $1 PASSING $2 AS "minimum" FALSE ON ERROR)`, []any{"$.items[*] ? (@.price > $minimum)", 10}},
		{"query_quotes", Select(JSONQuery(Col("doc"), Param("$.name")).Returning(Text).FormatJSON().OmitQuotes().Expr()), `SELECT JSON_QUERY("doc", $1 RETURNING text FORMAT JSON OMIT QUOTES)`, []any{"$.name"}},
		{"table_nested", Select(Star("j")).FromExpr(Table("orders"), JSONTable(Col("doc"), "$.items[*]", JSONOrdinality("pos"), JSONColumn("sku", Text).Path("$.sku"), JSONNested("$.prices[*]", JSONColumn("amount", Numeric).Path("$.amount").OnEmpty(JSONDefault(Param(0))))).Passing("x", Param(1)).OnError(JSONEmptyArray()).As("j")), `SELECT "j".* FROM "orders", JSON_TABLE("doc", E'$.items[*]' PASSING $1 AS "x" COLUMNS ("pos" FOR ORDINALITY, "sku" text PATH E'$.sku', NESTED PATH E'$.prices[*]' COLUMNS ("amount" numeric PATH E'$.amount' DEFAULT $2 ON EMPTY)) EMPTY ARRAY ON ERROR) AS "j"`, []any{1, 0}},
		{"table_columns", Select(Star()).FromExpr(JSONTable(Param(`{"a":1}`), "$", JSONExistsColumn("has_a", Bool).Path("$.a").OnError(JSONUnknown()), JSONColumn("a", JSONB).Path("$.a").WithWrapper().KeepQuotes().OnEmpty(JSONEmptyArray())).PathName("root").As("j")), `SELECT * FROM JSON_TABLE($1, E'$' AS "root" COLUMNS ("has_a" boolean EXISTS PATH E'$.a' UNKNOWN ON ERROR, "a" jsonb PATH E'$.a' WITH UNCONDITIONAL ARRAY WRAPPER KEEP QUOTES EMPTY ARRAY ON EMPTY)) AS "j"`, []any{`{"a":1}`}},
		{"native_json", Select(JSONGetText(Col("doc"), Param("name"))).Where(JSONContains(Col("doc"), Param(`{"active":true}`).Cast(JSONB))), `SELECT ("doc" ->> $1) WHERE ("doc" @> ($2)::jsonb)`, []any{"name", `{"active":true}`}},
		{"native_question_operators", Select(JSONHas(Col("doc"), Param("?")).Expr(), JSONHasAny(Col("doc"), ArrayParam([]string{"a", "b"}, Text)).Expr(), JSONHasAll(Col("doc"), Array(Param("c"))).Expr()), `SELECT ("doc" ? $1), ("doc" ?| ($2)::text[]), ("doc" ?& ARRAY[$3])`, []any{"?", []string{"a", "b"}, "c"}},
		{"native_record", Select(Star()).FromExpr(TableFunc(JSONBToRecord(Param(`{"id":1}`).Cast(JSONB))).DefineColumns(Def("id", Int4)).As("r")), `SELECT * FROM jsonb_to_record(($1)::jsonb) AS "r" ("id" integer)`, []any{`{"id":1}`}},
	})
}
func TestPostgreSQLExpressions(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"empty_condition_identities", Select(In[int]("id").Expr(), NotIn[int]("id").Expr(), And().Expr(), Or().Expr()), `SELECT FALSE, TRUE, TRUE, FALSE`, nil},
		{"array_any", SelectCols("id").From("users").Where(EqAny(Col("id"), ArrayParam([]int64{1, 2, 3}, Int8))), `SELECT "id" FROM "users" WHERE ("id" = ANY(($1)::bigint[]))`, []any{[]int64{1, 2, 3}}},
		{"array_query", Select(ArrayFrom(SelectCols("id").From("users"))), `SELECT ARRAY(SELECT "id" FROM "users")`, nil},
		{"array_empty", Select(Array().Cast(ArrayType(Int4))), `SELECT (ARRAY[])::integer[]`, nil},
		{"array_slice", Select(Col("a").Slice(Param(1), Param(3)), Col("a").SliceFrom(Param(2)), Col("a").SliceTo(Param(4))), `SELECT ("a")[$1:$2], ("a")[$3:], ("a")[:$4]`, []any{1, 3, 2, 4}},
		{"array_assignment", Update("t").Set(Assign(Col("a").Index(Param(2)), Param(4))), `UPDATE "t" SET "a"[$1] = $2`, []any{2, 4}},
		{"range", Select(Star()).From("booking").Where(RangeOverlaps(Col("during"), TSTZRange(Param("2026-01-01"), Param("2026-02-01"), Param("[)")))), `SELECT * FROM "booking" WHERE ("during" && tstzrange($1, $2, $3))`, []any{"2026-01-01", "2026-02-01", "[)"}},
		{"fulltext", SelectCols("id").From("docs").Where(TSMatches(Col("search"), WebsearchToTSQuery(Param("english"), Param("database builder")))).OrderBy(TSRank(Col("search"), PlainToTSQuery(Param("database"))).Desc()), `SELECT "id" FROM "docs" WHERE ("search" @@ websearch_to_tsquery($1, $2)) ORDER BY ts_rank("search", plainto_tsquery($3)) DESC`, []any{"english", "database builder", "database"}},
		{"qualified_operator", Select(QualifiedOperator(Col("a"), "ext", "<->", Param("b"))), `SELECT ("a" OPERATOR("ext".<->) $1)`, []any{"b"}},
		{"collation", Select(Col("name").Collate("C")).OrderBy(Col("name").Using("~<~")), `SELECT ("name" COLLATE "C") ORDER BY "name" USING ~<~`, nil},
		{"custom_type", Select(Param("paid").Cast(NamedType("domain", "state"))), `SELECT ($1)::"domain"."state"`, []any{"paid"}},
		{"numeric_type", Select(Param("123.45").Cast(Decimal(12, 2))), `SELECT ($1)::numeric(12, 2)`, []any{"123.45"}},
		{"quantified_subquery", SelectCols("id").Where(Col("n").GtExpr(AllQuery(SelectCols("n").From("thresholds").Where(Eq("active", true))))), `SELECT "id" WHERE ("n" > ALL (SELECT "n" FROM "thresholds" WHERE ("active" = $1)))`, []any{true}},
		{"row_membership", SelectCols("a", "b").Where(Row(Col("a"), Col("b")).In(Row(Param(1), Param(2)), Row(Param(3), Param(4)))), `SELECT "a", "b" WHERE (ROW("a", "b") IN (ROW($1, $2), ROW($3, $4)))`, []any{1, 2, 3, 4}},
		{"row_subquery", SelectCols("a", "b").Where(Row(Col("a"), Col("b")).InQuery(SelectCols("c", "d").From("t"))), `SELECT "a", "b" WHERE (ROW("a", "b") IN (SELECT "c", "d" FROM "t"))`, nil},
		{"between_symmetric", Select(Col("n").BetweenSymmetric(Param(4), Param(1)).Expr()), `SELECT ("n" BETWEEN SYMMETRIC $1 AND $2)`, []any{4, 1}},
		{"like_escape", Select(Like("name", `a\_%`).Escape(`\`).Expr()), `SELECT ("name" LIKE $1 ESCAPE $2)`, []any{`a\_%`, `\`}},
	})
}
func TestPostgreSQLUtility(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"explain", Explain(SelectCols("id").From("book").Where(Eq("id", 1))).Analyze(false).Costs(true).Format(ExplainJSON), `EXPLAIN (ANALYZE FALSE, COSTS TRUE, FORMAT JSON) SELECT "id" FROM "book" WHERE ("id" = $1)`, []any{1}},
		{"explain_dml_cte", Explain(Select(Star()).With(CTE("d", DeleteFrom("t").ReturningCols("id"))).From("d")), `EXPLAIN WITH "d" AS (DELETE FROM "t" RETURNING "id") SELECT * FROM "d"`, nil},
		{"truncate", Truncate("a", "b").RestartIdentity().Cascade(), `TRUNCATE TABLE "a", "b" RESTART IDENTITY CASCADE`, nil},
		{"truncate_only", Truncate().TablesExpr(Table("a").Only()).ContinueIdentity().Restrict(), `TRUNCATE TABLE ONLY "a" CONTINUE IDENTITY RESTRICT`, nil},
		{"raw_fragment", Select(Fragment(UnsafeSQL("substring("), Col("name"), UnsafeSQL(" FROM "), Param(1), UnsafeSQL(" FOR "), Param(3), UnsafeSQL(")")).As("part")), `SELECT substring("name" FROM $1 FOR $2) AS "part"`, []any{1, 3}},
	})
}
