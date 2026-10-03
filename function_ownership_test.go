package qs

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// functionOwnershipFixture keeps construction, reusable rendering and clone
// benchmarks on the same expression shapes.
type functionOwnershipFixture struct {
	name     string
	build    func() Expr
	wantSQL  string
	wantArgs []any
}

var functionOwnershipFixtures = []functionOwnershipFixture{
	{name: "Lag", build: func() Expr { return Lag(Param("value"), Param(1), Param("fallback")) }, wantSQL: `SELECT lag($1, $2, $3)`, wantArgs: []any{"value", 1, "fallback"}},
	{name: "Lead", build: func() Expr { return Lead(Param("value"), Param(1), Param("fallback")) }, wantSQL: `SELECT lead($1, $2, $3)`, wantArgs: []any{"value", 1, "fallback"}},
	{name: "Coalesce", build: func() Expr { return Coalesce(Param("first"), Param("second"), Param("third")) }, wantSQL: `SELECT coalesce($1, $2, $3)`, wantArgs: []any{"first", "second", "third"}},
	{name: "Greatest", build: func() Expr { return Greatest(Param(1), Param(2), Param(3)) }, wantSQL: `SELECT greatest($1, $2, $3)`, wantArgs: []any{1, 2, 3}},
	{name: "Least", build: func() Expr { return Least(Param(1), Param(2), Param(3)) }, wantSQL: `SELECT least($1, $2, $3)`, wantArgs: []any{1, 2, 3}},
	{name: "Substring", build: func() Expr { return Substring(Param("value"), Param(1), Param(3)) }, wantSQL: `SELECT substr($1, $2, $3)`, wantArgs: []any{"value", 1, 3}},
	{name: "Round", build: func() Expr { return Round(Param(1.5), Param(2)) }, wantSQL: `SELECT round($1, $2)`, wantArgs: []any{1.5, 2}},
	{name: "DateTrunc", build: func() Expr { return DateTrunc(Param("hour"), Param("value"), Param("UTC")) }, wantSQL: `SELECT date_trunc($1, $2, $3)`, wantArgs: []any{"hour", "value", "UTC"}},
	{name: "Age", build: func() Expr { return Age(Param("left"), Param("right")) }, wantSQL: `SELECT age($1, $2)`, wantArgs: []any{"left", "right"}},
	{name: "GenerateSeries", build: func() Expr { return GenerateSeries(Param(1), Param(3), Param(1)) }, wantSQL: `SELECT generate_series($1, $2, $3)`, wantArgs: []any{1, 3, 1}},
}

type functionOwnershipChecker interface {
	Helper()
	Fatalf(format string, args ...any)
}

func validateBenchmarkStatement(t functionOwnershipChecker, statement Statement, options Options, wantSQL string, wantArgs ...any) {
	t.Helper()
	sql, args, err := statement.ToSQLWith(options)
	if err != nil {
		t.Fatalf("render benchmark fixture: %v", err)
	}
	if sql != wantSQL {
		t.Fatalf("SQL = %q, want %q", sql, wantSQL)
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func benchmarkQuestionSQL(sql string, arguments int) string {
	replacements := make([]string, 0, arguments*2)
	for i := arguments; i > 0; i-- {
		replacements = append(replacements, "$"+strconv.Itoa(i), "?")
	}
	return strings.NewReplacer(replacements...).Replace(sql)
}

func validateFunctionOwnershipFixture(t functionOwnershipChecker, fixture functionOwnershipFixture) {
	t.Helper()
	validateBenchmarkStatement(t, Select(fixture.build()), Options{}, fixture.wantSQL, fixture.wantArgs...)
}

var (
	functionOwnershipExpr      Expr
	functionOwnershipStatement *SelectBuilder
	functionOwnershipSQLString string
	functionOwnershipSQL       []byte
	functionOwnershipArgs      []any
)

func TestFunctionVariadicArgumentsOwnInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		build func([]Expr) Expr
		want  string
		args  []any
	}{
		{
			name:  "Lag",
			build: func(values []Expr) Expr { return Lag(Param("value"), values...) },
			want:  `SELECT lag($1, $2, $3)`,
			args:  []any{"value", "second", "third"},
		},
		{
			name:  "Lead",
			build: func(values []Expr) Expr { return Lead(Param("value"), values...) },
			want:  `SELECT lead($1, $2, $3)`,
			args:  []any{"value", "second", "third"},
		},
		{
			name:  "Coalesce",
			build: func(values []Expr) Expr { return Coalesce(Param("first"), values...) },
			want:  `SELECT coalesce($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
		{
			name:  "Greatest",
			build: func(values []Expr) Expr { return Greatest(Param("first"), values...) },
			want:  `SELECT greatest($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
		{
			name:  "Least",
			build: func(values []Expr) Expr { return Least(Param("first"), values...) },
			want:  `SELECT least($1, $2, $3)`,
			args:  []any{"first", "second", "third"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []Expr{Param("second"), Param("third")}
			expr := tc.build(values)
			values[0] = Param("changed")
			checkSQL(t, Select(expr), tc.want, tc.args...)
		})
	}
}

func TestFunctionOptionalArgumentsOwnInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		build func([]Expr) Expr
		want  string
		args  []any
	}{
		{
			name:  "Substring",
			build: func(values []Expr) Expr { return Substring(Param("value"), Param(1), values...) },
			want:  `SELECT substr($1, $2, $3)`,
			args:  []any{"value", 1, 3},
		},
		{
			name:  "Round",
			build: func(values []Expr) Expr { return Round(Param(1.5), values...) },
			want:  `SELECT round($1, $2)`,
			args:  []any{1.5, 2},
		},
		{
			name:  "DateTrunc",
			build: func(values []Expr) Expr { return DateTrunc(Param("hour"), Param("value"), values...) },
			want:  `SELECT date_trunc($1, $2, $3)`,
			args:  []any{"hour", "value", "UTC"},
		},
		{
			name:  "Age",
			build: func(values []Expr) Expr { return Age(Param("left"), values...) },
			want:  `SELECT age($1, $2)`,
			args:  []any{"left", "right"},
		},
		{
			name:  "GenerateSeries",
			build: func(values []Expr) Expr { return GenerateSeries(Param(1), Param(3), values...) },
			want:  `SELECT generate_series($1, $2, $3)`,
			args:  []any{1, 3, 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := []Expr{Param(tc.args[len(tc.args)-1])}
			expr := tc.build(values)
			values[0] = Param("changed")
			checkSQL(t, Select(expr), tc.want, tc.args...)
		})
	}
}

func TestFunctionArgumentsKeepNestedBuildersLive(t *testing.T) {
	t.Parallel()

	child := Select(Param("child"))
	expr := Coalesce(Scalar(child), Param("fallback"))
	child.Where(True())
	checkSQL(t, Select(expr), `SELECT coalesce((SELECT $1 WHERE TRUE), $2)`, "child", "fallback")
}

func TestFunctionOwnershipBenchmarkFixtures(t *testing.T) {
	t.Parallel()
	for _, fixture := range functionOwnershipFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			validateFunctionOwnershipFixture(t, fixture)
		})
	}
}

func BenchmarkFunctionOwnershipConstruction(b *testing.B) {
	for _, fixture := range functionOwnershipFixtures {
		b.Run(fixture.name, func(b *testing.B) {
			validateFunctionOwnershipFixture(b, fixture)
			b.ReportAllocs()
			for b.Loop() {
				functionOwnershipExpr = fixture.build()
			}
		})
	}
}

func BenchmarkFunctionOwnershipAppendWarm(b *testing.B) {
	for _, fixture := range functionOwnershipFixtures {
		b.Run(fixture.name, func(b *testing.B) {
			statement := Select(fixture.build())
			validateFunctionOwnershipFixture(b, fixture)
			sql := make([]byte, 0, 256)
			args := make([]any, 0, 16)
			options := Options{MaxDepth: 4096}
			var err error
			sql, args, err = AppendWith(sql, args, statement, options)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sql, args, err = AppendWith(sql[:0], args[:0], statement, options)
				if err != nil {
					b.Fatal(err)
				}
			}
			functionOwnershipSQL, functionOwnershipArgs = sql, args
		})
	}
}

func BenchmarkFunctionOwnershipToSQL(b *testing.B) {
	for _, fixture := range functionOwnershipFixtures {
		b.Run(fixture.name, func(b *testing.B) {
			statement := Select(fixture.build())
			validateFunctionOwnershipFixture(b, fixture)
			b.ReportAllocs()
			for b.Loop() {
				var err error
				functionOwnershipSQLString, functionOwnershipArgs, err = statement.ToSQL()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFunctionOwnershipClone(b *testing.B) {
	for _, fixture := range functionOwnershipFixtures {
		b.Run(fixture.name, func(b *testing.B) {
			statement := Select(fixture.build())
			validateFunctionOwnershipFixture(b, fixture)
			b.ReportAllocs()
			for b.Loop() {
				functionOwnershipStatement = statement.Clone()
			}
		})
	}
}

func BenchmarkFunctionOwnershipRelationCol(b *testing.B) {
	cases := []struct {
		name    string
		rel     Relation
		wantSQL string
	}{
		{name: "ordinary", rel: Table("users"), wantSQL: `SELECT "users"."value"`},
		{name: "schema", rel: Table("public.users"), wantSQL: `SELECT "public"."users"."value"`},
		{name: "alias", rel: Table("users").As("u"), wantSQL: `SELECT "u"."value"`},
		{name: "components", rel: TableIdent("public", "users"), wantSQL: `SELECT "public"."users"."value"`},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			sql, args, err := Select(tc.rel.Col("value")).ToSQL()
			if err != nil {
				b.Fatal(err)
			}
			if sql != tc.wantSQL || len(args) != 0 {
				b.Fatalf("fixture = %q, %#v; want %q, no args", sql, args, tc.wantSQL)
			}
			b.ReportAllocs()
			for b.Loop() {
				functionOwnershipExpr = tc.rel.Col("value")
			}
		})
	}
}

func BenchmarkFunctionOwnershipSetChainAppendWarm(b *testing.B) {
	for _, width := range []int{8, 32, 128} {
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			statement := functionOwnershipSetChain(width)
			sql := make([]byte, 0, 1<<20)
			args := make([]any, 0, width)
			options := Options{MaxDepth: 4096}
			var err error
			sql, args, err = AppendWith(sql, args, statement, options)
			if err != nil {
				b.Fatal(err)
			}
			wantSQL := functionOwnershipSetChainSQL(width)
			if string(sql) != wantSQL || len(args) != 0 {
				b.Fatalf("fixture = %q, %#v; want %q, no args", sql, args, wantSQL)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sql, args, err = AppendWith(sql[:0], args[:0], statement, options)
				if err != nil {
					b.Fatal(err)
				}
			}
			functionOwnershipSQL, functionOwnershipArgs = sql, args
		})
	}
}

func BenchmarkFunctionOwnershipFailureAppend(b *testing.B) {
	statement := Select(Param("value"), Expr{})
	sql := make([]byte, 0, 128)
	args := make([]any, 0, 8)
	options := Options{MaxDepth: 4096}
	_, _, err := AppendWith(sql, args, statement, options)
	if !errors.Is(err, ErrInvalid) {
		b.Fatalf("setup error = %v, want ErrInvalid", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		sql, args, err = AppendWith(sql[:0], args[:0], statement, options)
		if !errors.Is(err, ErrInvalid) {
			b.Fatal(err)
		}
	}
	functionOwnershipSQL, functionOwnershipArgs = sql, args
}

func functionOwnershipSetChain(n int) Rowset {
	statement := Rowset(Select(LiteralInt(1)))
	for i := 2; i <= n; i++ {
		statement = UnionAll(statement, Select(LiteralInt(int64(i))))
	}
	return statement
}

func functionOwnershipSetChainSQL(n int) string {
	var builder strings.Builder
	builder.WriteString("(SELECT 1)")
	for i := 2; i <= n; i++ {
		previous := builder.String()
		builder.Reset()
		if i > 2 {
			builder.WriteByte('(')
		}
		builder.WriteString(previous)
		if i > 2 {
			builder.WriteByte(')')
		}
		builder.WriteString(" UNION ALL (SELECT ")
		builder.WriteString(strconv.Itoa(i))
		builder.WriteByte(')')
	}
	return builder.String()
}
