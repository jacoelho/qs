package qs_test

import (
	"fmt"

	"github.com/jacoelho/qs"
)

func ExampleSelect() {
	query, args, err := qs.Select(qs.Col("id"), qs.Col("name")).From("users").
		Where(qs.Eq("active", true), qs.IsNull("deleted_at")).
		OrderBy(qs.Desc("created_at"), qs.Desc("id")).Limit(20).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "name" FROM "users" WHERE ("active" = $1) AND ("deleted_at" IS NULL) ORDER BY "created_at" DESC, "id" DESC LIMIT $2
	// [true 20] <nil>
}

func ExampleCTE() {
	recent := qs.CTE("recent", qs.SelectCols("user_id").From("orders").Where(qs.Gt("total", 100)))
	query, args, err := qs.SelectCols("u.id").With(recent).
		FromExpr(qs.InnerJoin(qs.Table("users").As("u"), recent.Ref()).On(qs.EqColumns("u.id", "recent.user_id"))).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// WITH "recent" AS (SELECT "user_id" FROM "orders" WHERE ("total" > $1)) SELECT "u"."id" FROM ("users" AS "u" JOIN "recent" ON ("u"."id" = "recent"."user_id"))
	// [100] <nil>
}

func ExampleNull() {
	type Patch struct{ Email qs.Optional[qs.Null[string]] }
	patch := Patch{Email: qs.Some(qs.NullOf[string]())}
	q := qs.Update("users").Where(qs.Eq("id", 42))
	if patch.Email.Present {
		q.Set(qs.SetNullable("email", patch.Email.Value))
	}
	query, args, err := q.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// UPDATE "users" SET "email" = $1 WHERE ("id" = $2)
	// [<nil> 42] <nil>
}

func ExampleInsertInto() {
	query, args, err := qs.InsertInto("users").Columns("id", "name").Values(42, "Ana").
		OnConflict(qs.ConflictColumns("id").DoUpdate(qs.SetExpr("name", qs.Excluded("name")))).
		ReturningCols("id").ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name" RETURNING "id"
	// [42 Ana] <nil>
}

func ExampleFragment() {
	// Trusted text is separate from identifiers and bound values. The JSONB
	// operator is not confused with a placeholder.
	condition := qs.AsCondition(qs.Fragment(qs.Col("metadata"), qs.UnsafeSQL(" ? "), qs.Param("reference")))
	query, args, err := qs.SelectCols("id").From("payments").Where(condition).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "payments" WHERE "metadata" ? $1
	// [reference] <nil>
}

func ExampleSelectSQL() {
	// Existing source-code SQL can be retained without parsing it. This is
	// equivalent to Select(UnsafeSQL("id, created_at")).
	query, args, err := qs.SelectSQL("id, created_at").
		Columns(qs.Col("team_id").Cast(qs.Text)).
		From("users").Where(qs.Eq("active", true)).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT id, created_at, ("team_id")::text FROM "users" WHERE ("active" = $1)
	// [true] <nil>
}
