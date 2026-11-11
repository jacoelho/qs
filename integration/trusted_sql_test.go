//go:build postgres

package integration_test

import (
	"testing"

	"github.com/jacoelho/qs"
)

// PostgreSQL is the oracle for what counts as a placeholder: every trusted text
// qs accepts must leave the statement with exactly the parameters qs bound.
func TestTrustedSQLAddsNoParameters(t *testing.T) {
	session := connect(t)
	for _, text := range []string{
		`'$1'`,
		`'it''s $1'`,
		`E'a\'$1'`,
		`'C:\' || 'x'`,
		`$$ select $1 $$`,
		`$fn$ select $1 $$ still $fn$`,
		`1 /* $1 /* nested $2 */ $3 */`,
		"1 -- $1\n",
		`U&'d\0061t\+000061 $1'`,
		`"col$1" FROM (SELECT 1 AS "col$1") AS t$1`,
	} {
		t.Run(text, func(t *testing.T) {
			sql, args, err := qs.Select(qs.Param(int64(7)).Cast(qs.TypeInt8), qs.UnsafeSQL(text)).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			description, err := session.Prepare(session, "", sql)
			if err != nil {
				t.Fatalf("prepare %s: %v", sql, err)
			}
			if len(description.ParamOIDs) != len(args) {
				t.Fatalf("%s: PostgreSQL sees %d parameters, qs bound %d", sql, len(description.ParamOIDs), len(args))
			}
		})
	}
}

// One shared parameter serves every use, and PostgreSQL executes the statement
// with the single argument.
func TestSharedParamExecutes(t *testing.T) {
	session := connect(t)
	v := qs.SharedParam(int64(21))
	sql, args, err := qs.Select(v.Cast(qs.TypeInt8).Add(v.Cast(qs.TypeInt8))).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := session.QueryRow(session, sql, args...).Scan(&got); err != nil {
		t.Fatalf("%s %v: %v", sql, args, err)
	}
	if len(args) != 1 || got != 42 {
		t.Fatalf("%s: %d arguments, result %d, want 1 and 42", sql, len(args), got)
	}
}
