//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
)

func TestPostgreSQLLiteralPrefix(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qs_pattern_prefix (id integer PRIMARY KEY, value text);
		INSERT INTO qs_pattern_prefix (id, value) VALUES
			(1, 'a!%_suffix'),
			(2, 'aX_suffix'),
			(3, 'a%b'),
			(4, 'a_b'),
			(5, 'john'),
			(6, 'JOHN'),
			(7, 'Jöhn'),
			(8, ''),
			(9, NULL)`); err != nil {
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
		{"empty_prefix", qs.ILikePrefix("value", ""), []string{"a!%_suffix", "aX_suffix", "a%b", "a_b", "john", "JOHN", "Jöhn", ""}},
	} {
		query := qs.Select(qs.Col("value")).From("qs_pattern_prefix").
			Where(tc.condition).OrderBy(qs.Asc("id"))
		got := queryStrings(t, ctx, conn, query)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.name, got, tc.want)
		}
	}

	nullQuery := qs.Select(qs.ILikePrefix("value", "").Expr().IsNull().Expr()).
		From("qs_pattern_prefix").Where(qs.IsNull("value"))
	sql, args, err := nullQuery.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var isNull bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&isNull); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if !isNull {
		t.Fatal("literal prefix predicate on NULL should be UNKNOWN")
	}
}
