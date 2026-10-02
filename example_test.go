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
		Columns(qs.Col("team_id").Cast(qs.TypeText)).
		From("users").Where(qs.Eq("active", true)).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT id, created_at, ("team_id")::text FROM "users" WHERE ("active" = $1)
	// [true] <nil>
}

func ExampleExpr_Cast() {
	id := "550e8400-e29b-41d4-a716-446655440000"
	query, args, err := qs.Select(qs.Param(id).Cast(qs.TypeUUID)).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT ($1)::uuid
	// [550e8400-e29b-41d4-a716-446655440000] <nil>
}

func filteredUsers(tenantID int, prefix string, roles []string) *qs.SelectBuilder {
	q := qs.SelectCols("id", "email").From("users").
		Where(qs.Eq("tenant_id", tenantID), qs.IsNull("deleted_at"))
	if prefix != "" {
		q.Where(qs.Or(
			qs.ILike("name", prefix+"%"),
			qs.ILike("email", prefix+"%"),
		))
	}
	if len(roles) > 0 {
		q.Where(qs.In("role", roles...))
	}
	return q
}

func ExampleSelectBuilder_filters() {
	query, args, err := filteredUsers(42, "jo", []string{"admin", "editor"}).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)

	query, args, err = filteredUsers(42, "", nil).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "email" FROM "users" WHERE ("tenant_id" = $1) AND ("deleted_at" IS NULL) AND (("name" ILIKE $2) OR ("email" ILIKE $3)) AND ("role" IN ($4, $5))
	// [42 jo% jo% admin editor] <nil>
	// SELECT "id", "email" FROM "users" WHERE ("tenant_id" = $1) AND ("deleted_at" IS NULL)
	// [42] <nil>
}

func ExampleSelectBuilder_pagination() {
	base := qs.SelectCols("id").From("users").Where(qs.Eq("active", true))
	first := base.Clone().OrderBy(qs.Asc("id")).Limit(10).Offset(0)
	next := base.Clone().OrderBy(qs.Asc("id")).Limit(10).Offset(10)

	query, args, err := first.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	query, args, err = next.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	query, args, err = base.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("active" = $1) ORDER BY "id" ASC LIMIT $2 OFFSET $3
	// [true 10 0] <nil>
	// SELECT "id" FROM "users" WHERE ("active" = $1) ORDER BY "id" ASC LIMIT $2 OFFSET $3
	// [true 10 10] <nil>
	// SELECT "id" FROM "users" WHERE ("active" = $1)
	// [true] <nil>
}

func ExampleCTE_report() {
	total := qs.Sum(qs.Col("o.total"))
	paidOrders := qs.CTE("paid_orders",
		qs.Select(qs.Col("o.user_id"), total.As("total_spend")).
			FromExpr(qs.Table("orders").As("o")).
			Where(qs.Eq("o.status", "paid")).
			GroupBy("o.user_id").
			Having(total.Gte(100)),
	)
	tier := qs.Case().
		When(qs.Gte("p.total_spend", 1000), qs.Param("vip")).
		Else(qs.Param("standard")).
		End().As("tier")
	q := qs.Select(qs.Col("u.id"), qs.Col("u.email"), qs.Col("p.total_spend"), tier).
		With(paidOrders).
		FromExpr(qs.InnerJoin(qs.Table("users").As("u"), paidOrders.Ref().As("p")).
			On(qs.EqColumns("u.id", "p.user_id"))).
		Where(qs.Eq("u.active", true)).
		OrderBy(qs.Desc("p.total_spend"), qs.Asc("u.id")).
		Limit(20).Offset(40)

	query, args, err := q.ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// WITH "paid_orders" AS (SELECT "o"."user_id", sum("o"."total") AS "total_spend" FROM "orders" AS "o" WHERE ("o"."status" = $1) GROUP BY "o"."user_id" HAVING (sum("o"."total") >= $2)) SELECT "u"."id", "u"."email", "p"."total_spend", CASE WHEN ("p"."total_spend" >= $3) THEN $4 ELSE $5 END AS "tier" FROM ("users" AS "u" JOIN "paid_orders" AS "p" ON ("u"."id" = "p"."user_id")) WHERE ("u"."active" = $6) ORDER BY "p"."total_spend" DESC, "u"."id" ASC LIMIT $7 OFFSET $8
	// [paid 100 1000 vip standard true 20 40] <nil>
}
