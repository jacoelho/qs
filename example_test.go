package qs_test

import (
	"fmt"
	"strings"

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
	query, args, err := qs.InsertInto("users").Columns("id", "name").Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictColumns("id").DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).
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
			qs.ILikePrefix("name", prefix),
			qs.ILikePrefix("email", prefix),
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
	// SELECT "id", "email" FROM "users" WHERE ("tenant_id" = $1) AND ("deleted_at" IS NULL) AND (("name" ILIKE $2 ESCAPE E'!') OR ("email" ILIKE $3 ESCAPE E'!')) AND ("role" IN ($4, $5))
	// [42 jo% jo% admin editor] <nil>
	// SELECT "id", "email" FROM "users" WHERE ("tenant_id" = $1) AND ("deleted_at" IS NULL)
	// [42] <nil>
}

func ExampleTypedExpr_search() {
	users := qs.Table("users").As("u")
	id := qs.TypedExpr[int64](users.Col("id"))
	ids := []int64{10, 20}
	requiredTags := []string{"staff", "active"}
	rawUsername := "Al"

	q := qs.Select(
		id.As("id"),
		users.Col("username"),
		qs.CountAll().Over(qs.Window()).As("total"),
	).FromExpr(users)
	if len(ids) > 0 {
		q.Where(id.In(ids...))
	}
	if len(requiredTags) > 0 {
		q.Where(qs.ArrayContains(users.Col("tags"), qs.ArrayParam(requiredTags, qs.TypeText)))
	}
	if rawUsername != "" {
		q.Where(qs.Lower(users.Col("username")).LikePrefix(strings.ToLower(rawUsername)))
	}

	query, args, err := q.OrderBy(id.Asc()).Limit(20).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "u"."id" AS "id", "u"."username", count(*) OVER () AS "total" FROM "users" AS "u" WHERE ("u"."id" IN ($1, $2)) AND ("u"."tags" @> ($3)::text[]) AND (lower("u"."username") LIKE $4 ESCAPE E'!') ORDER BY "u"."id" ASC LIMIT $5
	// [10 20 [staff active] al% 20] <nil>
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

func ExampleInsertInto_row() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		ReturningCols("id").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) RETURNING "id"
	// [42 Ana] <nil>
}

func ExampleUpdate() {
	query, args, err := qs.Update("users").
		Set(qs.Set("name", qs.Value("Ana"))).
		Where(qs.Eq("id", 42)).
		ReturningCols("id", "name").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// UPDATE "users" SET "name" = $1 WHERE ("id" = $2) RETURNING "id", "name"
	// [Ana 42] <nil>
}

func ExampleDeleteFrom() {
	query, args, err := qs.DeleteFrom("users").
		Where(qs.Eq("id", 42)).
		ReturningCols("id").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"
	// [42] <nil>
}

func ExampleConflictColumnsTarget_DoUpdateSlice() {
	columns := []string{"name", "email"}
	query, args, err := qs.InsertInto("users").
		Columns("id", "name", "email").
		Values(qs.Value(42), qs.Value("Ana"), qs.Value("ana@example.com")).
		OnConflict(qs.ConflictColumns("id").
			DoUpdateSlice(qs.SetAllExcluded(columns...))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name", "email" = "excluded"."email"
	// [42 Ana ana@example.com] <nil>
}

func ExampleConflictIndexTarget_DoUpdateSlice() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictIndex(qs.IndexColumn("id")).
			DoUpdateSlice(qs.SetAllExcluded("name"))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
	// [42 Ana] <nil>
}

func ExampleConflictConstraintTarget_DoUpdateSlice() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictConstraint("users_pkey").
			DoUpdateSlice(qs.SetAllExcluded("name"))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ON CONSTRAINT "users_pkey" DO UPDATE SET "name" = "excluded"."name"
	// [42 Ana] <nil>
}

func ExampleSetAllExcluded() {
	query, args, err := qs.InsertInto("settings").
		Columns("id", "a.b").
		Values(qs.Value(42), qs.Value("new")).
		OnConflict(qs.ConflictColumns("id").
			DoUpdateSlice(qs.SetAllExcluded("a.b"))).
		Returning(qs.Ident("a.b")).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "settings" ("id", "a.b") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "a.b" = "excluded"."a.b" RETURNING "a.b"
	// [42 new] <nil>
}

func ExampleParam_nil() {
	query, args, err := qs.Select(qs.Param(nil).Cast(qs.TypeText)).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT ($1)::text
	// [<nil>] <nil>
}

func ExampleInsertTarget_ColumnsSlice() {
	columns := []string{"id", "name"}
	row := []qs.WriteValue{qs.Value(42), qs.Value("Ana")}
	query, args, err := qs.InsertInto("users").ColumnsSlice(columns).ValuesSlice(row).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2)
	// [42 Ana] <nil>
}

func ExampleValuesBuilder_Row_widthError() {
	_, _, err := qs.Values(1).Row(2, 3).ToSQL()
	fmt.Println(err)
	// Output:
	// qs: invalid query (VALUES): row 2: expected 1 value, got 2 values [path: VALUES → row[2]]
}

func ExampleILikePrefix() {
	query, args, err := qs.SelectCols("id").From("users").Where(qs.ILikePrefix("name", "a_%")).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("name" ILIKE $1 ESCAPE E'!')
	// [a!_!%%] <nil>
}

func ExampleLikeSuffix() {
	query, args, err := qs.SelectCols("id").From("users").
		Where(qs.LikeSuffix("email", "@example.com")).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("email" LIKE $1 ESCAPE E'!')
	// [%@example.com] <nil>
}

func ExampleILikeContains() {
	query, args, err := qs.SelectCols("id").From("users").
		Where(qs.ILikeContains("display_name", "50%_")).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("display_name" ILIKE $1 ESCAPE E'!')
	// [%50!%!_%] <nil>
}
