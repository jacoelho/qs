package qs

import "testing"

func TestTypedFacade(t *testing.T) {
	t.Parallel()

	a, b := Typed[int]("t.a"), Typed[int]("s.b")
	cases := []struct {
		name string
		c    Condition
		sql  string
		args []any
	}{
		{"eq", a.Eq(1), `("t"."a" = $1)`, []any{1}},
		{"ne", a.Ne(1), `("t"."a" <> $1)`, []any{1}},
		{"lt", a.Lt(1), `("t"."a" < $1)`, []any{1}},
		{"lte", a.Lte(1), `("t"."a" <= $1)`, []any{1}},
		{"gt", a.Gt(1), `("t"."a" > $1)`, []any{1}},
		{"gte", a.Gte(1), `("t"."a" >= $1)`, []any{1}},
		{"eq_field", a.EqField(b), `("t"."a" = "s"."b")`, nil},
		{"ne_field", a.NeField(b), `("t"."a" <> "s"."b")`, nil},
		{"lt_field", a.LtField(b), `("t"."a" < "s"."b")`, nil},
		{"lte_field", a.LteField(b), `("t"."a" <= "s"."b")`, nil},
		{"gt_field", a.GtField(b), `("t"."a" > "s"."b")`, nil},
		{"gte_field", a.GteField(b), `("t"."a" >= "s"."b")`, nil},
		{"null", a.IsNull(), `("t"."a" IS NULL)`, nil},
		{"not_null", a.IsNotNull(), `("t"."a" IS NOT NULL)`, nil},
		{"distinct", a.IsDistinctFrom(NullOf[int]()), `("t"."a" IS DISTINCT FROM $1)`, []any{nil}},
		{"not_distinct", a.IsNotDistinctFrom(NonNull(2)), `("t"."a" IS NOT DISTINCT FROM $1)`, []any{2}},
		{"distinct_field", a.IsDistinctFromField(b), `("t"."a" IS DISTINCT FROM "s"."b")`, nil},
		{"not_distinct_field", a.IsNotDistinctFromField(b), `("t"."a" IS NOT DISTINCT FROM "s"."b")`, nil},
		{"in", a.In(1, 2), `("t"."a" IN ($1, $2))`, []any{1, 2}},
		{"not_in", a.NotIn(1, 2), `("t"."a" NOT IN ($1, $2))`, []any{1, 2}},
		{"between", a.Between(1, 2), `("t"."a" BETWEEN $1 AND $2)`, []any{1, 2}},
		{"not_between", a.NotBetween(1, 2), `("t"."a" NOT BETWEEN $1 AND $2)`, []any{1, 2}},
		{"in_query", a.InQuery(Select(Param(1))), `("t"."a" IN (SELECT $1))`, []any{1}},
		{"not_in_query", a.NotInQuery(Select(Param(1))), `("t"."a" NOT IN (SELECT $1))`, []any{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, Select(tc.c.Expr()), "SELECT "+tc.sql, tc.args...)
		})
	}
	checkSQL(t, Select(a.As("value"), TypedExpr[int](Param(1)).Expr()).OrderBy(a.Asc(), b.Desc()), `SELECT "t"."a" AS "value", $1 ORDER BY "t"."a" ASC, "s"."b" DESC`, 1)
	checkSQL(t, Update("t").Set(a.Set(1)), `UPDATE "t" SET "a" = $1`, 1)
	checkSQL(t, Update("t").Set(a.SetField(b)), `UPDATE "t" SET "a" = "s"."b"`)
	checkSQL(t, Update("t").Set(a.SetNullable(NullOf[int]())), `UPDATE "t" SET "a" = $1`, nil)
	checkSQL(t, Update("t").Set(a.SetNull()), `UPDATE "t" SET "a" = NULL`)
	checkSQL(t, Update("t").Set(a.SetDefault()), `UPDATE "t" SET "a" = DEFAULT`)
	checkSQL(t, InsertInto("users").Set(Typed[int]("u.id").Set(42), Typed[string]("u.name").Set("Ana")), `INSERT INTO "users" ("id", "name") VALUES ($1, $2)`, 42, "Ana")
}

func TestTypedTupleOperators(t *testing.T) {
	t.Parallel()

	a, b, c, d := Typed[int]("a"), Typed[string]("b"), Typed[bool]("c"), Typed[int64]("d")
	r2, r3, r4 := Tuple2(a, b), Tuple3(a, b, c), Tuple4(a, b, c, d)
	operators := []struct {
		op               string
		two, three, four Condition
	}{
		{"=", r2.EqValues(1, "x"), r3.EqValues(1, "x", true), r4.EqValues(1, "x", true, 4)},
		{"<>", r2.NeValues(1, "x"), r3.NeValues(1, "x", true), r4.NeValues(1, "x", true, 4)},
		{"<", r2.LtValues(1, "x"), r3.LtValues(1, "x", true), r4.LtValues(1, "x", true, 4)},
		{"<=", r2.LteValues(1, "x"), r3.LteValues(1, "x", true), r4.LteValues(1, "x", true, 4)},
		{">", r2.GtValues(1, "x"), r3.GtValues(1, "x", true), r4.GtValues(1, "x", true, 4)},
		{">=", r2.GteValues(1, "x"), r3.GteValues(1, "x", true), r4.GteValues(1, "x", true, 4)},
	}
	for _, tt := range operators {
		checkSQL(t, Select(tt.two.Expr()), `SELECT (ROW("a", "b") `+tt.op+` ROW($1, $2))`, 1, "x")
		checkSQL(t, Select(tt.three.Expr()), `SELECT (ROW("a", "b", "c") `+tt.op+` ROW($1, $2, $3))`, 1, "x", true)
		checkSQL(t, Select(tt.four.Expr()), `SELECT (ROW("a", "b", "c", "d") `+tt.op+` ROW($1, $2, $3, $4))`, 1, "x", true, int64(4))
	}
	checkSQL(t, Select(r2.Expr(), r3.Expr(), r4.Expr()), `SELECT ROW("a", "b"), ROW("a", "b", "c"), ROW("a", "b", "c", "d")`)
}
