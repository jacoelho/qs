package qs

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

	q := Select(Param("value")).Where(Col("id").Eq(Param(42)))
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
	if _, _, err := q.Columns(Param("after")).ToSQL(); err != nil {
		t.Fatal(err)
	}
	if text != "SELECT $1" || !reflect.DeepEqual(args, []any{"before"}) {
		t.Fatal("ToSQL output aliases reusable storage")
	}
	data := []byte("old")
	q = Select(Param(data))
	_, args, _ = q.ToSQL()
	data[0] = 'n'
	bound, ok := args[0].([]byte)
	if !ok {
		t.Fatalf("bound value has type %T, want []byte", args[0])
	}
	if bound[0] != 'n' {
		t.Fatal("bound application objects should not be silently copied")
	}
}

func TestDerivedStatementsLeaveTheirSourceUnchanged(t *testing.T) {
	t.Parallel()

	child := Select(Param("child"))
	parent := Select(Scalar(child)).With(CTE("c", child)).FromExpr(child.As("s"))
	const parentSQL = `WITH "c" AS (SELECT $1) SELECT (SELECT $2) FROM (SELECT $3) AS "s"`

	widened := child.Columns(Param("later"))
	checkSQL(t, widened, `SELECT $1, $2`, "child", "later")
	checkSQL(t, child, `SELECT $1`, "child")
	checkSQL(t, parent, parentSQL, "child", "child", "child")

	filtered := parent.Where(True())
	checkSQL(t, filtered, parentSQL+` WHERE TRUE`, "child", "child", "child")
	checkSQL(t, parent, parentSQL, "child", "child", "child")
}

// Two statements derived from one base must not write into storage the base
// still shares with them: the base is built by repeated appends, so its lists
// have spare capacity that an unclipped append would hand to both siblings.
func TestSiblingBranchesDoNotShareAppendedItems(t *testing.T) {
	t.Parallel()

	grown := func(n int, add func(i int)) {
		for i := range n {
			add(i)
		}
	}
	type branch struct {
		name        string
		base        Statement
		left, right Statement
		baseSQL     string
		leftSQL     string
		rightSQL    string
	}
	var cases []branch

	selectBase := Select(Col("c0")).From("t")
	grown(3, func(i int) {
		selectBase = selectBase.Columns(Col("c")).Where(Col("w").IsNull()).GroupBy("g").
			Having(Col("h").IsNull()).OrderBy(Col("o").Asc())
	})
	const selectSQL = `SELECT "c0", "c", "c", "c" FROM "t" WHERE ("w" IS NULL) AND ("w" IS NULL) AND ("w" IS NULL) GROUP BY "g", "g", "g" HAVING ("h" IS NULL) AND ("h" IS NULL) AND ("h" IS NULL) ORDER BY "o" ASC, "o" ASC, "o" ASC`
	selectTail := func(columns, where, group, having, order string) string {
		return `SELECT "c0", "c", "c", "c"` + columns + ` FROM "t" WHERE ("w" IS NULL) AND ("w" IS NULL) AND ("w" IS NULL)` + where +
			` GROUP BY "g", "g", "g"` + group + ` HAVING ("h" IS NULL) AND ("h" IS NULL) AND ("h" IS NULL)` + having +
			` ORDER BY "o" ASC, "o" ASC, "o" ASC` + order
	}
	cases = append(cases,
		branch{"select_columns", selectBase, selectBase.Columns(Col("l")), selectBase.Columns(Col("r")), selectSQL, selectTail(`, "l"`, "", "", "", ""), selectTail(`, "r"`, "", "", "", "")},
		branch{"select_where", selectBase, selectBase.Where(Col("l").IsNull()), selectBase.Where(Col("r").IsNull()), selectSQL, selectTail("", ` AND ("l" IS NULL)`, "", "", ""), selectTail("", ` AND ("r" IS NULL)`, "", "", "")},
		branch{"select_group", selectBase, selectBase.GroupBy("l"), selectBase.GroupBy("r"), selectSQL, selectTail("", "", `, "l"`, "", ""), selectTail("", "", `, "r"`, "", "")},
		branch{"select_having", selectBase, selectBase.Having(Col("l").IsNull()), selectBase.Having(Col("r").IsNull()), selectSQL, selectTail("", "", "", ` AND ("l" IS NULL)`, ""), selectTail("", "", "", ` AND ("r" IS NULL)`, "")},
		branch{"select_order", selectBase, selectBase.OrderBy(Col("l").Asc()), selectBase.OrderBy(Col("r").Asc()), selectSQL, selectTail("", "", "", "", `, "l" ASC`), selectTail("", "", "", "", `, "r" ASC`)},
	)

	joined := Select(Star()).From("a", "b", "c")
	cases = append(cases, branch{"select_join", joined, joined.Join("l", True()), joined.Join("r", True()),
		`SELECT * FROM "a", "b", "c"`,
		`SELECT * FROM "a", "b", ("c" JOIN "l" ON TRUE)`,
		`SELECT * FROM "a", "b", ("c" JOIN "r" ON TRUE)`})

	updatePending := Update("t").Set(Set("a0", Write(LiteralInt(0)))).From("f0")
	grown(3, func(i int) {
		updatePending = updatePending.Set(Set([]string{"a1", "a2", "a3"}[i], Write(LiteralInt(1)))).From("f")
	})
	const updateSet, updateFrom = `UPDATE "t" SET "a0" = 0, "a1" = 1, "a2" = 1, "a3" = 1`, ` FROM "f0", "f", "f", "f"`
	cases = append(cases,
		branch{"update_set", updatePending.AllRows(), updatePending.Set(Set("l", Write(LiteralInt(2)))).AllRows(), updatePending.Set(Set("r", Write(LiteralInt(3)))).AllRows(),
			updateSet + updateFrom, updateSet + `, "l" = 2` + updateFrom, updateSet + `, "r" = 3` + updateFrom},
		branch{"update_from", updatePending.AllRows(), updatePending.From("l").AllRows(), updatePending.From("r").AllRows(),
			updateSet + updateFrom, updateSet + updateFrom + `, "l"`, updateSet + updateFrom + `, "r"`},
	)
	updateBase := updatePending.AllRows().Returning(Col("r0"))
	grown(3, func(i int) { updateBase = updateBase.Returning(Col("x")) })
	const updateSQL = updateSet + updateFrom + ` RETURNING "r0", "x", "x", "x"`
	cases = append(cases, branch{"update_returning", updateBase, updateBase.Returning(Col("l")), updateBase.Returning(Col("r")),
		updateSQL, updateSQL + `, "l"`, updateSQL + `, "r"`})

	deletePending := DeleteFrom("t").Using("u0")
	grown(3, func(i int) { deletePending = deletePending.Using("u") })
	const deleteUsing = `DELETE FROM "t" USING "u0", "u", "u", "u"`
	cases = append(cases, branch{"delete_using", deletePending.AllRows(), deletePending.Using("l").AllRows(), deletePending.Using("r").AllRows(),
		deleteUsing, deleteUsing + `, "l"`, deleteUsing + `, "r"`})
	deleteBase := deletePending.AllRows().Returning(Col("r0"))
	grown(3, func(i int) { deleteBase = deleteBase.Returning(Col("x")) })
	const deleteSQL = deleteUsing + ` RETURNING "r0", "x", "x", "x"`
	cases = append(cases, branch{"delete_returning", deleteBase, deleteBase.Returning(Col("l")), deleteBase.Returning(Col("r")),
		deleteSQL, deleteSQL + `, "l"`, deleteSQL + `, "r"`})

	insertBase := InsertInto("t").Values(Write(LiteralInt(0)))
	grown(3, func(i int) { insertBase = insertBase.Values(Write(LiteralInt(1))) })
	cases = append(cases, branch{"insert_rows", insertBase, insertBase.Values(Write(LiteralInt(2))), insertBase.Values(Write(LiteralInt(3))),
		`INSERT INTO "t" VALUES (0), (1), (1), (1)`,
		`INSERT INTO "t" VALUES (0), (1), (1), (1), (2)`,
		`INSERT INTO "t" VALUES (0), (1), (1), (1), (3)`})

	assignBase := InsertInto("t").Set(Set("a0", Write(LiteralInt(0))))
	grown(3, func(i int) { assignBase = assignBase.Set(Set([]string{"a1", "a2", "a3"}[i], Write(LiteralInt(1)))) })
	cases = append(cases, branch{"insert_assignments", assignBase, assignBase.Set(Set("l", Write(LiteralInt(2)))), assignBase.Set(Set("r", Write(LiteralInt(3)))),
		`INSERT INTO "t" ("a0", "a1", "a2", "a3") VALUES (0, 1, 1, 1)`,
		`INSERT INTO "t" ("a0", "a1", "a2", "a3", "l") VALUES (0, 1, 1, 1, 2)`,
		`INSERT INTO "t" ("a0", "a1", "a2", "a3", "r") VALUES (0, 1, 1, 1, 3)`})

	valuesBase := ValuesExpr(LiteralInt(0))
	grown(3, func(i int) { valuesBase = valuesBase.RowExpr(LiteralInt(1)) })
	cases = append(cases, branch{"values_rows", valuesBase, valuesBase.RowExpr(LiteralInt(2)), valuesBase.RowExpr(LiteralInt(3)),
		`VALUES (0), (1), (1), (1)`, `VALUES (0), (1), (1), (1), (2)`, `VALUES (0), (1), (1), (1), (3)`})

	cteBase := Select(LiteralInt(0))
	grown(3, func(i int) { cteBase = cteBase.With(CTE([]string{"a", "b", "c"}[i], Select(LiteralInt(1)))) })
	const cteSQL = `WITH "a" AS (SELECT 1), "b" AS (SELECT 1), "c" AS (SELECT 1)`
	cases = append(cases, branch{"with", cteBase, cteBase.With(CTE("l", Select(LiteralInt(2)))), cteBase.With(CTE("r", Select(LiteralInt(3)))),
		cteSQL + ` SELECT 0`, cteSQL + `, "l" AS (SELECT 2) SELECT 0`, cteSQL + `, "r" AS (SELECT 3) SELECT 0`})

	mergeBase := MergeInto("t").Using(Table("s")).On(True())
	grown(3, func(i int) { mergeBase = mergeBase.When(Matched().And(Col("m").IsNull()).ThenDoNothing()) })
	const mergeSQL = `MERGE INTO "t" USING "s" ON TRUE WHEN MATCHED AND ("m" IS NULL) THEN DO NOTHING WHEN MATCHED AND ("m" IS NULL) THEN DO NOTHING WHEN MATCHED AND ("m" IS NULL) THEN DO NOTHING`
	cases = append(cases, branch{"merge_when", mergeBase, mergeBase.When(Matched().ThenDelete()), mergeBase.When(NotMatched().ThenDoNothing()), mergeSQL,
		mergeSQL + ` WHEN MATCHED THEN DELETE`, mergeSQL + ` WHEN NOT MATCHED THEN DO NOTHING`})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, tc.left, tc.leftSQL)
			checkSQL(t, tc.right, tc.rightSQL)
			checkSQL(t, tc.base, tc.baseSQL)
		})
	}
}

func TestDepthLimit(t *testing.T) {
	t.Parallel()

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
	// A statement can only contain statements built before it, so nesting is
	// the one way to reach the limit through queries.
	nested := Select(LiteralInt(1))
	for range 100 {
		nested = Select(Star()).FromExpr(nested.As("q"))
	}
	checkErrorWithOptions(t, nested, Options{MaxDepth: 50}, ErrDepth)
	if _, _, err := ToSQLWith(nested, Options{MaxDepth: 1000}); err != nil {
		t.Fatal(err)
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
	parameters := make([]Expr, 65536)
	for i := range parameters {
		parameters[i] = Param(i)
	}
	// IN avoids the server's separate result-column limit; this is a rendering test.
	sql, params, err := Select(LiteralInt(1)).Where(Col("id").In(parameters[:65535]...)).ToSQL()
	if err != nil || len(params) != 65535 || !strings.Contains(sql, "$65535)") {
		t.Fatalf("maximum: %d %v", len(params), err)
	}
	q = Select(LiteralInt(1)).Where(Col("id").In(parameters...))
	checkError(t, q, ErrParameterLimit)
}
func TestOptions(t *testing.T) {
	t.Parallel()

	for _, o := range []Options{{MaxParameters: -1}, {MaxParameters: 65536}, {MaxDepth: -1}, {MaxDepth: 4097}} {
		sql, args, err := ToSQLWith(Select(Param(1)), o)
		if !errors.Is(err, ErrInvalid) || sql != "" || args != nil {
			t.Fatalf("invalid options %#v accepted", o)
		}
	}
	_, _, err := AppendWith(nil, []any{1, 2}, Select(Param(1)), Options{MaxParameters: 1})
	if !errors.Is(err, ErrParameterLimit) {
		t.Fatal(err)
	}
}
func TestExpressionsSnapshotSlices(t *testing.T) {
	t.Parallel()

	fields := []Expr{Param(1), Param(2)}
	expr := Array(fields...)
	fields[0] = Param(99)
	conditions := []Condition{Col("a").Eq(Param(1)), Col("b").Eq(Param(2))}
	c := And(conditions...)
	conditions[0] = True()
	checkSQL(t, Select(expr).Where(c), `SELECT ARRAY[$1, $2] WHERE (("a" = $3) AND ("b" = $4))`, 1, 2, 1, 2)
	cb := Case().When(True(), Param(1))
	e := cb.End()
	withElse := cb.Else(Param(2)).End()
	checkSQL(t, Select(e), `SELECT CASE WHEN TRUE THEN $1 END`, 1)
	checkSQL(t, Select(withElse), `SELECT CASE WHEN TRUE THEN $1 ELSE $2 END`, 1, 2)
	base := CountAll()
	filtered := base.Filter(Col("a").Eq(Param(1)))
	checkSQL(t, Select(base, filtered), `SELECT count(*), count(*) FILTER (WHERE ("a" = $1))`, 1)
}

// Goroutines share one base statement, and each derives and renders its own
// branch. Run under the race detector, this fails if deriving a statement
// writes to anything its source still holds.
func TestConcurrentBranchesFromSharedBase(t *testing.T) {
	t.Parallel()

	base := SelectCols("id").From("users").Where(Col("active").Eq(Param(true)))
	for range 3 {
		base = base.Where(Col("deleted_at").IsNull()).OrderBy(Col("id").Asc())
	}
	baseSQL, baseArgs, err := base.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for i := range 50 {
				tenant := worker*1000 + i
				sql, args, err := base.Where(Col("tenant").Eq(Param(tenant))).OrderBy(Col("name").Asc()).Limit(i).ToSQL()
				if err != nil || len(args) != 3 || args[0] != any(true) || args[1] != tenant || args[2] != i || !strings.Contains(sql, `("tenant" = $2)`) {
					t.Errorf("branch %d: %s %#v %v", tenant, sql, args, err)
				}
				got, gotArgs, err := base.ToSQL()
				if err != nil || got != baseSQL || !reflect.DeepEqual(gotArgs, baseArgs) {
					t.Errorf("base changed: %s %#v %v", got, gotArgs, err)
				}
			}
		})
	}
	wg.Wait()
}
func TestIdentifierAndValueBoundaries(t *testing.T) {
	t.Parallel()

	attack := `x"; DROP TABLE users; --`
	checkSQL(t, Select(Col(attack), Col("literal.dot"), Col("schema", "table", "column")).From("users").Where(Col("name").Eq(Param(attack))), `SELECT "x""; DROP TABLE users; --", "literal.dot", "schema"."table"."column" FROM "users" WHERE ("name" = $1)`, attack)
	checkSQL(t, Select(LiteralString("a'\\b")), `SELECT E'a''\\b'`)
	checkSQL(t, InsertInto("t").Columns("a.b").Values(Write(Param(7))).
		OnConflict(ConflictIndex(IndexColumn("a.b")).DoNothing()),
		`INSERT INTO "t" ("a.b") VALUES ($1) ON CONFLICT ("a.b") DO NOTHING`, 7)
}
func TestReusableAppendAllocations(t *testing.T) {
	q := SelectCols("id", "name").From("users").Where(Col("active").Eq(Param(true)), Col("id").In(Param(1), Param(2), Param(3))).OrderBy(Col("id").Asc()).Limit(20)
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
