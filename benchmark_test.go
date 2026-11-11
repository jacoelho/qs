package qs

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// Sinks keep benchmark results live.
var (
	benchmarkSQL   string
	benchmarkBytes []byte
	benchmarkArgs  []any
)

// benchmarkScenario is one statement as an application writes it. A request
// builds its statement and renders it once, so BenchmarkBuildAndRender is the
// figure that describes qs in use. BenchmarkRender isolates the renderer.
type benchmarkScenario struct {
	name      string
	statement func() Statement
	// render builds the statement and renders it through its own type, as a
	// caller does.
	render func() (string, []any, error)
	sql    string
	args   []any
}

func scenario[S Statement](name string, build func() S, sql string, args ...any) benchmarkScenario {
	return benchmarkScenario{
		name:      name,
		statement: func() Statement { return build() },
		render:    func() (string, []any, error) { return build().ToSQL() },
		sql:       sql,
		args:      args,
	}
}

func benchQuery() SelectBuilder {
	return SelectCols("id", "name", "created_at").From("users").Where(Col("active").Eq(Param(true)), Col("id").In(Param(1), Param(2), Param(3))).OrderBy(Col("created_at").Desc(), Col("id").Desc()).Limit(50)
}

var benchmarkScenarios = []benchmarkScenario{
	scenario("select", benchQuery,
		`SELECT "id", "name", "created_at" FROM "users" WHERE ("active" = $1) AND ("id" IN ($2, $3, $4)) ORDER BY "created_at" DESC, "id" DESC LIMIT $5`,
		true, 1, 2, 3, 50),
	scenario("keyset_page", func() SelectBuilder {
		return SelectCols("id", "name").From("users").Where(Col("active").Eq(Param(true))).
			OrderBy(Col("users", "created_at").Desc(), Col("users", "id").Desc()).
			SeekAfter(int64(1700000000), int64(42)).Limit(20)
	},
		`SELECT "id", "name" FROM "users" WHERE ("active" = $1) AND (("users"."created_at", "users"."id") < ($2, $3)) ORDER BY "users"."created_at" DESC, "users"."id" DESC LIMIT $4`,
		true, int64(1700000000), int64(42), 20),
	scenario("cte_window", func() SelectBuilder {
		recent := CTE("recent", SelectCols("id").From("users").Where(Col("created_at").Gt(Param(100))))
		return Select(Col("r", "id"), CountAll().Over(Window().OrderBy(Col("r", "id").Asc()))).
			With(recent).FromExpr(recent.Ref().As("r")).
			Where(Col("r", "id").In(Param(1), Param(2), Param(3))).Limit(20)
	},
		`WITH "recent" AS (SELECT "id" FROM "users" WHERE ("created_at" > $1)) SELECT "r"."id", count(*) OVER (ORDER BY "r"."id" ASC) FROM "recent" AS "r" WHERE ("r"."id" IN ($2, $3, $4)) LIMIT $5`,
		100, 1, 2, 3, 20),
	scenario("join", func() SelectBuilder {
		return Select(Col("a", "id")).FromExpr(LeftJoin(Table("a"), Table("b")).
			On(Col("a", "id").Eq(Col("b", "id")), Col("b", "active").Eq(Param(true))))
	},
		`SELECT "a"."id" FROM ("a" LEFT JOIN "b" ON ("a"."id" = "b"."id") AND ("b"."active" = $1))`,
		true),
	scenario("functions", func() SelectBuilder {
		return Select(
			Coalesce(Col("nickname"), Col("name")),
			DateTrunc(Param("day"), Col("created_at")),
			Lag(Col("score"), Param(1)).Over(Window().OrderBy(Col("id").Asc())),
		).From("users")
	},
		`SELECT coalesce("nickname", "name"), date_trunc($1, "created_at"), lag("score", $2) OVER (ORDER BY "id" ASC) FROM "users"`,
		"day", 1),
	scenario("searched_case", func() SelectBuilder {
		return Select(Case().When(Col("v").Eq(Param(7)), Param("yes")).When(Col("v").Eq(Param(9)), Param("later")).Else(Param("no")).End())
	},
		`SELECT CASE WHEN ("v" = $1) THEN $2 WHEN ("v" = $3) THEN $4 ELSE $5 END`,
		7, "yes", 9, "later", "no"),
	scenario("json_document", typedJSONQuery,
		`SELECT ("payload" #>> (ARRAY[E'items', E'0', E'name'])::text[]) AS "name" FROM "events" WHERE ("payload" ?& (ARRAY[E'items', E'active'])::text[]) AND ("payload" @> ($1)::jsonb) AND (((("payload" -> E'items') -> -1) ->> E'name') = $2)`,
		`{"active":true}`, "Ada"),
	scenario("quoted_identifiers", func() SelectBuilder {
		return Select(Col(`odd"name`), Col(`"leading`), Col(`trailing"`), Col(`two""quotes`)).From("events")
	},
		`SELECT "odd""name", """leading", "trailing""", "two""""quotes" FROM "events"`),
	scenario("insert_rows", func() InsertRows {
		return InsertInto("events").Columns("id", "name").Values(Value(1), Value("Ada")).Values(Value(2), Value("Grace"))
	},
		`INSERT INTO "events" ("id", "name") VALUES ($1, $2), ($3, $4)`,
		1, "Ada", 2, "Grace"),
	scenario("insert_expression_and_default", func() InsertRows {
		return InsertInto("events").Targets(Col("id"), Col("payload"), Col("active")).
			Values(Value(1), Write(JSONBParam(`{"a":1}`).Expr()), Default())
	},
		`INSERT INTO "events" ("id", "payload", "active") VALUES ($1, ($2)::jsonb, DEFAULT)`,
		1, `{"a":1}`),
	scenario("insert_eight_rows", func() InsertRows {
		b := InsertInto("events").Columns("id", "name", "active").Values(Value(0), Value("name"), Value(true))
		for i := 1; i < 8; i++ {
			b = b.Values(Value(i), Value("name"), Value(true))
		}
		return b
	},
		`INSERT INTO "events" ("id", "name", "active") VALUES ($1, $2, $3), ($4, $5, $6), ($7, $8, $9), ($10, $11, $12), ($13, $14, $15), ($16, $17, $18), ($19, $20, $21), ($22, $23, $24)`,
		0, "name", true, 1, "name", true, 2, "name", true, 3, "name", true,
		4, "name", true, 5, "name", true, 6, "name", true, 7, "name", true),
	scenario("insert_assignments", func() InsertAssignments {
		return InsertInto("events").Set(
			Set("a", Value(1)), Set("b", Value(2)), Set("c", Value(3)), Set("d", Value(4)),
			Set("e", Value(5)), Set("f", Value(6)), Set("g", Value(7)), Set("h", Value(8)),
		)
	},
		`INSERT INTO "events" ("a", "b", "c", "d", "e", "f", "g", "h") VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		1, 2, 3, 4, 5, 6, 7, 8),
	scenario("upsert", func() InsertRows {
		conflict := ConflictColumns("id").TargetWhere(Col("active").Eq(Param(true))).
			DoUpdate(Set("name", Value("updated"))).Where(Col("locked").Eq(Param(false)))
		return InsertInto("events").Columns("id", "name").Values(Value(1), Value("Ada")).OnConflict(conflict)
	},
		`INSERT INTO "events" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") WHERE ("active" = $3) DO UPDATE SET "name" = $4 WHERE ("locked" = $5)`,
		1, "Ada", true, "updated", false),
	scenario("update_tuple", func() UpdateBuilder {
		return Update("people").Set(
			AssignRow([]Expr{Col("first"), Col("last")}, WriteTuple(Default(), Value("Lovelace"))),
		).Where(Col("id").Eq(Param(7)))
	},
		`UPDATE "people" SET ("first", "last") = (DEFAULT, $1) WHERE ("id" = $2)`,
		"Lovelace", 7),
	scenario("merge", func() MergeBuilder {
		return MergeInto("t").Using(Table("s")).On(Col("t", "id").Eq(Col("s", "id"))).When(
			Matched().And(Col("s", "state").Eq(Param("open"))).ThenUpdate(Set("v", Write(Param(7)))),
			NotMatched().OverridingSystemValue().ThenInsert(Set("id", Write(Param(11))), Set("v", Write(Param(13)))),
		)
	},
		`MERGE INTO "t" USING "s" ON ("t"."id" = "s"."id") WHEN MATCHED AND ("s"."state" = $1) THEN UPDATE SET "v" = $2 WHEN NOT MATCHED THEN INSERT ("id", "v") OVERRIDING SYSTEM VALUE VALUES ($3, $4)`,
		"open", 7, 11, 13),
}

func TestBenchmarkScenariosRenderTheirSQL(t *testing.T) {
	t.Parallel()
	for _, s := range benchmarkScenarios {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, s.statement(), s.sql, s.args...)
		})
	}
}

// BenchmarkBuildAndRender is the cost a request pays: construct the statement,
// then render owned SQL and arguments.
func BenchmarkBuildAndRender(b *testing.B) {
	for _, s := range benchmarkScenarios {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				benchmarkSQL, benchmarkArgs, err = s.render()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRender renders a statement built once into reused storage. It
// isolates the renderer and is not what an application that builds a statement
// per request observes.
func BenchmarkRender(b *testing.B) {
	for _, s := range benchmarkScenarios {
		b.Run(s.name, func(b *testing.B) {
			statement := s.statement()
			sql := make([]byte, 0, 1024)
			args := make([]any, 0, 32)
			b.ReportAllocs()
			for b.Loop() {
				var err error
				sql, args, err = statement.AppendSQL(sql[:0], args[:0])
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkBytes, benchmarkArgs = sql, args
		})
	}
}

// TestBuildAndRenderAllocations bounds what one request pays for the reference
// query. It is a budget, not an exact count: lower it when the path gets
// cheaper, and treat a need to raise it as a regression to justify.
func TestBuildAndRenderAllocations(t *testing.T) {
	const budget = 10
	var err error
	allocs := testing.AllocsPerRun(1000, func() {
		benchmarkSQL, benchmarkArgs, err = benchQuery().ToSQL()
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocs > budget {
		t.Fatalf("building and rendering the reference query allocated %.0f times; budget is %d", allocs, budget)
	}
}

// TestQualifiedColumnAllocations pins what a table-qualified column costs to
// construct. Names written as constants must cost nothing, which holds only
// while Col stays within the compiler's inlining budget.
func TestQualifiedColumnAllocations(t *testing.T) {
	names := []string{"users", "id"}
	relation := Table("users").As("u")
	for _, tc := range []struct {
		name   string
		budget float64
		build  func() Expr
		sql    string
	}{
		{"constant_names", 0, func() Expr { return Col("users", "id") }, `SELECT "users"."id"`},
		{"excluded", 0, func() Expr { return Excluded("id") }, `SELECT "excluded"."id"`},
		{"runtime_names", 1, func() Expr { return Col(names[0], names[1]) }, `SELECT "users"."id"`},
		{"relation_column", 1, func() Expr { return relation.Col("id") }, `SELECT "u"."id"`},
		{"typed_column", 1, func() Expr { return TypedCol[int]("users", "id").Expr() }, `SELECT "users"."id"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var column Expr
			allocs := testing.AllocsPerRun(1000, func() { column = tc.build() })
			if allocs > tc.budget {
				t.Fatalf("constructing the column allocated %.0f times; budget is %.0f", allocs, tc.budget)
			}
			checkSQL(t, Select(column), tc.sql)
		})
	}
}

// BenchmarkRenderUnionChain measures rendering as a left-deep set operation
// grows, which is the deepest structure a caller can build by repetition.
func BenchmarkRenderUnionChain(b *testing.B) {
	for _, width := range []int{8, 32, 128} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			statement := Rowset(Select(LiteralInt(1)))
			for i := 2; i <= width; i++ {
				statement = UnionAll(statement, Select(LiteralInt(int64(i))))
			}
			sql := make([]byte, 0, 1<<20)
			options := Options{MaxDepth: 4096}
			sql, _, err := AppendWith(sql, nil, statement, options)
			if err != nil {
				b.Fatal(err)
			}
			if got := strings.Count(string(sql), " UNION ALL "); got != width-1 {
				b.Fatalf("rendered %d UNION ALL operators; want %d", got, width-1)
			}
			b.ReportAllocs()
			for b.Loop() {
				sql, _, err = AppendWith(sql[:0], nil, statement, options)
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkBytes = sql
		})
	}
}

// BenchmarkRenderFailure measures a render that fails and rolls back.
func BenchmarkRenderFailure(b *testing.B) {
	statement := Select(Param("value"), Expr{})
	sql := make([]byte, 0, 128)
	args := make([]any, 0, 8)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		sql, args, err = statement.AppendSQL(sql[:0], args[:0])
		if !errors.Is(err, ErrInvalid) {
			b.Fatalf("error = %v; want ErrInvalid", err)
		}
	}
	benchmarkBytes, benchmarkArgs = sql, args
}
