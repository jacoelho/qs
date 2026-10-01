package qx_test

import (
	"fmt"

	"github.com/jacoelho/qx"
)

func ExampleSelect() {
	query, args, err := qx.Select(qx.Col("id"), qx.Col("name")).From("users").
		Where(qx.Eq("active", true), qx.IsNull("deleted_at")).
		OrderBy(qx.Desc("created_at"), qx.Desc("id")).Limit(20).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "name" FROM "users" WHERE ("active" = $1) AND ("deleted_at" IS NULL) ORDER BY "created_at" DESC, "id" DESC LIMIT $2
	// [true 20] <nil>
}

func ExampleCTE() {
	recent := qx.CTE("recent", qx.SelectCols("user_id").From("orders").Where(qx.Gt("total", 100)))
	query, args, err := qx.SelectCols("u.id").With(recent).
		FromExpr(qx.InnerJoin(qx.Table("users").As("u"), recent.Ref()).On(qx.EqColumns("u.id", "recent.user_id"))).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// WITH "recent" AS (SELECT "user_id" FROM "orders" WHERE ("total" > $1)) SELECT "u"."id" FROM ("users" AS "u" JOIN "recent" ON ("u"."id" = "recent"."user_id"))
	// [100] <nil>
}

func ExampleNull() {
	type Patch struct{ Email qx.Optional[qx.Null[string]] }
	patch := Patch{Email: qx.Some(qx.NullOf[string]())}
	q := qx.Update("users").Where(qx.Eq("id", 42))
	if patch.Email.Present {
		q.Set(qx.SetNullable("email", patch.Email.Value))
	}
	query, args, err := q.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// UPDATE "users" SET "email" = $1 WHERE ("id" = $2)
	// [<nil> 42] <nil>
}

func ExampleInsertInto() {
	query, args, err := qx.InsertInto("users").Columns("id", "name").Values(42, "Ana").
		OnConflict(qx.ConflictColumns("id").DoUpdate(qx.SetExpr("name", qx.Excluded("name")))).
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
	condition := qx.AsCondition(qx.Fragment(qx.Col("metadata"), qx.UnsafeSQL(" ? "), qx.Param("reference")))
	query, args, err := qx.SelectCols("id").From("payments").Where(condition).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "payments" WHERE "metadata" ? $1
	// [reference] <nil>
}

func ExampleSelectSQL() {
	// Existing source-code SQL can be retained without parsing it. This is
	// equivalent to Select(UnsafeSQL("id, created_at")).
	query, args, err := qx.SelectSQL("id, created_at").
		Columns(qx.Col("team_id").Cast(qx.Text)).
		From("users").Where(qx.Eq("active", true)).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT id, created_at, ("team_id")::text FROM "users" WHERE ("active" = $1)
	// [true] <nil>
}
