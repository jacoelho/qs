package qs

import (
	"errors"
	"testing"
)

func TestNamedArrays(t *testing.T) {
	t.Parallel()

	ids, names := []int32{42, 43}, []string{"Ana", "Ben"}
	id, name := NamedArray("id", ArrayParam(ids, TypeInt4)), NamedArray(
		"name",
		ArrayParam(names, TypeText),
	)
	u := UnnestAs("u", id, name)
	runCases(t, []renderCase{
		{"relation", Select(u.Col("name"), u.Col("id")).FromExpr(u),
			`SELECT "u"."name", "u"."id" FROM unnest(($1)::integer[], ($2)::text[]) AS "u" ("id", "name")`, []any{ids, names}},
		{"one_array", Select(Star()).FromExpr(UnnestAs("u", name)),
			`SELECT * FROM unnest(($1)::text[]) AS "u" ("name")`, []any{names}},
		{"ordinality", Select(u.Col("id"), u.Col("ordinality")).FromExpr(u.WithOrdinality()),
			`SELECT "u"."id", "u"."ordinality" FROM unnest(($1)::integer[], ($2)::text[]) WITH ORDINALITY AS "u" ("id", "name")`, []any{ids, names}},
		{
			"renamed_with_ordinality",
			Select(Star()).FromExpr(u.WithOrdinality().As("v", "a", "b", "n")),
			`SELECT * FROM unnest(($1)::integer[], ($2)::text[]) WITH ORDINALITY AS "v" ("a", "b", "n")`,
			[]any{ids, names},
		},
		{
			"lateral_column_array",
			Select(Col("e", "tag")).FromExpr(
				Table("posts"),
				Lateral(UnnestAs("e", NamedArray("tag", Col("posts", "tags")))),
			),
			`SELECT "e"."tag" FROM "posts", LATERAL unnest("posts"."tags") AS "e" ("tag")`,
			nil,
		},
		{"insert", InsertInto("users").FromArrays(id, name),
			`INSERT INTO "users" ("id", "name") SELECT * FROM unnest(($1)::integer[], ($2)::text[])`, []any{ids, names}},
		{"insert_reordered", InsertInto("users").FromArrays(name, id),
			`INSERT INTO "users" ("name", "id") SELECT * FROM unnest(($1)::text[], ($2)::integer[])`, []any{names, ids}},
		{
			"insert_clauses",
			InsertInto("users").
				FromArrays(id, name).
				OnConflict(ConflictColumns("id").DoUpdate(SetAllExcluded("name")[0])).
				ReturningCols("id"),
			`INSERT INTO "users" ("id", "name") SELECT * FROM unnest(($1)::integer[], ($2)::text[]) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name" RETURNING "id"`,
			[]any{ids, names},
		},
		{
			"quoted_name",
			InsertInto("users").FromArrays(NamedArray(`a"b`, ArrayParam(names, TypeText))),
			`INSERT INTO "users" ("a""b") SELECT * FROM unnest(($1)::text[])`,
			[]any{names},
		},
	})

	for label, statement := range map[string]Statement{
		"zero column in insert":   InsertInto("users").FromArrays(ArrayColumn{}),
		"zero column in relation": Select(Star()).FromExpr(UnnestAs("u", id, ArrayColumn{})),
		"empty name in insert": InsertInto("users").FromArrays(
			NamedArray("", ArrayParam(ids, TypeInt4)),
		),
		"empty name in relation": Select(Star()).FromExpr(
			UnnestAs("u", NamedArray("", ArrayParam(ids, TypeInt4))),
		),
	} {
		if _, _, err := ToSQL(statement); err == nil {
			t.Errorf("%s rendered", label)
		}
	}
}

func TestNamedArraysOwnTheirLists(t *testing.T) {
	t.Parallel()

	columns := []ArrayColumn{NamedArray("a", Col("x")), NamedArray("b", Col("y"))}
	relation := UnnestAs("u", columns[0], columns[1:]...)
	insert := InsertInto("t").FromArrays(columns[0], columns[1:]...)
	columns[1] = NamedArray("changed", Col("z"))
	checkSQL(t, Select(Star()).FromExpr(relation), `SELECT * FROM unnest("x", "y") AS "u" ("a", "b")`)
	checkSQL(t, insert, `INSERT INTO "t" ("a", "b") SELECT * FROM unnest("x", "y")`)
}

func TestInsertFromNamed(t *testing.T) {
	t.Parallel()

	u := UnnestAs("u", NamedArray("name", ArrayParam([]string{"a"}, TypeText))).WithOrdinality()
	e := TableFunc(JSONBArrayElements(Param(`[]`).Cast(TypeJSONB))).WithOrdinality().As(
		"e",
		"item",
		"n",
	)
	item := JSONBExpr(e.Col("item"))
	runCases(t, []renderCase{
		{
			"column_and_alias",
			InsertInto("events").FromNamed(
				Select(u.Col("name"), u.Col("ordinality").As("position"), Param("x").As("kind")).FromExpr(
					u,
				),
			),
			`INSERT INTO "events" ("name", "position", "kind") SELECT "u"."name", "u"."ordinality" AS "position", $1 AS "kind" FROM unnest(($2)::text[]) WITH ORDINALITY AS "u" ("name")`,
			[]any{"x", []string{"a"}},
		},
		{
			"json_elements",
			InsertInto("events").FromNamed(
				Select(item.TextKey("name").As("name"), e.Col("n")).FromExpr(e),
			),
			`INSERT INTO "events" ("name", "n") SELECT ("e"."item" ->> E'name') AS "name", "e"."n" FROM jsonb_array_elements(($1)::jsonb) WITH ORDINALITY AS "e" ("item", "n")`,
			[]any{`[]`},
		},
		{
			"reference_forms",
			InsertInto("t").FromNamed(
				Select(
					Col("a"),
					Col("s", "b"),
					Col("db", "s", "t", "c"),
					Typed[int](Col("s", "d")).Expr(),
				).From(
					"s",
				),
			),
			`INSERT INTO "t" ("a", "b", "c", "d") SELECT "a", "s"."b", "db"."s"."t"."c", "s"."d" FROM "s"`,
			nil,
		},
		{
			"clauses",
			InsertInto("t").
				FromNamed(Select(Col("a")).From("s")).
				OnConflict(AnyConflict().DoNothing()).
				ReturningCols("a"),
			`INSERT INTO "t" ("a") SELECT "a" FROM "s" ON CONFLICT DO NOTHING RETURNING "a"`,
			nil,
		},
	})

	for label, query := range map[string]SelectBuilder{
		"unaliased expression": Select(Col("a"), Lower(Col("b"))).From("s"),
		"unaliased parameter":  Select(Param(1)),
		"star":                 Select(Star()).From("s"),
		"relation star":        Select(u.Star()).FromExpr(u),
		"trusted SQL":          SelectSQL("a").From("s"),
		"duplicate name":       Select(Col("s", "a"), Col("b").As("a")).From("s"),
		"no columns":           SelectNoColumns().From("s"),
		"aliased star":         Select(Star().As("a")).From("s"),
		"aliased expansion":    Select(Col("c").Fields().As("a")).From("s"),
		"parenthesized column": Select(Col("a").Parenthesized()).From("s"),
	} {
		if _, _, err := ToSQL(InsertInto("t").FromNamed(query)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v; want ErrInvalid", label, err)
		}
	}
}

func TestFieldEqAny(t *testing.T) {
	t.Parallel()

	id := TypedCol[int64]("users", "id")
	ids := []int64{1, 2, 3}
	runCases(t, []renderCase{
		{"values", SelectCols("id").From("users").Where(id.EqAny(ids, TypeInt8)),
			`SELECT "id" FROM "users" WHERE ("users"."id" = ANY(($1)::bigint[]))`, []any{ids}},
		{"nil_binds_empty_array", SelectCols("id").From("users").Where(id.EqAny(nil, TypeInt8)),
			`SELECT "id" FROM "users" WHERE ("users"."id" = ANY(($1)::bigint[]))`, []any{[]int64{}}},
		{"negated", SelectCols("id").From("users").Where(id.EqAny(ids, TypeInt8).Not()),
			`SELECT "id" FROM "users" WHERE (NOT ("users"."id" = ANY(($1)::bigint[])))`, []any{ids}},
	})
}

func TestJSONKey(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"members", Select(JSONObject(
			JSONKey("id", Col("id")),
			JSONKey("settings", FormatJSON(Col("settings_text"))),
			JSONPair(Col("label"), Param(1)),
		).Returning(TypeJSONB).Expr()),
			`SELECT JSON_OBJECT(E'id' : "id", E'settings' : "settings_text" FORMAT JSON, "label" : $1 RETURNING jsonb)`, []any{1}},
		{"escaped", Select(JSONObject(JSONKey(`it's\`, Col("v"))).Expr()),
			`SELECT JSON_OBJECT(E'it''s\\' : "v")`, nil},
	})
	checkError(t, Select(JSONObject(JSONKey("a\x00", Col("v"))).Expr()), ErrInvalid)
}
