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

func ExampleConcat() {
	userFields := func(user qs.Relation, prefix string) projection.Projection[string] {
		return projection.New(
			projection.Named(user.Col("id"), prefix+"_id"),
			projection.Named(user.Col("name"), prefix+"_name"),
		)
	}
	employee := qs.Table("users").As("employee")
	manager := qs.Table("users").As("manager")
	p := projection.Concat(userFields(employee, "employee"), userFields(manager, "manager"))
	text, args, err := qs.Select(p.Expressions()...).FromExpr(
		qs.LeftJoin(employee, manager).On(employee.Col("manager_id").EqExpr(manager.Col("id"))),
	).ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(args)
	fmt.Println(p.Metadata())
	// Output:
	// SELECT "employee"."id" AS "employee_id", "employee"."name" AS "employee_name", "manager"."id" AS "manager_id", "manager"."name" AS "manager_name" FROM ("users" AS "employee" LEFT JOIN "users" AS "manager" ON ("employee"."manager_id" = "manager"."id"))
	// []
	// [employee_id employee_name manager_id manager_name]
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

func ExampleProjection_AppendExpressions() {
	p := projection.New(projection.Named(qs.Col("id"), "id"))
	columns := make([]qs.Expr, 0, 4)
	columns = p.AppendExpressions(columns[:0])
	query := qs.Select(columns...).From("documents")
	// SELECT owns its column positions, so extraction storage can be reused.
	columns[0] = qs.Col("other")
	text, args, err := query.ToSQL()
	if err != nil {
		panic(err)
	}
	fmt.Println(text, args)
	fmt.Println(p.AppendMetadata([]string{"prefix"}))
	// Output:
	// SELECT "id" AS "id" FROM "documents" []
	// [prefix id]
}
