package qs_test

import (
	"errors"
	"reflect"
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
		{"table_ident", qs.Table("settings"), "a.b", `SELECT "settings"."a.b" FROM "settings"`},
		{"table_ident_star", qs.Table("*"), "a.b", `SELECT "*"."a.b" FROM "*"`},
		{"alias", qs.Table("settings").As("s"), "a.b", `SELECT "s"."a.b" FROM "settings" AS "s"`},
		{"schema", qs.Table("app", "settings"), "a.b", `SELECT "app"."settings"."a.b" FROM "app"."settings"`},
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
		{"schema", qs.Table("app", "settings"), `SELECT "app"."settings".* FROM "app"."settings"`},
		{"alias", qs.Table("settings").As("s"), `SELECT "s".* FROM "settings" AS "s"`},
		{"table_ident", qs.Table("settings"), `SELECT "settings".* FROM "settings"`},
	} {
		t.Run("star/"+tc.name, func(t *testing.T) {
			t.Parallel()
			if got, args, err := qs.Select(tc.relation.Star()).FromExpr(tc.relation).ToSQL(); err != nil || got != tc.want || len(args) != 0 {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q", got, args, err, tc.want)
			}
		})
	}

	for _, relation := range []qs.Relation{qs.Table(""), qs.Table("a", "", "b"), qs.Table("a", "\x00"), qs.Table("a", "\xff")} {
		if _, _, err := qs.Select(relation.Col("id")).FromExpr(relation).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("malformed table path accepted: %v", err)
		}
	}
	for _, relation := range []qs.Relation{qs.Table("settings"), qs.Table("settings"), qs.Table("settings").As("s")} {
		for _, column := range []string{"", "bad\x00name", "\xff"} {
			if _, _, err := qs.Select(relation.Col(column)).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
				t.Fatalf("invalid literal column %q accepted: %v", column, err)
			}
		}
	}
	if got, _, err := qs.Select(qs.Col("public", "settings", "a", "b")).ToSQL(); err != nil || got != `SELECT "public"."settings"."a"."b"` {
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
		{
			"in_query",
			qs.Row(qs.Col("a"), qs.Col("b")).InQuery(qs.ValuesExpr(qs.Param(1), qs.Param(2))),
			`SELECT (ROW("a", "b") IN (VALUES ($1, $2)))`,
			[]any{1, 2},
		},
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

func TestCorrectnessMembershipQueryWidthFollowsTheQueryGiven(t *testing.T) {
	t.Parallel()
	child := qs.Select(qs.Col("a"), qs.Col("b")).From("t")
	row := qs.Row(qs.Col("a"), qs.Col("b"))
	if _, _, err := qs.Select(row.InQuery(child).Expr()).ToSQL(); err != nil {
		t.Fatalf("matching width rejected: %v", err)
	}
	if _, _, err := qs.Select(row.InQuery(child.Columns(qs.Col("c"))).Expr()).ToSQL(); !errors.Is(err, qs.ErrInvalid) {
		t.Fatalf("wider query did not produce mismatch: %v", err)
	}
}

func TestCorrectnessAppendFailureClearsOriginalStorage(t *testing.T) {
	t.Parallel()
	storage := make([]any, 2)
	storage[0] = "prefix"
	prefix := storage[:1]
	outSQL, outArgs, err := qs.Select(qs.Param(new(int)), qs.Param("second"), qs.Expr{}).AppendSQL(
		[]byte("/* prefix */ "),
		prefix,
	)
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
	_, severalArgs, err := qs.Select(qs.Param("one"), qs.Param("two"), qs.Param("three"), qs.Expr{}).AppendSQL(
		nil,
		severalPrefix,
	)
	if !errors.Is(err, qs.ErrInvalid) || len(severalArgs) != 1 || several[1] != nil || several[2] != nil || several[3] != nil || several[4] != nil {
		t.Fatalf("several-bind cleanup failed: args=%#v storage=%#v err=%v", severalArgs, several, err)
	}
	limited := make([]any, 4)
	limited[0] = "prefix"
	limitedPrefix := limited[:1]
	_, limitedArgs, err := qs.Select(qs.Param("one"), qs.Param("two"), qs.Param("three")).AppendWith(
		nil,
		limitedPrefix,
		qs.Options{MaxParameters: 2},
	)
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

func TestCorrectnessScalarComparisonsRejectUntypedNil(t *testing.T) {
	t.Parallel()
	var nilSlice []any
	field := qs.TypedCol[any]("value")
	a, b, c, d := qs.TypedCol[any]("a"), qs.TypedCol[any]("b"), qs.TypedCol[any]("c"), qs.TypedCol[any](
		"d",
	)
	for _, tc := range []struct {
		name      string
		condition qs.Condition
	}{
		{"eq", qs.Col("value").Eq(qs.Param(nil))},
		{"ne", qs.Col("value").Ne(qs.Param(nil))},
		{"lt", qs.Col("value").Lt(qs.Param(nil))},
		{"lte", qs.Col("value").Lte(qs.Param(nil))},
		{"gt", qs.Col("value").Gt(qs.Param(nil))},
		{"gte", qs.Col("value").Gte(qs.Param(nil))},
		{"expr_eq", qs.Col("value").Eq(qs.Param(nil))},
		{"expr_gte", qs.Col("value").Gte(qs.Param(nil))},
		{"field_eq", field.Eq(nil)},
		{"in_first", qs.Col("value").In(qs.Param(nil), qs.Param(1))},
		{"in_last", qs.Col("value").In(qs.Param(1), qs.Param(nil))},
		{"in_all", qs.Col("value").In(qs.Param(nil), qs.Param(nil))},
		{"not_in", qs.Col("value").NotIn(qs.Param(1), qs.Param(nil))},
		{"expr_in", qs.Col("value").In(qs.Param(1), qs.Param(nil))},
		{"expr_not_in", qs.Col("value").NotIn(qs.Param(nil))},
		{"field_in", field.In(1, nil)},
		{"field_not_in", field.NotIn(nil, 1)},
		{"between_low", qs.Col("value").Between(qs.Param(nil), qs.Param(1))},
		{"between_high", qs.Col("value").Between(qs.Param(1), qs.Param(nil))},
		{"not_between", qs.Col("value").NotBetween(qs.Param(1), qs.Param(nil))},
		{"field_between", field.Between(nil, 1)},
		{"row_values", qs.Row(qs.Col("a"), qs.Col("b")).Eq(qs.Row(qs.Param(1), qs.Param(nil)))},
		{"left_operand", qs.Param(nil).Eq(qs.Col("value"))},
		{"between_value", qs.Param(nil).Between(qs.Col("a"), qs.Col("b"))},
		{"in_left", qs.Param(nil).In(qs.Param(1))},
		{"in_query_left", qs.Param(nil).InQuery(qs.SelectCols("id").From("t"))},
		{"row_in_left", qs.Row(qs.Param(nil), qs.Col("a")).In(qs.Row(qs.Param(1), qs.Param(2)))},
		{
			"row_in_query_left",
			qs.Row(qs.Param(nil), qs.Col("a")).InQuery(qs.SelectCols("a", "b").From("t")),
		},
		{"nested_row", qs.Col("value").Eq(qs.Row(qs.Row(qs.Param(nil)).Expr(), qs.Param(1)).Expr())},
		{"any_array", qs.Col("value").Eq(qs.AnyArray(qs.Param(nil)))},
		{"like_pattern", qs.Col("value").LikeExpr(qs.Param(nil))},
		{"similar_pattern", qs.Col("value").SimilarTo(qs.Param(nil))},
		{"tuple2", qs.Tuple2(a, b).GtValues(1, nil)},
		{"tuple3", qs.Tuple3(a, b, c).LtValues(1, 2, nil)},
		{"tuple4", qs.Tuple4(a, b, c, d).NeValues(1, 2, 3, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := qs.Select(tc.condition.Expr()).ToSQL()
			if !errors.Is(err, qs.ErrInvalid) || sql != "" || args != nil {
				t.Fatalf("SQL=%q args=%#v err=%v; want atomic ErrInvalid", sql, args, err)
			}
		})
	}
	for _, query := range []qs.SelectBuilder{
		qs.Select(qs.Col("id")).
			From("t").
			OrderBy(qs.Col("t", "a").Asc(), qs.Col("t", "id").Asc()).
			SeekAfter(nil, 1),
		qs.Select(qs.Col("id")).
			From("t").
			OrderBy(qs.Col("t", "a").Asc(), qs.Col("t", "id").Asc()).
			SeekAfter(1, nil),
	} {
		if sql, args, err := query.ToSQL(); !errors.Is(err, qs.ErrInvalid) || sql != "" || args != nil {
			t.Fatalf("SeekAfter nil: SQL=%q args=%#v err=%v; want atomic ErrInvalid", sql, args, err)
		}
	}

	for _, tc := range []struct {
		name      string
		statement qs.Statement
		want      string
		args      []any
	}{
		{"typed_nil_pointer", qs.Select(qs.Col("value").Eq(qs.Param((*int)(nil))).Expr()), `SELECT ("value" = $1)`, []any{(*int)(nil)}},
		{"typed_dynamic_value", qs.Select(qs.Col("value").Eq(qs.Param(int64(1))).Expr()), `SELECT ("value" = $1)`, []any{int64(1)}},
		{"empty_in", qs.Select(qs.Col("value").In().Expr()), `SELECT FALSE`, nil},
		{"empty_in_spread", qs.Select(qs.TypedCol[any]("value").In(nilSlice...).Expr()), `SELECT FALSE`, nil},
		{"param", qs.Select(qs.Param(nil)), `SELECT $1`, []any{nil}},
		{"param_null", qs.Select(qs.ParamNull(qs.Null[int]{})), `SELECT $1`, []any{nil}},
		{"write_value", qs.InsertInto("t").Values(qs.Value(nil)), `INSERT INTO "t" VALUES ($1)`, []any{nil}},
		{"values_row", qs.Values(nil), `VALUES ($1)`, []any{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := tc.statement.ToSQL()
			if err != nil || sql != tc.want || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("SQL=%q args=%#v err=%v; want SQL=%q args=%#v", sql, args, err, tc.want, tc.args)
			}
			for i, arg := range args {
				if reflect.TypeOf(arg) != reflect.TypeOf(tc.args[i]) {
					t.Fatalf("argument %d has type %T, want %T", i+1, arg, tc.args[i])
				}
			}
		})
	}
}
