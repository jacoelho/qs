package qs_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jacoelho/qs"
)

func TestCorrectnessRelationColUsesLiteralColumnComponent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		relation qs.Relation
		column   string
		want     string
	}{
		{"table", qs.Table("settings"), "a.b", `SELECT "settings"."a.b" FROM "settings"`},
		{"table_ident", qs.TableIdent("settings"), "a.b", `SELECT "settings"."a.b" FROM "settings"`},
		{"table_ident_star", qs.TableIdent("*"), "a.b", `SELECT "*"."a.b" FROM "*"`},
		{"alias", qs.Table("settings").As("s"), "a.b", `SELECT "s"."a.b" FROM "settings" AS "s"`},
		{"schema", qs.Table("app.settings"), "a.b", `SELECT "app"."settings"."a.b" FROM "app"."settings"`},
		{"quoted", qs.Table("settings"), `weird"name`, `SELECT "settings"."weird""name" FROM "settings"`},
		{"table_star", qs.Table("settings"), "*", `SELECT "settings"."*" FROM "settings"`},
		{"alias_star", qs.Table("settings").As("s"), "*", `SELECT "s"."*" FROM "settings" AS "s"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, args, err := qs.Select(tc.relation.Col(tc.column)).FromExpr(tc.relation).ToSQL()
			if err != nil || got != tc.want || len(args) != 0 {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q and no arguments", got, args, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		relation qs.Relation
		want     string
	}{
		{"table", qs.Table("settings"), `SELECT "settings".* FROM "settings"`},
		{"schema", qs.Table("app.settings"), `SELECT "app"."settings".* FROM "app"."settings"`},
		{"alias", qs.Table("settings").As("s"), `SELECT "s".* FROM "settings" AS "s"`},
		{"table_ident", qs.TableIdent("settings"), `SELECT "settings".* FROM "settings"`},
	} {
		t.Run("star/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if got, args, err := qs.Select(tc.relation.Star()).FromExpr(tc.relation).ToSQL(); err != nil || got != tc.want || len(args) != 0 {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q", got, args, err, tc.want)
			}
		})
	}

	for _, relation := range []qs.Relation{qs.Table(""), qs.Table("a..b"), qs.Table("*"), qs.Table("a.*"), qs.Table("*.a"), qs.Table("a.\x00"), qs.Table("a.\xff")} {
		if _, _, err := qs.Select(relation.Col("id")).FromExpr(relation).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("malformed table path accepted: %v", err)
		}
	}
	for _, relation := range []qs.Relation{qs.Table("settings"), qs.TableIdent("settings"), qs.Table("settings").As("s")} {
		for _, column := range []string{"", "bad\x00name", "\xff"} {
			if _, _, err := qs.Select(relation.Col(column)).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
				t.Fatalf("invalid literal column %q accepted: %v", column, err)
			}
		}
	}
	if got, _, err := qs.Select(qs.Col("public.settings.a.b")).ToSQL(); err != nil || got != `SELECT "public"."settings"."a"."b"` {
		t.Fatalf("top-level Col changed: SQL=%q err=%v", got, err)
	}
}

func TestCorrectnessMembershipAllowsUnknownWidths(t *testing.T) {
	t.Parallel()
	values := qs.ValuesExpr(qs.LiteralInt(1), qs.LiteralInt(2))
	relation := qs.Subquery(values, "r", "a", "b")
	unknown := qs.Row(qs.Col("r").Fields())
	known := qs.Row(relation.Col("a"), relation.Col("b"))
	for _, tc := range []struct {
		name      string
		condition qs.Condition
		want      string
	}{
		{"unknown_left_in_query", unknown.InQuery(values), `SELECT (ROW(("r").*) IN (VALUES (1, 2))) FROM (VALUES (1, 2)) AS "r" ("a", "b")`},
		{"unknown_left_not_in_query", unknown.NotInQuery(values), `SELECT (ROW(("r").*) NOT IN (VALUES (1, 2))) FROM (VALUES (1, 2)) AS "r" ("a", "b")`},
		{"unknown_right_in_list", known.In(unknown), `SELECT (ROW("r"."a", "r"."b") IN (ROW(("r").*))) FROM (VALUES (1, 2)) AS "r" ("a", "b")`},
		{"unknown_right_not_in_list", known.NotIn(unknown), `SELECT (ROW("r"."a", "r"."b") NOT IN (ROW(("r").*))) FROM (VALUES (1, 2)) AS "r" ("a", "b")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if sql, args, err := qs.Select(tc.condition.Expr()).FromExpr(relation).ToSQL(); err != nil || sql != tc.want || len(args) != 0 {
				t.Fatalf("SQL=%q args=%#v err=%v; want %q without arguments", sql, args, err, tc.want)
			}
		})
	}
	bad := qs.Select(known.InQuery(qs.ValuesExpr(qs.LiteralInt(1))).Expr()).FromExpr(relation)
	if _, _, err := bad.ToSQL(); !errors.Is(err, qs.ErrInvalid) {
		t.Fatalf("known width mismatch was accepted: %v", err)
	}
	if _, _, err := qs.Select(qs.Row(qs.Col("a"), qs.Col("b")).In(qs.Row(qs.Param(1))).Expr()).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
		t.Fatalf("known row-list mismatch was accepted: %v", err)
	}
	for _, tc := range []struct {
		name string
		c    qs.Condition
		want string
		args []any
	}{
		{"in_query", qs.Row(qs.Col("a"), qs.Col("b")).InQuery(qs.ValuesExpr(qs.Param(1), qs.Param(2))), `SELECT (ROW("a", "b") IN (VALUES ($1, $2)))`, []any{1, 2}},
		{"not_in_list", qs.Row(qs.Col("a"), qs.Col("b")).NotIn(qs.Row(qs.Param(1), qs.Param(2))), `SELECT (ROW("a", "b") NOT IN (ROW($1, $2)))`, []any{1, 2}},
	} {
		t.Run("sql/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got, args, err := qs.Select(tc.c.Expr()).ToSQL()
			if err != nil || got != tc.want || len(args) != len(tc.args) {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q args=%#v", got, args, err, tc.want, tc.args)
			}
			for i := range tc.args {
				if args[i] != tc.args[i] {
					t.Fatalf("args[%d]=%#v, want %#v", i, args[i], tc.args[i])
				}
			}
		})
	}
}

func TestCorrectnessMembershipQueryWidthIsLiveAndCloneable(t *testing.T) {
	t.Parallel()
	child := qs.Select(qs.Col("a"), qs.Col("b")).From("t")
	parent := qs.Select(qs.Row(qs.Col("a"), qs.Col("b")).InQuery(child).Expr())
	clone := parent.Clone()
	child.RemoveColumns().Columns(qs.Col("a"))
	if _, _, err := parent.ToSQL(); !errors.Is(err, qs.ErrInvalid) {
		t.Fatalf("mutated live child did not produce mismatch: %v", err)
	}
	if _, _, err := clone.ToSQL(); err != nil {
		t.Fatalf("clone was contaminated by live child mutation: %v", err)
	}
}

func TestCorrectnessAppendFailureClearsOriginalStorage(t *testing.T) {
	t.Parallel()
	storage := make([]any, 2)
	storage[0] = "prefix"
	prefix := storage[:1]
	outSQL, outArgs, err := qs.Select(qs.Param(new(int)), qs.Param("second"), qs.Expr{}).AppendSQL([]byte("/* prefix */ "), prefix)
	if !errors.Is(err, qs.ErrInvalid) {
		t.Fatalf("expected invalid render error, got %v", err)
	}
	if string(outSQL) != "/* prefix */ " || len(outArgs) != 1 || outArgs[0] != "prefix" {
		t.Fatalf("visible prefix changed: SQL=%q args=%#v", outSQL, outArgs)
	}
	if &outArgs[0] != &prefix[0] {
		t.Fatal("failure did not return the original argument slice")
	}
	if storage[1] != nil {
		t.Fatalf("original spare slot retained an appended reference: %T", storage[1])
	}
	several := make([]any, 5)
	several[0] = "prefix"
	severalPrefix := several[:1]
	_, severalArgs, err := qs.Select(qs.Param("one"), qs.Param("two"), qs.Param("three"), qs.Expr{}).AppendSQL(nil, severalPrefix)
	if !errors.Is(err, qs.ErrInvalid) || len(severalArgs) != 1 || several[1] != nil || several[2] != nil || several[3] != nil || several[4] != nil {
		t.Fatalf("several-bind cleanup failed: args=%#v storage=%#v err=%v", severalArgs, several, err)
	}
	limited := make([]any, 4)
	limited[0] = "prefix"
	limitedPrefix := limited[:1]
	_, limitedArgs, err := qs.Select(qs.Param("one"), qs.Param("two"), qs.Param("three")).AppendWith(nil, limitedPrefix, qs.Options{MaxParameters: 2})
	if !errors.Is(err, qs.ErrParameterLimit) || len(limitedArgs) != 1 || limited[1] != nil {
		t.Fatalf("parameter-limit cleanup failed: args=%#v storage=%#v err=%v", limitedArgs, limited, err)
	}

	zeroCapacity := make([]any, 0)
	_, returned, err := qs.Select(qs.Param("value"), qs.Expr{}).AppendSQL(nil, zeroCapacity)
	if !errors.Is(err, qs.ErrInvalid) || len(returned) != 0 || returned == nil {
		t.Fatalf("zero-capacity failure returned args=%#v err=%v", returned, err)
	}
}

func TestCorrectnessOperatorValidationMatchesPostgreSQLSpelling(t *testing.T) {
	t.Parallel()
	left, right := qs.LiteralInt(6), qs.LiteralInt(2)
	for _, operator := range []string{"*-", "++", "=-", "=>", "--", "/*", strings.Repeat("*", 64)} {
		t.Run(operator, func(t *testing.T) {
			t.Parallel()
			for _, expression := range []qs.Expr{
				qs.Operator(left, operator, right),
				qs.PrefixOperator(operator, right),
				qs.QualifiedOperator(left, "custom", operator, right),
			} {
				if _, _, err := qs.Select(expression).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
					t.Errorf("operator %q was accepted: %v", operator, err)
				}
			}
			if _, _, err := qs.Select(left).OrderBy(left.Using(operator)).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
				t.Errorf("ORDER BY operator %q was accepted: %v", operator, err)
			}
		})
	}
	for _, operator := range []string{"+", "-", "@-", "?-", "`+", "%-", "!=", "<>", strings.Repeat("*", 63), "*=>*"} {
		for _, expression := range []qs.Expr{
			qs.Operator(left, operator, right),
			qs.PrefixOperator(operator, right),
			qs.QualifiedOperator(left, "custom", operator, right),
		} {
			if _, _, err := qs.Select(expression).ToSQL(); err != nil {
				t.Errorf("valid operator %q rejected: %v", operator, err)
			}
		}
		if _, _, err := qs.Select(left).OrderBy(left.Using(operator)).ToSQL(); err != nil {
			t.Errorf("valid ORDER BY operator %q rejected: %v", operator, err)
		}
	}
	if got, args, err := qs.Select(qs.Call("f", qs.NamedArg("named", qs.Param(1)))).ToSQL(); err != nil || got != `SELECT "f"("named" => $1)` || len(args) != 1 || args[0] != 1 {
		t.Fatalf("NamedArg rendering changed: SQL=%q args=%#v err=%v", got, args, err)
	}
}

func TestCorrectnessDedicatedSyntaxVersionGates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		query   qs.Statement
		before  qs.PostgreSQLVersion
		support qs.PostgreSQLVersion
	}{
		{"normalize", qs.Select(qs.Normalize(qs.Param("x"))), qs.PostgreSQL12, qs.PostgreSQL13},
		{"is_normalized", qs.Select(qs.IsNormalized(qs.Param("x")).Expr()), qs.PostgreSQL12, qs.PostgreSQL13},
		{"is_not_normalized", qs.Select(qs.IsNotNormalized(qs.Param("x")).Expr()), qs.PostgreSQL12, qs.PostgreSQL13},
		{"substring_similar", qs.Select(qs.SubstringSimilar(qs.Param("x"), qs.Param("%x%"), qs.Param("\\"))), qs.PostgreSQL13, qs.PostgreSQL14},
		{"at_local", qs.Select(qs.AtLocal(qs.Param("x"))), qs.PostgreSQL16, qs.PostgreSQL17},
		{"at_local_method", qs.Select(qs.Param("x").AtLocal()), qs.PostgreSQL16, qs.PostgreSQL17},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if sql, args, err := qs.ToSQLWith(tc.query, qs.Options{PostgreSQL: tc.before}); !errors.Is(err, qs.ErrUnsupported) || sql != "" || args != nil {
				t.Fatalf("unsupported syntax rendered: sql=%q args=%#v err=%v", sql, args, err)
			}
			if _, _, err := qs.ToSQLWith(tc.query, qs.Options{PostgreSQL: tc.support}); err != nil {
				t.Fatalf("supported syntax rejected: %v", err)
			}
		})
	}
	for _, tc := range tests {
		for version := qs.PostgreSQL12; version <= qs.PostgreSQL18; version++ {
			_, _, err := qs.ToSQLWith(tc.query, qs.Options{PostgreSQL: version})
			if version < tc.support && !errors.Is(err, qs.ErrUnsupported) {
				t.Fatalf("%s accepted on PostgreSQL %d: %v", tc.name, version, err)
			}
			if version >= tc.support && err != nil {
				t.Fatalf("%s rejected on PostgreSQL %d: %v", tc.name, version, err)
			}
		}
	}
	for _, query := range []qs.Statement{
		qs.Select(qs.SubstringFrom(qs.Param("x"), qs.Param(1))),
		qs.Select(qs.SubstringFor(qs.Param("x"), qs.Param(1))),
	} {
		if _, _, err := qs.ToSQLWith(query, qs.Options{PostgreSQL: qs.PostgreSQL12}); err != nil {
			t.Fatalf("older substring form rejected on PostgreSQL 12: %v", err)
		}
	}
}

func TestCorrectnessScalarComparisonsBindUntypedNil(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		make func(string, any) qs.Condition
		op   string
	}{
		{"eq", qs.Eq, "="},
		{"ne", qs.Ne, "<>"},
		{"lt", qs.Lt, "<"},
		{"lte", qs.Lte, "<="},
		{"gt", qs.Gt, ">"},
		{"gte", qs.Gte, ">="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := qs.Select(tc.make("value", nil).Expr()).ToSQL()
			want := `SELECT ("value" ` + tc.op + ` $1)`
			if err != nil || sql != want || len(args) != 1 || args[0] != nil {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q and one nil argument", sql, args, err, want)
			}
		})
	}
	if sql, args, err := qs.Select(qs.Eq("value", int64(1)).Expr()).ToSQL(); err != nil || len(args) != 1 {
		t.Fatalf("typed dynamic comparison failed: SQL=%q args=%#v err=%v", sql, args, err)
	} else if got, ok := args[0].(int64); !ok || got != 1 {
		t.Fatalf("comparison changed dynamic type: %#v", args[0])
	}
}
