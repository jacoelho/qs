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
		{"literal_escape_percent_underscore", qs.Col("value").LikePrefix("a!%_"), []string{"a!%_suffix"}},
		{"literal_percent", qs.Col("value").ILikePrefix("a%"), []string{"a%b"}},
		{"literal_underscore", qs.Col("value").LikePrefix("a_"), []string{"a_b"}},
		{"ascii_case_sensitive", qs.Col("value").LikePrefix("john"), []string{"john"}},
		{"ascii_case_insensitive", qs.Col("value").ILikePrefix("john"), []string{"john", "JOHN"}},
		{"unicode", qs.Col("value").LikePrefix("Jö"), []string{"Jöhn"}},
		{"empty_prefix", qs.Col("value").ILikePrefix(""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"literal_suffix", qs.Col("value").LikeSuffix("!%_"), []string{"ends!%_", `beforeÖ\!%_`}},
		{"literal_contains", qs.Col("value").LikeContains("!%_"), []string{"a!%_suffix", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`}},
		{"unicode_backslash_contains", qs.Col("value").LikeContains(`Ö\!%_`), []string{`beforeÖ\!%_tail`, `beforeÖ\!%_`}},
		{"unicode_backslash_suffix", qs.Col("value").LikeSuffix(`Ö\!%_`), []string{`beforeÖ\!%_`}},
		{"empty_suffix", qs.Col("value").LikeSuffix(""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"empty_contains", qs.Col("value").ILikeContains(""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", "", "ends!%_", "middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail", `beforeÖ\!%_`, "ends!XY"}},
		{"ascii_case_insensitive_suffix", qs.Col("value").ILikeSuffix("!%_TAIL"), []string{"middle!%_tail", `beforeÖ\!%_tail`, "xJOHN!%_tail"}},
	} {
		query := qs.Select(qs.Col("value")).
			From("qs_pattern_prefix").
			Where(tc.condition).
			OrderBy(qs.Col("id").Asc())
		got := queryStrings(t, session, query)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}

	nullQuery := qs.Select(
		qs.Col("value").ILikePrefix("").Expr().IsNull().Expr(),
		qs.Col("value").LikeSuffix("").Expr().IsNull().Expr(),
		qs.Col("value").ILikeContains("").Expr().IsNull().Expr(),
		qs.Col("value").ILikeContains("x").Not().Expr().IsNull().Expr(),
	).
		From("qs_pattern_prefix").
		Where(qs.Col("value").IsNull())
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
