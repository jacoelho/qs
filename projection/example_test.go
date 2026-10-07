package projection_test

import (
	"fmt"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

func ExampleNamed() {
	base := projection.New(
		projection.Named(qs.Col("d.id"), "id"),
		projection.Named(qs.Col("d.payload"), "payload"),
	)
	p := base.With(projection.Named(qs.JSONBCol("d.payload").TextKey("country").Expr(), "country"))
	text, args, err := qs.Select(p.Expressions()...).FromExpr(qs.Table("documents").As("d")).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	fmt.Println(base.Metadata(), p.Metadata())
	// Output:
	// SELECT "d"."id" AS "id", "d"."payload" AS "payload", ("d"."payload" ->> $1) AS "country" FROM "documents" AS "d"
	// [country]
	// [id payload] [id payload country]
}

func ExampleOutput() {
	type key string
	p := projection.New(
		projection.Output(qs.Col("id"), key("document-id")),
		projection.Output(qs.Col("payload"), key("document-payload")),
	)
	text, args, err := qs.Select(p.Expressions()...).From("documents").ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	fmt.Println(p.Metadata())
	// Output:
	// SELECT "id", "payload" FROM "documents"
	// []
	// [document-id document-payload]
}

func ExampleProjection_returning() {
	p := projection.New(
		projection.Named(qs.Col("d.id"), "id"),
		projection.Named(qs.Col("d.payload"), "payload"),
	)
	text, args, err := qs.UpdateTable(qs.Table("documents").As("d")).
		Set(qs.SetNull("archived_at")).Where(qs.Eq("d.id", "document-1")).
		Returning(p.Expressions()...).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// UPDATE "documents" AS "d" SET "archived_at" = NULL WHERE ("d"."id" = $1) RETURNING "d"."id" AS "id", "d"."payload" AS "payload"
	// [document-1]
}

func ExampleProjection_cte() {
	recent := qs.CTE("recent", qs.SelectCols("id", "payload").From("documents").
		OrderBy(qs.Desc("id")).Limit(10))
	p := projection.New(
		projection.Named(qs.Col("r.id"), "id"),
		projection.Named(qs.JSONBCol("r.payload").TextKey("country").Expr(), "country"),
	)
	text, args, err := qs.Select(p.Expressions()...).With(recent).
		FromExpr(recent.Ref().As("r")).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	// Output:
	// WITH "recent" AS (SELECT "id", "payload" FROM "documents" ORDER BY "id" DESC LIMIT $1) SELECT "r"."id" AS "id", ("r"."payload" ->> $2) AS "country" FROM "recent" AS "r"
	// [10 country]
}
