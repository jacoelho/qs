package qs

import (
	"reflect"
	"testing"
)

var benchmarkRoleStatement Statement

func BenchmarkGrammarRoles(b *testing.B) {
	fixtures := []struct {
		name  string
		build func() Statement
		sql   string
		args  []any
	}{
		{"SearchedCase", func() Statement {
			return Select(Case().When(Eq("v", 7), Param("yes")).When(Eq("v", 9), Param("later")).Else(Param("no")).End())
		}, `SELECT CASE WHEN ("v" = $1) THEN $2 WHEN ("v" = $3) THEN $4 ELSE $5 END`, []any{7, "yes", 9, "later", "no"}},
		{"SimpleCase", func() Statement {
			return Select(CaseOf(Col("v")).WhenValue(Param(7), Param("yes")).WhenValue(Param(9), Param("later")).Else(Param("no")).End())
		}, `SELECT CASE "v" WHEN $1 THEN $2 WHEN $3 THEN $4 ELSE $5 END`, []any{7, "yes", 9, "later", "no"}},
		{"Merge", func() Statement {
			return MergeInto("t").Using(Table("s")).On(EqColumns("t.id", "s.id")).When(Matched().And(Eq("s.state", "open")).ThenUpdate(Set("v", Write(Param(7)))), NotMatched().OverridingSystemValue().ThenInsert(Set("id", Write(Param(11))), Set("v", Write(Param(13)))))
		}, `MERGE INTO "t" USING "s" ON ("t"."id" = "s"."id") WHEN MATCHED AND ("s"."state" = $1) THEN UPDATE SET "v" = $2 WHEN NOT MATCHED THEN INSERT ("id", "v") OVERRIDING SYSTEM VALUE VALUES ($3, $4)`, []any{"open", 7, 11, 13}},
		{"Join", func() Statement {
			return Select(Col("a.id")).FromExpr(LeftJoin(Table("a"), Table("b")).On(EqColumns("a.id", "b.id"), Eq("b.active", true)))
		}, `SELECT "a"."id" FROM ("a" LEFT JOIN "b" ON ("a"."id" = "b"."id") AND ("b"."active" = $1))`, []any{true}},
		{"Conflict", func() Statement {
			return InsertInto("t").Columns("v").Values(Write(Param(7))).OnConflict(ConflictColumns("v").TargetWhere(Eq("live", true)).DoUpdate(Set("v", Write(Param(9)))).Where(Eq("state", "open")))
		}, `INSERT INTO "t" ("v") VALUES ($1) ON CONFLICT ("v") WHERE ("live" = $2) DO UPDATE SET "v" = $3 WHERE ("state" = $4)`, []any{7, true, 9, "open"}},
		{"JSONValue", func() Statement {
			return Select(JSONValue(Col("doc"), Param("$.v")).Passing("p", Param(7)).Returning(TypeInt4).OnEmpty(JSONDefault(Param(9))).OnError(JSONNull()).Expr())
		}, `SELECT JSON_VALUE("doc", $1 PASSING $2 AS "p" RETURNING integer DEFAULT $3 ON EMPTY NULL ON ERROR)`, []any{"$.v", 7, 9}},
		{"JSONQuery", func() Statement {
			return Select(JSONQuery(Col("doc"), Param("$.v")).Passing("p", Param(7)).Returning(TypeJSONB).WithConditionalWrapper().KeepQuotes().OnEmpty(JSONEmptyArray()).OnError(JSONEmptyObject()).Expr())
		}, `SELECT JSON_QUERY("doc", $1 PASSING $2 AS "p" RETURNING jsonb WITH CONDITIONAL ARRAY WRAPPER KEEP QUOTES EMPTY ARRAY ON EMPTY EMPTY OBJECT ON ERROR)`, []any{"$.v", 7}},
		{"JSONExists", func() Statement {
			return Select(JSONExists(Col("doc"), Param("$.v")).Passing("p", Param(7)).OnError(JSONFalse()).Expr())
		}, `SELECT JSON_EXISTS("doc", $1 PASSING $2 AS "p" FALSE ON ERROR)`, []any{"$.v", 7}},
	}
	for _, fixture := range fixtures {
		b.Run(fixture.name, func(b *testing.B) {
			statement := fixture.build()
			sql, args, err := statement.ToSQL()
			if err != nil || sql != fixture.sql || !reflect.DeepEqual(args, fixture.args) {
				b.Fatalf("fixture: SQL=%q args=%#v err=%v; want=%q %#v", sql, args, err, fixture.sql, fixture.args)
			}
			b.Run("Construct", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					benchmarkRoleStatement = fixture.build()
				}
			})
			b.Run("ToSQL", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var err error
					benchmarkSQL, benchmarkArgs, err = statement.ToSQL()
					if err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, style := range []PlaceholderStyle{Dollar, Question} {
				name := "AppendDollar"
				if style == Question {
					name = "AppendQuestion"
				}
				b.Run(name, func(b *testing.B) {
					sql := make([]byte, 0, 1024)
					args := make([]any, 0, 16)
					b.ReportAllocs()
					for b.Loop() {
						var err error
						sql, args, err = statement.AppendWith(sql[:0], args[:0], Options{PlaceholderStyle: style})
						if err != nil {
							b.Fatal(err)
						}
					}
					benchmarkArgs = args
				})
			}
		})
	}
}
