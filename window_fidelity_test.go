package qs

import (
	"errors"
	"testing"
)

func TestUnusedWindowAllowsRowLocking(t *testing.T) {
	t.Parallel()
	query := SelectCols("id").
		From("jobs").
		Window("unused", Window().OrderBy(Col("id").Asc())).
		Lock(ForUpdate())
	checkSQL(t, query, `SELECT "id" FROM "jobs" WINDOW "unused" AS (ORDER BY "id" ASC) FOR UPDATE`)
}

func TestWindowUseRejectsRowLocking(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		stmt Statement
	}{
		{"inline", Select(RowNumber().Over(Window())).From("jobs").Lock(ForUpdate())},
		{
			"named_alias",
			Select(RowNumber().OverNamed("w").As("position")).
				From("jobs").
				Window("w", Window()).
				Lock(ForUpdate()),
		},
		{
			"select_order",
			SelectCols("id").
				From("jobs").
				OrderBy(RowNumber().Over(Window()).Asc()).
				Lock(ForUpdate()),
		},
		{
			"table_order",
			TableRows("jobs").OrderBy(RowNumber().Over(Window()).Asc()).Lock(ForUpdate()),
		},
		{
			"outer_use_survives_child",
			Select(RowNumber().Over(Window()), Scalar(Select(Param(7)))).
				From("jobs").
				Lock(ForUpdate()),
		},
		{
			"nested_lock",
			Select(Scalar(Select(RowNumber().Over(Window())).From("jobs").Lock(ForUpdate()))),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.stmt, ErrInvalid)
		})
	}
}

func TestNestedWindowUseDoesNotRejectOuterRowLock(t *testing.T) {
	t.Parallel()

	checkSQL(
		t,
		Select(Col("id"), Scalar(Select(RowNumber().Over(Window())))).From("jobs").Lock(ForUpdate()),
		`SELECT "id", (SELECT row_number() OVER ()) FROM "jobs" FOR UPDATE`,
	)
	ranked := CTE("ranked", Select(RowNumber().Over(Window()).As("position")))
	checkSQL(
		t,
		SelectCols("id").
			With(ranked).
			From("jobs").
			Where(Exists(SelectCols("position").FromExpr(ranked.Ref()))).
			Lock(ForUpdate()),
		`WITH "ranked" AS (SELECT row_number() OVER () AS "position") SELECT "id" FROM "jobs" WHERE EXISTS (SELECT "position" FROM "ranked") FOR UPDATE`,
	)
}

func TestOrderWindowLockFailurePreservesAppendPrefixes(t *testing.T) {
	t.Parallel()

	query := Select(Param("rendered before failure")).
		From("jobs").
		OrderBy(RowNumber().Over(Window()).Asc()).
		Lock(ForUpdate())
	sql, args, err := query.AppendSQL([]byte("prefix "), []any{"existing"})
	if !errors.Is(err, ErrInvalid) || string(sql) != "prefix " || len(args) != 1 || args[0] != "existing" {
		t.Fatalf("window lock rollback: sql=%q args=%#v err=%v", sql, args, err)
	}
}

func TestWindowShorthandRendering(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"rows", Select(Sum(Col("v")).Over(Window().OrderBy(Col("id").Asc()).Rows(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "id" ASC ROWS $1 PRECEDING)`, []any{1}},
		{"range", Select(Sum(Col("v")).Over(Window().OrderBy(Col("v").Asc()).Range(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "v" ASC RANGE $1 PRECEDING)`, []any{1}},
		{"groups", Select(Sum(Col("v")).Over(Window().OrderBy(Col("v").Asc()).Groups(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "v" ASC GROUPS $1 PRECEDING)`, []any{1}},
		{"rows_current", Select(Sum(Col("v")).Over(Window().Rows(CurrentRow()))), `SELECT sum("v") OVER (ROWS CURRENT ROW)`, nil},
		{
			"range_between",
			Select(
				Sum(Col("v")).Over(
					Window().OrderBy(Col("v").Asc()).RangeBetween(Preceding(1), CurrentRow()),
				),
			),
			`SELECT sum("v") OVER (ORDER BY "v" ASC RANGE BETWEEN $1 PRECEDING AND CURRENT ROW)`,
			[]any{1},
		},
	})
}

func TestWindowShorthandBranchingAndTransitions(t *testing.T) {
	t.Parallel()

	base := Window().OrderBy(Col("v").Asc())
	rows := base.Rows(Preceding(1))
	rangeFrame := base.Range(Preceding(1))
	checkSQL(t, Select(Sum(Col("v")).Over(base)), `SELECT sum("v") OVER (ORDER BY "v" ASC)`)
	checkSQL(t, Select(Sum(Col("v")).Over(rows)), `SELECT sum("v") OVER (ORDER BY "v" ASC ROWS $1 PRECEDING)`, 1)
	checkSQL(t, Select(Sum(Col("v")).Over(rangeFrame)), `SELECT sum("v") OVER (ORDER BY "v" ASC RANGE $1 PRECEDING)`, 1)

	between := rows.RowsBetween(UnboundedPreceding(), CurrentRow())
	shorthandAgain := between.Rows(Preceding(2))
	checkSQL(t, Select(Sum(Col("v")).Over(between)), `SELECT sum("v") OVER (ORDER BY "v" ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)`)
	checkSQL(t, Select(Sum(Col("v")).Over(shorthandAgain)), `SELECT sum("v") OVER (ORDER BY "v" ASC ROWS $1 PRECEDING)`, 2)
}

func TestWindowBindOrderNamedAndExclusion(t *testing.T) {
	t.Parallel()

	frame := Window().OrderBy(Col("v").Asc(), Col("id").Asc()).Rows(PrecedingExpr(Param(3))).Exclude(
		ExcludeTies,
	)
	named := Window().OrderBy(Col("v").Asc()).Rows(Preceding(2)).Exclude(ExcludeCurrentRow)
	query := Select(
		Sum(Param(11)).Filter(Col("status").Eq(Param("open"))).Over(frame),
		CountAll().OverNamed("w"),
	).Window("w", named)
	checkSQL(t, query, `SELECT sum($1) FILTER (WHERE ("status" = $2)) OVER (ORDER BY "v" ASC, "id" ASC ROWS $3 PRECEDING EXCLUDE TIES), count(*) OVER "w" WINDOW "w" AS (ORDER BY "v" ASC ROWS $4 PRECEDING EXCLUDE CURRENT ROW)`, 11, "open", 3, 2)

	checkSQL(t, Select(Sum(Col("amount")).Over(frame)).Window("w", frame), `SELECT sum("amount") OVER (ORDER BY "v" ASC, "id" ASC ROWS $1 PRECEDING EXCLUDE TIES) WINDOW "w" AS (ORDER BY "v" ASC, "id" ASC ROWS $2 PRECEDING EXCLUDE TIES)`, 3, 3)
}

func TestWindowInvalidShorthandBounds(t *testing.T) {
	t.Parallel()

	frame := func(spec WindowSpec) Statement { return Select(Sum(Col("v")).Over(spec)) }
	for _, tc := range []struct {
		name string
		stmt Statement
	}{
		{"following_start", frame(Window().Rows(Following(1)))},
		{"unbounded_following_start", frame(Window().Rows(UnboundedFollowing()))},
		{"zero_bound", frame(Window().Rows(Bound{}))},
		{"negative_preceding", frame(Window().Rows(Preceding(-1)))},
		{"negative_following", frame(Window().Rows(Following(-1)))},
		{"groups_without_order", frame(Window().Groups(Preceding(1)))},
		{"range_without_order", frame(Window().Range(Preceding(1)))},
		{
			"range_multiple_orders",
			frame(Window().OrderBy(Col("a").Asc(), Col("b").Asc()).Range(Preceding(1))),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.stmt, ErrInvalid)
		})
	}
}

func TestWindowLocalInheritanceValidation(t *testing.T) {
	t.Parallel()

	base := Window().OrderBy(Col("v").Asc())
	checkSQL(t, Select(RowNumber().OverNamed("server_window")), `SELECT row_number() OVER "server_window"`)
	checkSQL(t,
		Select(Sum(Col("v")).OverNamed("child")).
			Window("base", base).
			Window("child", Window().Base("base")),
		`SELECT sum("v") OVER "child" WINDOW "base" AS (ORDER BY "v" ASC), "child" AS ("base")`,
	)
	checkSQL(t,
		Select(Sum(Col("v")).OverNamed("child")).
			Window("base", Window()).
			Window("child", Window().Base("base").OrderBy(Col("v").Asc())),
		`SELECT sum("v") OVER "child" WINDOW "base" AS (), "child" AS ("base" ORDER BY "v" ASC)`,
	)
	checkSQL(t,
		Select(Sum(Col("v")).OverNamed("child")).
			Window("base", base).
			Window("middle", Window().Base("base")).
			Window("child", Window().Base("middle").Groups(CurrentRow())),
		`SELECT sum("v") OVER "child" WINDOW "base" AS (ORDER BY "v" ASC), "middle" AS ("base"), "child" AS ("middle" GROUPS CURRENT ROW)`,
	)
	checkSQL(t,
		Select(Sum(Col("v")).OverNamed("child")).
			Window("base", base).
			Window("child", Window().Base("base").Range(PrecedingExpr(Param(1)))),
		`SELECT sum("v") OVER "child" WINDOW "base" AS (ORDER BY "v" ASC), "child" AS ("base" RANGE $1 PRECEDING)`, 1,
	)
	checkSQL(t,
		Select(Sum(Col("v")).OverNamed("child")).
			Window("base", Window()).
			Window(
				"child",
				Window().Base("base").OrderBy(Col("v").Asc()).Range(PrecedingExpr(Param(1))),
			),
		`SELECT sum("v") OVER "child" WINDOW "base" AS (), "child" AS ("base" ORDER BY "v" ASC RANGE $1 PRECEDING)`, 1,
	)

	for _, tc := range []struct {
		name string
		stmt Statement
	}{
		{
			"missing_base",
			Select(Sum(Col("v")).OverNamed("child")).Window("child", Window().Base("missing")),
		},
		{
			"later_base",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("child", Window().Base("base")).
				Window("base", Window()),
		},
		{
			"inherited_order",
			Select(Param("earlier"), Sum(Col("v")).OverNamed("child")).
				Window("base", base).
				Window("child", Window().Base("base").OrderBy(Col("id").Asc())),
		},
		{
			"transitive_inherited_order",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", base).
				Window("middle", Window().Base("base")).
				Window("child", Window().Base("middle").OrderBy(Col("id").Asc())),
		},
		{
			"copy_frame",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", Window().Rows(CurrentRow())).
				Window("child", Window().Base("base")),
		},
		{
			"override_partition",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", Window().PartitionBy(Col("group"))).
				Window("child", Window().Base("base").PartitionBy(Col("other"))),
		},
		{
			"groups_without_inherited_order",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", Window()).
				Window("child", Window().Base("base").Groups(CurrentRow())),
		},
		{
			"range_without_inherited_order",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", Window()).
				Window("child", Window().Base("base").Range(PrecedingExpr(Param(1)))),
		},
		{
			"range_with_many_inherited_orders",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", Window().OrderBy(Col("v").Asc(), Col("id").Asc())).
				Window("child", Window().Base("base").Range(PrecedingExpr(Param(1)))),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.stmt, ErrInvalid)
		})
	}
}

func TestWindowExclusionPresenceAndFrameEnum(t *testing.T) {
	t.Parallel()

	checkError(t, Select(Sum(Col("v")).Over(Window().Rows(CurrentRow()).Exclude(FrameExclusion(0)))), ErrInvalid)
	checkError(t, Select(Sum(Col("v")).Over(Window().Rows(CurrentRow()).Exclude(FrameExclusion(99)))), ErrInvalid)
	checkError(t, Select(Sum(Col("v")).Over(Window().Exclude(FrameExclusion(0)))), ErrInvalid)
	checkError(
		t,
		Select(
			Sum(Col("v")).Over(
				WindowSpec{frame: windowFrame(99), start: CurrentRow(), end: CurrentRow()},
			),
		),
		ErrInvalid,
	)
}
