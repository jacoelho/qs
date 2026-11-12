package qs_test

import (
	"fmt"
	"strings"

	"github.com/jacoelho/qs"
)

func ExampleSelect() {
	query, args, err := qs.Select(qs.Col("id"), qs.Col("name")).
		From("users").
		Where(qs.Col("active").Eq(qs.Param(true)), qs.Col("deleted_at").IsNull()).
		OrderBy(qs.Col("created_at").Desc(), qs.Col("id").Desc()).
		Limit(20).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "name" FROM "users" WHERE ("active" = $1) AND ("deleted_at" IS NULL) ORDER BY "created_at" DESC, "id" DESC LIMIT $2
	// [true 20] <nil>
}

func ExampleCTE() {
	recent := qs.CTE(
		"recent",
		qs.Select("user_id").From("orders").Where(qs.Col("total").Gt(qs.Param(100))),
	)
	query, args, err := qs.Select(qs.Col("u", "id")).
		With(recent).
		From(
			qs.InnerJoin(qs.Table("users").As("u"), recent.Ref()).
				On(qs.Col("u", "id").Eq(qs.Col("recent", "user_id"))),
		).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// WITH "recent" AS (SELECT "user_id" FROM "orders" WHERE ("total" > $1)) SELECT "u"."id" FROM ("users" AS "u" JOIN "recent" ON ("u"."id" = "recent"."user_id"))
	// [100] <nil>
}

func ExampleNull() {
	type Patch struct{ Email qs.Optional[qs.Null[string]] }
	for _, patch := range []Patch{
		{},
		{Email: qs.Some(qs.NullOf[string]())},
		{Email: qs.Some(qs.NonNull("ana@example.test"))},
	} {
		var assignments []qs.Assignment
		if patch.Email.Present {
			assignments = append(assignments, qs.SetNullable("email", patch.Email.Value))
		}
		// The application chooses no-op behavior for an absent patch.
		if len(assignments) == 0 {
			fmt.Println("no changes")
			continue
		}
		query, args, err := qs.Update("users").
			Set(assignments).
			Where(qs.Col("id").Eq(qs.Param(42))).
			ToSQL()
		if err != nil {
			panic(err)
		}
		fmt.Println(query)
		fmt.Println(args)
	}
	// Output:
	// no changes
	// UPDATE "users" SET "email" = $1 WHERE ("id" = $2)
	// [<nil> 42]
	// UPDATE "users" SET "email" = $1 WHERE ("id" = $2)
	// [ana@example.test 42]
}

func ExampleInsertInto() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictColumns("id").DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).
		Returning("id").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name" RETURNING "id"
	// [42 Ana] <nil>
}

func ExampleFragment() {
	// Trusted text is separate from identifiers and bound values. The JSONB
	// operator is not confused with a placeholder.
	condition := qs.Fragment(qs.Col("metadata"), qs.UnsafeSQL(" ? "), qs.Param("reference")).Condition()
	query, args, err := qs.Select("id").From("payments").Where(condition).ToSQL()
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
		From("users").
		Where(qs.Col("active").Eq(qs.Param(true))).
		ToSQL()
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

func filteredUsers(tenantID int, prefix string, roles []string) qs.SelectBuilder {
	q := qs.Select("id", "email").
		From("users").
		Where(qs.Col("tenant_id").Eq(qs.Param(tenantID)), qs.Col("deleted_at").IsNull())
	if prefix != "" {
		q = q.Where(qs.Or(
			qs.Col("name").ILikePrefix(prefix),
			qs.Col("email").ILikePrefix(prefix),
		))
	}
	if len(roles) > 0 {
		q = q.Where(qs.TypedCol[string]("role").In(roles...))
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

func ExampleTyped_search() {
	users := qs.Table("users").As("u")
	id := qs.Typed[int64](users.Col("id"))
	ids := []int64{10, 20}
	requiredTags := []string{"staff", "active"}
	rawUsername := "Al"

	q := qs.Select(
		id.As("id"),
		users.Col("username"),
		qs.CountAll().Over(qs.Window()).As("total"),
	).From(users)
	if len(ids) > 0 {
		q = q.Where(id.In(ids...))
	}
	if len(requiredTags) > 0 {
		q = q.Where(qs.ArrayContains(users.Col("tags"), qs.ArrayParam(requiredTags, qs.TypeText)))
	}
	if rawUsername != "" {
		q = q.Where(qs.Lower(users.Col("username")).LikePrefix(strings.ToLower(rawUsername)))
	}

	query, args, err := q.OrderBy(id.Asc()).Limit(20).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "u"."id" AS "id", "u"."username", count(*) OVER () AS "total" FROM "users" AS "u" WHERE ("u"."id" IN ($1, $2)) AND ("u"."tags" @> ($3)::text[]) AND (lower("u"."username") LIKE $4 ESCAPE E'!') ORDER BY "u"."id" ASC LIMIT $5
	// [10 20 [staff active] al% 20] <nil>
}

func ExampleSelectBuilder_pagination() {
	base := qs.Select("id").From("users").Where(qs.Col("active").Eq(qs.Param(true)))
	first := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(0)
	next := base.OrderBy(qs.Col("id").Asc()).Limit(10).Offset(10)

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
	total := qs.Sum(qs.Col("o", "total"))
	paidOrders := qs.CTE("paid_orders",
		qs.Select(qs.Col("o", "user_id"), total.As("total_spend")).
			From(qs.Table("orders").As("o")).
			Where(qs.Col("o", "status").Eq(qs.Param("paid"))).
			GroupBy(qs.Col("o", "user_id")).
			Having(total.Gte(qs.Param(100))),
	)
	tier := qs.Case().
		When(qs.Col("p", "total_spend").Gte(qs.Param(1000)), qs.Param("vip")).
		Else(qs.Param("standard")).
		End().As("tier")
	q := qs.Select(qs.Col("u", "id"), qs.Col("u", "email"), qs.Col("p", "total_spend"), tier).
		With(paidOrders).
		From(qs.InnerJoin(qs.Table("users").As("u"), paidOrders.Ref().As("p")).
			On(qs.Col("u", "id").Eq(qs.Col("p", "user_id")))).
		Where(qs.Col("u", "active").Eq(qs.Param(true))).
		OrderBy(qs.Col("p", "total_spend").Desc(), qs.Col("u", "id").Asc()).
		Limit(20).
		Offset(40)

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
		Returning("id").
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
		Where(qs.Col("id").Eq(qs.Param(42))).
		Returning("id", "name").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// UPDATE "users" SET "name" = $1 WHERE ("id" = $2) RETURNING "id", "name"
	// [Ana 42] <nil>
}

func ExampleDeleteFrom() {
	query, args, err := qs.DeleteFrom("users").
		Where(qs.Col("id").Eq(qs.Param(42))).
		Returning("id").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// DELETE FROM "users" WHERE ("id" = $1) RETURNING "id"
	// [42] <nil>
}

func ExampleConflictColumnsTarget_DoUpdate_list() {
	columns := []string{"name", "email"}
	query, args, err := qs.InsertInto("users").
		Columns("id", "name", "email").
		Values(qs.Value(42), qs.Value("Ana"), qs.Value("ana@example.com")).
		OnConflict(qs.ConflictColumns("id").
			DoUpdate(qs.SetAllExcluded(columns...))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name", "email") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name", "email" = "excluded"."email"
	// [42 Ana ana@example.com] <nil>
}

func ExampleConflictIndexTarget_DoUpdate_list() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictIndex(qs.IndexColumn("id")).
			DoUpdate(qs.SetAllExcluded("name"))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name"
	// [42 Ana] <nil>
}

func ExampleConflictConstraintTarget_DoUpdate_list() {
	query, args, err := qs.InsertInto("users").
		Columns("id", "name").
		Values(qs.Value(42), qs.Value("Ana")).
		OnConflict(qs.ConflictConstraint("users_pkey").
			DoUpdate(qs.SetAllExcluded("name"))).
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
			DoUpdate(qs.SetAllExcluded("a.b"))).
		Returning(qs.Col("a.b")).
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

func ExampleInsertTarget_Columns_list() {
	columns := []string{"id", "name"}
	row := []qs.WriteValue{qs.Value(42), qs.Value("Ana")}
	query, args, err := qs.InsertInto("users").Columns(columns...).Rows(row).ToSQL()
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

func ExampleExpr_ILikePrefix() {
	query, args, err := qs.Select("id").
		From("users").
		Where(qs.Col("name").ILikePrefix("a_%")).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("name" ILIKE $1 ESCAPE E'!')
	// [a!_!%%] <nil>
}

func ExampleExpr_LikeSuffix() {
	query, args, err := qs.Select("id").
		From("users").
		Where(qs.Col("email").LikeSuffix("@example.com")).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("email" LIKE $1 ESCAPE E'!')
	// [%@example.com] <nil>
}

func ExampleExpr_ILikeContains() {
	query, args, err := qs.Select("id").
		From("users").
		Where(qs.Col("display_name").ILikeContains("50%_")).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "users" WHERE ("display_name" ILIKE $1 ESCAPE E'!')
	// [%50!%!_%] <nil>
}

func ExampleJSONObject() {
	user := qs.JSONObject(
		qs.JSONKey("id", qs.Col("id")),
		qs.JSONKey("settings", qs.FormatJSON(qs.Col("settings_text"))),
	).AbsentOnNull().Returning(qs.TypeJSONB)
	query, args, err := qs.Select(user.As("user")).
		From("users").
		Where(qs.Col("team_id").Eq(qs.Param(7))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT JSON_OBJECT(E'id' : "id", E'settings' : "settings_text" FORMAT JSON ABSENT ON NULL RETURNING jsonb) AS "user" FROM "users" WHERE ("team_id" = $1)
	// [7] <nil>
}

func ExampleJSONValue() {
	doc := qs.Col("doc")
	total := qs.JSONValue(doc, qs.LiteralString("$.total")).
		Returning(qs.TypeNumeric).DefaultOnEmpty(qs.LiteralInt(0))
	large := qs.JSONExists(doc, qs.LiteralString("$.items[*] ? (@.qty >= $min)")).
		Passing("min", qs.Param(10).Cast(qs.TypeInt4))
	query, args, err := qs.Select(total.As("total")).From("orders").Where(large.Condition()).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT JSON_VALUE("doc", E'$.total' RETURNING numeric DEFAULT 0 ON EMPTY) AS "total" FROM "orders" WHERE JSON_EXISTS("doc", E'$.items[*] ? (@.qty >= $min)' PASSING ($1)::integer AS "min")
	// [10] <nil>
}

func ExampleJSONTable() {
	items := qs.JSONTable(qs.Col("o", "doc"), "$.items[*]",
		qs.JSONOrdinality("position"),
		qs.JSONColumn("sku", qs.TypeText),
		qs.JSONColumn("qty", qs.TypeInt4).Path("$.quantity").DefaultOnEmpty(qs.LiteralInt(1)),
		qs.JSONNested("$.parts[*]", qs.JSONColumn("part", qs.TypeText).Path("$.name")),
	)
	query, args, err := qs.Select(qs.Col("o", "id"), qs.Star("item")).
		From(qs.Table("orders").As("o")).
		CrossJoin(qs.Lateral(items.As("item"))).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "o"."id", "item".* FROM ("orders" AS "o" CROSS JOIN LATERAL JSON_TABLE("o"."doc", E'$.items[*]' COLUMNS ("position" FOR ORDINALITY, "sku" text, "qty" integer PATH E'$.quantity' DEFAULT 1 ON EMPTY, NESTED PATH E'$.parts[*]' COLUMNS ("part" text PATH E'$.name'))) AS "item")
	// [] <nil>
}

// A typed field compares with Go values of its type. For any other operand,
// leave the field with Expr, or state the operand's type with Typed.
func ExampleField_Expr() {
	orders := qs.Table("orders").As("o")
	total := qs.TypedCol[int64]("o", "total")
	limit := qs.Typed[int64](orders.Col("credit").Sub(orders.Col("spent")))

	query, args, err := qs.Select(orders.Col("id")).
		From(orders).
		Where(
			total.Gt(100),
			total.LteField(limit),
			total.Expr().Ne(qs.Coalesce(orders.Col("estimate"), qs.LiteralInt(0))),
		).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "o"."id" FROM "orders" AS "o" WHERE ("o"."total" > $1) AND ("o"."total" <= ("o"."credit" - "o"."spent")) AND ("o"."total" <> coalesce("o"."estimate", 0))
	// [100] <nil>
}

// An interval is a cast string: a constant in the statement text, or a bound
// value. make_interval builds one from numbers.
func ExampleExpr_Cast_interval() {
	created := qs.Col("created_at")
	query, args, err := qs.Select(qs.Col("id")).
		From("jobs").
		Where(
			created.Lt(qs.Now().Sub(qs.LiteralString("1 second").Cast(qs.TypeInterval))),
			created.Gt(qs.Now().Sub(qs.Param("7 days").Cast(qs.TypeInterval))),
			qs.Col("retry_at").Lt(qs.Now().Add(qs.Call("make_interval", qs.NamedArg("secs", qs.Param(30))))),
		).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "jobs" WHERE ("created_at" < (now() - (E'1 second')::interval)) AND ("created_at" > (now() - ($1)::interval)) AND ("retry_at" < (now() + "make_interval"("secs" => $2)))
	// [7 days 30] <nil>
}

// Call reaches any function qs has no helper for. TrimSyntax is the TRIM
// grammar with a direction and the characters to remove.
func ExampleCall() {
	name := qs.Col("name")
	query, args, err := qs.Select(
		qs.Call("md5", name).As("digest"),
		qs.Call("right", name, qs.LiteralInt(4)).As("suffix"),
		qs.TrimSyntax(name, qs.TrimBothDirection, qs.LiteralString("-")).As("trimmed"),
	).
		From("users").
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "md5"("name") AS "digest", "right"("name", 4) AS "suffix", TRIM(BOTH E'-' FROM "name") AS "trimmed" FROM "users"
	// [] <nil>
}

// A lateral subquery reads columns of the relations before it, here the three
// newest items of each order. ON TRUE keeps orders that have none.
func ExampleLateral() {
	orders := qs.Table("orders").As("o")
	newest := qs.Select(qs.Col("sku"), qs.Col("created_at")).
		From("items").
		Where(qs.Col("items", "order_id").Eq(orders.Col("id"))).
		OrderBy(qs.DescCols("created_at")...).
		Limit(3)
	items := qs.Lateral(qs.Subquery(newest, "i"))

	query, args, err := qs.Select(orders.Col("id"), items.Col("sku")).
		From(orders).
		LeftJoin(items, qs.True()).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "o"."id", "i"."sku" FROM ("orders" AS "o" LEFT JOIN LATERAL (SELECT "sku", "created_at" FROM "items" WHERE ("items"."order_id" = "o"."id") ORDER BY "created_at" DESC LIMIT $1) AS "i" ON TRUE)
	// [3] <nil>
}

func ExampleInsertTarget_Set_list() {
	assignments := []qs.Assignment{qs.Set("name", qs.Value("Ana"))}
	if nickname := "ani"; nickname != "" {
		assignments = append(assignments, qs.Set("nickname", qs.Value(nickname)))
	}
	query, args, err := qs.InsertInto("users").Set(assignments).ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// INSERT INTO "users" ("name", "nickname") VALUES ($1, $2)
	// [Ana ani] <nil>
}

func ExampleAscCols() {
	query, args, err := qs.Select("id", "last_name", "first_name").
		From("users").
		OrderBy(qs.AscCols("last_name", "first_name")...).
		OrderBy(qs.Col("id").Desc()).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "last_name", "first_name" FROM "users" ORDER BY "last_name" ASC, "first_name" ASC, "id" DESC
	// [] <nil>
}

// A document stays a document through Key, Path and Index; Cast and TypeOf
// leave it for an expression or a text field.
func ExampleJSONBDocument_TypeOf() {
	doc := qs.JSONBExpr(qs.Col("payload"))
	tags := doc.Key("tags")
	query, args, err := qs.Select(
		doc.Key("quantity").Cast(qs.TypeInt4).As("quantity"),
		qs.Case().
			When(tags.TypeOf().Eq("array"), tags.Expr()).
			Else(qs.JSONBBuildArray(tags.Expr())).
			End().
			As("tags"),
	).
		From("events").
		Where(doc.TypeOf().Eq("object")).
		ToSQL()
	fmt.Println(query)
	fmt.Println(args, err)
	// Output:
	// SELECT (("payload" -> E'quantity'))::integer AS "quantity", CASE WHEN (jsonb_typeof(("payload" -> E'tags')) = $1) THEN ("payload" -> E'tags') ELSE jsonb_build_array(("payload" -> E'tags')) END AS "tags" FROM "events" WHERE (jsonb_typeof("payload") = $2)
	// [array object] <nil>
}
