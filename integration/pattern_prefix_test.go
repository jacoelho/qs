//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
)

func TestPostgreSQLLiteralPatterns(t *testing.T) {
	t.Parallel()
	session := connect(t)
	ctx, conn := session.Context, session.Conn
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qs_pattern_prefix (id integer PRIMARY KEY, value text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO qs_pattern_prefix (id, value) VALUES
		($1, $2), ($3, $4), ($5, $6), ($7, $8), ($9, $10), ($11, $12), ($13, $14),
		($15, $16), ($17, $18), ($19, $20), ($21, $22), ($23, $24), ($25, $26), ($27, $28), ($29, $30)`,
		1, "a!%_suffix",
		2, "aX_suffix",
		3, "a%b",
		4, "a_b",
		5, "john",
		6, "JOHN",
		7, "Jöhn",
		8, "",
		9, nil,
		10, "ends!%_",
		11, "middle!%_tail",
		12, `beforeÖ\!%_tail`,
		13, "xJOHN!%_tail",
		14, `beforeÖ\!%_`,
		15, "ends!XY",
	); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		condition qs.Condition
		want      []string
	}{
		{"literal_escape_percent_underscore", qs.LikePrefix("value", "a!%_"), []string{"a!%_suffix"}},
		{"literal_percent", qs.ILikePrefix("value", "a%"), []string{"a%b"}},
		{"literal_underscore", qs.LikePrefix("value", "a_"), []string{"a_b"}},
		{"ascii_case_sensitive", qs.LikePrefix("value", "john"), []string{"john"}},
		{"ascii_case_insensitive", qs.ILikePrefix("value", "john"), []string{"john", "JOHN"}},
		{"unicode", qs.LikePrefix("value", "Jö"), []string{"Jöhn"}},
		{"empty_prefix", qs.ILikePrefix("value", ""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"literal_suffix", qs.LikeSuffix("value", "!%_"), []string{"ends!%_", `beforeÖ\!%_`}},
		{"literal_contains", qs.LikeContains("value", "!%_"), []string{"a!%_suffix", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`}},
		{"unicode_backslash_contains", qs.LikeContains("value", `Ö\!%_`), []string{`beforeÖ\!%_tail`, `beforeÖ\!%_`}},
		{"unicode_backslash_suffix", qs.LikeSuffix("value", `Ö\!%_`), []string{`beforeÖ\!%_`}},
		{"empty_suffix", qs.LikeSuffix("value", ""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"empty_contains", qs.ILikeContains("value", ""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"ascii_case_insensitive_suffix", qs.ILikeSuffix("value", "!%_TAIL"), []string{"middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail"}},
	} {
		query := qs.Select(qs.Col("value")).From("qs_pattern_prefix").
			Where(tc.condition).OrderBy(qs.Asc("id"))
		got := queryStrings(t, session, query)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}

	nullQuery := qs.Select(
		qs.ILikePrefix("value", "").Expr().IsNull().Expr(),
		qs.LikeSuffix("value", "").Expr().IsNull().Expr(),
		qs.ILikeContains("value", "").Expr().IsNull().Expr(),
		qs.ILikeContains("value", "x").Not().Expr().IsNull().Expr(),
	).
		From("qs_pattern_prefix").Where(qs.IsNull("value"))
	sql, args, err := session.render(t, nullQuery)
	if err != nil {
		t.Fatal(err)
	}
	var isNull, suffixIsNull, containsIsNull, negatedIsNull bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&isNull, &suffixIsNull, &containsIsNull, &negatedIsNull); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if !isNull || !suffixIsNull || !containsIsNull {
		t.Fatal("literal predicates on NULL should be UNKNOWN")
	}
	if !negatedIsNull {
		t.Fatal("negated literal contains predicate on NULL should be UNKNOWN")
	}
}
