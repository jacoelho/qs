package qs

import "testing"

func TestMustBuild(t *testing.T) {
	t.Parallel()

	query, args := MustToSQL(Select(Param(1)))
	if query != "SELECT $1" || len(args) != 1 {
		t.Fatal(query, args)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustToSQL(Select())
}

// PostgreSQL-native recipes corresponding to dialect-specific conveniences.
// These are compositions, not automatic dialect-emulation methods.
func TestRecipes(t *testing.T) {
	t.Parallel()

	picked := CTE("picked", SelectCols("id").From("jobs").Where(Eq("status", "ready")).
		OrderBy(Asc("id")).Limit(10).Lock(ForUpdate().SkipLocked()))
	ranked := Select(Col("book.author_id"), Col("book.title"), RowNumber().Over(Window().
		PartitionBy(Col("book.author_id")).OrderBy(Asc("book.title"))).As("rn")).From("book")
	nestedBooks := Select(Coalesce(JSONBAgg(JSONBBuildObject(LiteralString("id"), Col("book.id"), LiteralString("title"), Col("book.title"))).OrderBy(Asc("book.id")), LiteralString("[]").Cast(TypeJSONB))).
		From("book").Where(EqColumns("book.author_id", "author.id"))
	runCases(t, []renderCase{
		{"bounded_update", Update("jobs").With(picked).Set(Set("status", "claimed")).From("picked").Where(EqColumns("jobs.id", "picked.id")).ReturningCols("jobs.id"),
			`WITH "picked" AS (SELECT "id" FROM "jobs" WHERE ("status" = $1) ORDER BY "id" ASC LIMIT $2 FOR UPDATE SKIP LOCKED) UPDATE "jobs" SET "status" = $3 FROM "picked" WHERE ("jobs"."id" = "picked"."id") RETURNING "jobs"."id"`, []any{"ready", 10, "claimed"}},
		{"bounded_delete", DeleteFrom("jobs").With(picked).Using("picked").Where(EqColumns("jobs.id", "picked.id")).ReturningCols("jobs.id"),
			`WITH "picked" AS (SELECT "id" FROM "jobs" WHERE ("status" = $1) ORDER BY "id" ASC LIMIT $2 FOR UPDATE SKIP LOCKED) DELETE FROM "jobs" USING "picked" WHERE ("jobs"."id" = "picked"."id") RETURNING "jobs"."id"`, []any{"ready", 10}},
		{"qualify_via_subquery", SelectCols("r.author_id", "r.title").FromExpr(ranked.As("r")).Where(Eq("r.rn", 1)),
			`SELECT "r"."author_id", "r"."title" FROM (SELECT "book"."author_id", "book"."title", row_number() OVER (PARTITION BY "book"."author_id" ORDER BY "book"."title" ASC) AS "rn" FROM "book") AS "r" WHERE ("r"."rn" = $1)`, []any{1}},
		{"semi_join", SelectCols("author.id").From("author").Where(Exists(Select(LiteralInt(1)).From("book").Where(EqColumns("book.author_id", "author.id")))),
			`SELECT "author"."id" FROM "author" WHERE EXISTS (SELECT 1 FROM "book" WHERE ("book"."author_id" = "author"."id"))`, nil},
		{"anti_join", SelectCols("author.id").From("author").Where(NotExists(Select(LiteralInt(1)).From("book").Where(EqColumns("book.author_id", "author.id")))),
			`SELECT "author"."id" FROM "author" WHERE (NOT EXISTS (SELECT 1 FROM "book" WHERE ("book"."author_id" = "author"."id")))`, nil},
		{"nested_collection", Select(Col("author.id"), Scalar(nestedBooks).As("books")).From("author"),
			`SELECT "author"."id", (SELECT coalesce(jsonb_agg(jsonb_build_object(E'id', "book"."id", E'title', "book"."title") ORDER BY "book"."id" ASC), (E'[]')::jsonb) FROM "book" WHERE ("book"."author_id" = "author"."id")) AS "books" FROM "author"`, nil},
	})
}
