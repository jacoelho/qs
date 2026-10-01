package qx

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAppendOwnership(t *testing.T) {
	t.Parallel()

	q := Select(Param("value")).Where(Eq("id", 42))
	buf := make([]byte, 0, 1024)
	buf = append(buf, "/* prefix */ "...)
	args := make([]any, 1, 8)
	args[0] = "existing"
	out, got, err := q.AppendSQL(buf, args)
	if err != nil || string(out) != `/* prefix */ SELECT $2 WHERE ("id" = $3)` || !reflect.DeepEqual(got, []any{"existing", "value", 42}) {
		t.Fatalf("%s %#v %v", out, got, err)
	}
	if string(buf) != "/* prefix */ " || args[0] != "existing" {
		t.Fatal("changed prefix")
	}
	// A later invalid expression must discard SQL and args, including bound values.
	bad := Select(Param("first"), Expr{})
	rollbackSQL, rollbackArgs, err := bad.AppendSQL(buf, args)
	if !errors.Is(err, ErrInvalid) || string(rollbackSQL) != "/* prefix */ " || !reflect.DeepEqual(rollbackArgs, []any{"existing"}) {
		t.Fatalf("rollback %s %#v %v", rollbackSQL, rollbackArgs, err)
	}
	if len(rollbackSQL) > 0 && &rollbackSQL[0] != &buf[0] {
		t.Fatal("did not return original byte slice")
	}
	if &rollbackArgs[0] != &args[0] {
		t.Fatal("did not return original args slice")
	}
}
func TestToSQLOwnsOutput(t *testing.T) {
	t.Parallel()

	q := Select(Param("before"))
	text, args, err := q.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	q.Reset()
	q.Columns(Param("after"))
	_, _, _ = q.ToSQL()
	if text != "SELECT $1" || !reflect.DeepEqual(args, []any{"before"}) {
		t.Fatal("ToSQL output aliases reusable storage")
	}
	data := []byte("old")
	q = Select(Param(data))
	_, args, _ = q.ToSQL()
	data[0] = 'n'
	if args[0].([]byte)[0] != 'n' {
		t.Fatal("bound application objects should not be silently copied")
	}
}
func TestCloneIndependentGraph(t *testing.T) {
	t.Parallel()

	child := Select(Param("child"))
	parent := Select(Scalar(child)).With(CTE("c", child)).FromExpr(child.As("s"))
	clone := parent.Clone()
	child.Columns(Param("later"))
	checkError(t, parent, ErrInvalid)
	checkSQL(t, clone, `WITH "c" AS (SELECT $1) SELECT (SELECT $2) FROM (SELECT $3) AS "s"`, "child", "child", "child")
	clone.Where(True())
	if len(parent.where) != 0 {
		t.Fatal("slice shared between branches")
	}
	// Repeated references must still point to one cloned child, not three copies.
	clonedChild := clone.base.with[0].body
	if clone.from[0].value != clonedChild || clone.columns[0].value.(*subqueryExpression).query != clonedChild {
		t.Fatal("graph sharing not preserved")
	}
}
func TestCyclesAndDepth(t *testing.T) {
	t.Parallel()

	q := Select(LiteralInt(1))
	q.Columns(Scalar(q))
	checkError(t, q, ErrInvalid) // known two-column scalar mismatch is detected first.
	q = Select(Star())
	q.FromExpr(q.As("self"))
	checkError(t, q, ErrDepth)
	checkError(t, q.Clone(), ErrDepth)
	r := Select(LiteralInt(1))
	r.With(CTE("cycle", r))
	checkError(t, r, ErrDepth)
	e := Param(1)
	for range 100 {
		e = e.Add(LiteralInt(1))
	}
	_, _, err := ToSQLWith(Select(e), Options{MaxDepth: 20})
	if !errors.Is(err, ErrDepth) {
		t.Fatalf("depth guard: %v", err)
	}
	if _, _, err := ToSQLWith(Select(e), Options{MaxDepth: 200}); err != nil {
		t.Fatal(err)
	}
	var nilStatement Statement
	if Clone(nilStatement) != nil {
		t.Fatal("nil clone")
	}
	if Clone((*SelectBuilder)(nil)) != nil {
		t.Fatal("typed nil clone")
	}
}
func TestParameterLimits(t *testing.T) {
	t.Parallel()

	q := Select(Param(1), Param(2))
	_, _, err := ToSQLWith(q, Options{MaxParameters: 1})
	if !errors.Is(err, ErrParameterLimit) {
		t.Fatalf("limit: %v", err)
	}
	args := make([]any, 65535)
	buf, got, err := q.AppendSQL(nil, args)
	if !errors.Is(err, ErrParameterLimit) || buf != nil || len(got) != 65535 {
		t.Fatal("prefix limit handling")
	}
	max := make([]Expr, 65535)
	for i := range max {
		max[i] = Param(i)
	}
	// IN avoids the server's separate result-column limit; this is a rendering test.
	sql, params, err := Select(LiteralInt(1)).Where(Col("id").InExpr(max...)).ToSQL()
	if err != nil || len(params) != 65535 || !strings.Contains(sql, "$65535)") {
		t.Fatalf("maximum: %d %v", len(params), err)
	}
	q = Select(LiteralInt(1)).Where(Col("id").InExpr(append(max, Param(65535))...))
	checkError(t, q, ErrParameterLimit)
}
func TestOptions(t *testing.T) {
	t.Parallel()

	for _, o := range []Options{{PostgreSQL: PostgreSQLVersion(11)}, {PostgreSQL: PostgreSQLVersion(19)}, {PostgreSQL: PostgreSQLVersion(-1)}, {MaxParameters: -1}, {MaxParameters: 65536}, {MaxDepth: -1}, {MaxDepth: 4097}} {
		sql, args, err := ToSQLWith(Select(Param(1)), o)
		if !errors.Is(err, ErrInvalid) || sql != "" || args != nil {
			t.Fatalf("invalid options %#v accepted", o)
		}
	}
	for _, version := range []PostgreSQLVersion{PostgreSQL12, PostgreSQL13, PostgreSQL14, PostgreSQL15, PostgreSQL16, PostgreSQL17, PostgreSQL18} {
		sql, args, err := Select(Param("version value")).ToSQLWith(Options{PostgreSQL: version})
		if err != nil || sql != "SELECT $1" || len(args) != 1 || args[0] != "version value" {
			t.Fatalf("PostgreSQL %d: SQL=%q args=%#v error=%v", version, sql, args, err)
		}
	}
	sql, args, err := Update("t").Set(Set("a", 1)).Returning(Old("a")).ToSQLWith(Options{})
	if err != nil || sql != `UPDATE "t" SET "a" = $1 RETURNING "old"."a"` || len(args) != 1 || args[0] != 1 {
		t.Fatalf("default version: SQL=%q args=%#v error=%v", sql, args, err)
	}
	_, _, err = AppendWith(nil, []any{1, 2}, Select(Param(1)), Options{MaxParameters: 1})
	if !errors.Is(err, ErrParameterLimit) {
		t.Fatal(err)
	}
}
func TestExpressionsSnapshotSlices(t *testing.T) {
	t.Parallel()

	fields := []Expr{Param(1), Param(2)}
	expr := Array(fields...)
	fields[0] = Param(99)
	conditions := []Condition{Eq("a", 1), Eq("b", 2)}
	c := And(conditions...)
	conditions[0] = True()
	checkSQL(t, Select(expr).Where(c), `SELECT ARRAY[$1, $2] WHERE (("a" = $3) AND ("b" = $4))`, 1, 2, 1, 2)
	cb := Case().When(True(), Param(1))
	e := cb.End()
	cb.Else(Param(2))
	checkSQL(t, Select(e), `SELECT CASE WHEN TRUE THEN $1 END`, 1)
	base := CountAll()
	filtered := base.Filter(Eq("a", 1))
	checkSQL(t, Select(base, filtered), `SELECT count(*), count(*) FILTER (WHERE ("a" = $1))`, 1)
}
func TestReadOnlyConcurrentRender(t *testing.T) {
	t.Parallel()

	q := SelectCols("id").From("users").Where(Eq("active", true)).Limit(10)
	expected, args, err := q.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				got, a, e := q.ToSQL()
				if e != nil || got != expected || !reflect.DeepEqual(a, args) {
					t.Errorf("concurrent render mismatch")
				}
			}
		})
	}
	wg.Wait()
}
func TestIdentifierAndValueBoundaries(t *testing.T) {
	t.Parallel()

	attack := `x"; DROP TABLE users; --`
	checkSQL(t, Select(Ident(attack), Ident("literal.dot"), Col("schema.table.column")).From("users").Where(Eq("name", attack)), `SELECT "x""; DROP TABLE users; --", "literal.dot", "schema"."table"."column" FROM "users" WHERE ("name" = $1)`, attack)
	checkSQL(t, Select(LiteralString("a'\\b")), `SELECT E'a''\\b'`)
	checkSQL(t, InsertInto("t").Columns("a.b").Values(7).
		OnConflict(ConflictIndex(IndexColumn("a.b")).DoNothing()),
		`INSERT INTO "t" ("a.b") VALUES ($1) ON CONFLICT ("a.b") DO NOTHING`, 7)
}
func TestReusableAppendAllocations(t *testing.T) {
	q := SelectCols("id", "name").From("users").Where(Eq("active", true), In("id", 1, 2, 3)).OrderBy(Asc("id")).Limit(20)
	buf := make([]byte, 0, 1024)
	args := make([]any, 0, 16)
	allocs := testing.AllocsPerRun(1000, func() {
		var err error
		buf, args, err = q.AppendSQL(buf[:0], args[:0])
		if err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm AppendSQL allocated %.2f times", allocs)
	}
	if !bytes.HasPrefix(buf, []byte("SELECT")) || len(args) != 5 {
		t.Fatal("benchmark path did not render")
	}
}
