package qx

import (
	"errors"
	"reflect"
	"testing"
)

func TestQueryUtilityRendering(t *testing.T) {
	t.Parallel()

	selectSource := Select(Col("id")).From("source")
	checkSQL(t, Execute("load"), `EXECUTE "load"`)
	checkError(t, Execute("load", Param(1), Param("ready")), ErrInvalid)
	checkSQL(t, Execute("load", LiteralInt(1), LiteralString("ready")), `EXECUTE "load" (1, E'ready')`)
	checkSQL(t, CreateTableAs("copy", selectSource), `CREATE TABLE "copy" AS SELECT "id" FROM "source"`)
	checkSQL(t, CreateTableAs("copy", Select(LiteralInt(1))).Temporary().OnCommit(OnCommitDrop), `CREATE TEMPORARY TABLE "copy" ON COMMIT DROP AS SELECT 1`)
	checkError(t, CreateTableAs("copy", Select(LiteralInt(1))).OnCommit(OnCommitDrop), ErrInvalid)
	checkError(t, CreateTableAsExecute("copy", Execute("load", Param(1))).Temporary().IfNotExists().Columns("id").Tablespace("fast").WithNoData(), ErrInvalid)
	checkSQL(t, CreateTableAsExecute("copy", Execute("load", LiteralInt(1))).Temporary().IfNotExists().Columns("id").Tablespace("fast").WithNoData(), `CREATE TEMPORARY TABLE IF NOT EXISTS "copy" ("id") TABLESPACE "fast" AS EXECUTE "load" (1) WITH NO DATA`)
	checkSQL(t, MaterializedViewAs("mv", Select(Col("id")).From("source")).IfNotExists().Columns("id").Using("heap").Tablespace("fast").WithNoData(), `CREATE MATERIALIZED VIEW IF NOT EXISTS "mv" ("id") USING "heap" TABLESPACE "fast" AS SELECT "id" FROM "source" WITH NO DATA`)
	checkSQL(t, Explain(MaterializedViewAs("mv", Select(LiteralInt(1)))), `EXPLAIN CREATE MATERIALIZED VIEW "mv" AS SELECT 1`)
	checkSQL(t, Explain(Select(LiteralInt(1))).Analyze(false).Format(ExplainJSON).Costs(true), `EXPLAIN (ANALYZE FALSE, FORMAT JSON, COSTS TRUE) SELECT 1`)
	checkSQL(t, Explain(Select(LiteralInt(1))).Analyze(false).Format(ExplainJSON).Costs(true).Format(ExplainText), `EXPLAIN (ANALYZE FALSE, FORMAT TEXT, COSTS TRUE) SELECT 1`)
	checkSQL(t, DeclareCursor("cur", Select(Star()).From("source")).Binary().Insensitive().Scroll(CursorNoScroll).WithHold(), `DECLARE "cur" BINARY INSENSITIVE NO SCROLL CURSOR WITH HOLD FOR SELECT * FROM "source"`)
	checkSQL(t, Select(Col("id")).From("source").Into("copy").Temporary(), `SELECT "id" INTO TEMPORARY "copy" FROM "source"`)
}

func TestQueryUtilityTransparentCTEAndRestrictions(t *testing.T) {
	t.Parallel()

	modifying := CTE("d", DeleteFrom("source").ReturningCols("id"))
	query := Select(Col("id")).With(modifying).From("d")
	checkSQL(t, CreateTableAs("copy", query), `CREATE TABLE "copy" AS WITH "d" AS (DELETE FROM "source" RETURNING "id") SELECT "id" FROM "d"`)
	checkSQL(t, query.Into("copy"), `WITH "d" AS (DELETE FROM "source" RETURNING "id") SELECT "id" INTO "copy" FROM "d"`)
	checkSQL(t, Explain(CreateTableAs("copy", query)), `EXPLAIN CREATE TABLE "copy" AS WITH "d" AS (DELETE FROM "source" RETURNING "id") SELECT "id" FROM "d"`)
	checkSQL(t, Explain(query.Into("copy")), `EXPLAIN WITH "d" AS (DELETE FROM "source" RETURNING "id") SELECT "id" INTO "copy" FROM "d"`)
	checkError(t, MaterializedViewAs("mv", query), ErrInvalid)
	checkError(t, DeclareCursor("cur", query), ErrInvalid)
	checkError(t, MaterializedViewAs("mv", Select(Param(1))), ErrInvalid)

	for _, locked := range []Rowset{
		Select(Star()).From("source").Lock(ForUpdate()),
		TableRows("source").Lock(ForUpdate()),
	} {
		checkError(t, DeclareCursor("cur", locked).WithHold(), ErrInvalid)
		checkError(t, DeclareCursor("cur", locked).Scroll(CursorScroll), ErrInvalid)
		checkError(t, DeclareCursor("cur", locked).Insensitive(), ErrInvalid)
		if _, _, err := DeclareCursor("cur", locked).Scroll(CursorNoScroll).ToSQL(); err != nil {
			t.Fatalf("NO SCROLL with a locking query should be allowed: %v", err)
		}
	}
}

func TestQueryUtilityDestinationOwnership(t *testing.T) {
	t.Parallel()

	source := Select(Param(1))
	first := source.Into("first").Temporary()
	second := source.Into("second").Unlogged()
	firstSQL, firstArgs, err := first.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	secondSQL, secondArgs, err := second.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if firstSQL != `SELECT $1 INTO TEMPORARY "first"` || secondSQL != `SELECT $1 INTO UNLOGGED "second"` || !reflect.DeepEqual(firstArgs, []any{1}) || !reflect.DeepEqual(secondArgs, []any{1}) {
		t.Fatalf("independent destinations rendered as %q %#v and %q %#v", firstSQL, firstArgs, secondSQL, secondArgs)
	}
	source.Columns(Col("two"))
	if got, _, err := first.ToSQL(); err != nil || got != `SELECT $1, "two" INTO TEMPORARY "first"` {
		t.Fatalf("destination did not retain a live source: %q %v", got, err)
	}
	clone := Clone(first)
	source.Columns(Col("three"))
	if got, _, err := clone.ToSQL(); err != nil || got != `SELECT $1, "two" INTO TEMPORARY "first"` {
		t.Fatalf("clone source changed with original: %q %v", got, err)
	}

	// Existing argument prefixes do not count as external materialized-view
	// parameters; only arguments introduced by the source are rejected.
	buf, args, err := AppendWith(nil, []any{"prefix"}, MaterializedViewAs("mv", Select(Col("id"))), Options{})
	if err != nil || string(buf) != `CREATE MATERIALIZED VIEW "mv" AS SELECT "id"` || !reflect.DeepEqual(args, []any{"prefix"}) {
		t.Fatalf("existing argument prefix changed: %q %#v %v", buf, args, err)
	}
}

func TestQueryUtilityCloneSources(t *testing.T) {
	t.Parallel()

	ctasSource := Select(LiteralInt(1).As("value"))
	ctas := CreateTableAs("copy", ctasSource)
	ctasClone := Clone(ctas)
	mviewSource := Select(LiteralInt(2).As("value"))
	mview := MaterializedViewAs("mv", mviewSource)
	mviewClone := Clone(mview)
	cursorSource := Select(LiteralInt(3).As("value"))
	cursor := DeclareCursor("cur", cursorSource)
	cursorClone := Clone(cursor)

	ctasSource.Columns(LiteralInt(4).As("extra"))
	mviewSource.Columns(LiteralInt(5).As("extra"))
	cursorSource.Columns(LiteralInt(6).As("extra"))
	checkSQL(t, ctasClone, `CREATE TABLE "copy" AS SELECT 1 AS "value"`)
	checkSQL(t, mviewClone, `CREATE MATERIALIZED VIEW "mv" AS SELECT 2 AS "value"`)
	checkSQL(t, cursorClone, `DECLARE "cur" CURSOR FOR SELECT 3 AS "value"`)

	loop := Select(LiteralInt(1))
	loop.With(CTE("loop", loop)).From("loop")
	checkErrorWithOptions(t, ctasForDepth(loop), Options{MaxDepth: 2}, ErrDepth)
	checkErrorWithOptions(t, loop.Into("copy"), Options{MaxDepth: 2}, ErrDepth)
	into := Select(LiteralInt(1)).Into("copy")
	checkErrorWithOptions(t, into, Options{MaxDepth: 2}, ErrDepth)
	if sql, args, err := into.ToSQLWith(Options{MaxDepth: 3}); err != nil || sql != `SELECT 1 INTO "copy"` || args != nil {
		t.Fatalf("SELECT INTO at depth boundary: %q %#v %v", sql, args, err)
	}
}

func ctasForDepth(query Rowset) Statement { return CreateTableAs("copy", query) }

func checkErrorWithOptions(t *testing.T, statement Statement, options Options, cause error) {
	t.Helper()
	sql, args, err := ToSQLWith(statement, options)
	if !errors.Is(err, cause) || sql != "" || args != nil {
		t.Fatalf("expected atomic %v error, got %q %#v %v", cause, sql, args, err)
	}
}

func TestQueryUtilityValidation(t *testing.T) {
	t.Parallel()

	for name, query := range map[string]Statement{
		"nil execute":           (*ExecuteBuilder)(nil),
		"nil ctas":              (*CreateTableAsBuilder)(nil),
		"nil materialized view": (*MaterializedViewBuilder)(nil),
		"nil cursor":            (*DeclareCursorBuilder)(nil),
		"nil select into":       (*SelectIntoBuilder)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, query, ErrInvalid)
		})
	}
	checkError(t, Execute(""), ErrInvalid)
	checkError(t, CreateTableAs("", Select(Param(1))), ErrInvalid)
	checkError(t, MaterializedViewAs("mv", Select(Param(1))), ErrInvalid)
	checkError(t, DeclareCursor("cur", Select(Param(1))).Scroll(CursorScrollMode(99)), ErrInvalid)
	checkError(t, Select(Param(1)).Into(""), ErrInvalid)
}
