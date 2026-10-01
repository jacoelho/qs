package qx

import (
	"errors"
	"testing"
)

func TestWindowShorthandRendering(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"rows", Select(Sum(Col("v")).Over(Window().OrderBy(Asc("id")).Rows(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "id" ASC ROWS $1 PRECEDING)`, []any{1}},
		{"range", Select(Sum(Col("v")).Over(Window().OrderBy(Asc("v")).Range(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "v" ASC RANGE $1 PRECEDING)`, []any{1}},
		{"groups", Select(Sum(Col("v")).Over(Window().OrderBy(Asc("v")).Groups(Preceding(1)))), `SELECT sum("v") OVER (ORDER BY "v" ASC GROUPS $1 PRECEDING)`, []any{1}},
		{"rows_current", Select(Sum(Col("v")).Over(Window().Rows(CurrentRow()))), `SELECT sum("v") OVER (ROWS CURRENT ROW)`, nil},
		{"range_between", Select(Sum(Col("v")).Over(Window().OrderBy(Asc("v")).RangeBetween(Preceding(1), CurrentRow()))), `SELECT sum("v") OVER (ORDER BY "v" ASC RANGE BETWEEN $1 PRECEDING AND CURRENT ROW)`, []any{1}},
	})
}

func TestWindowShorthandBranchingAndTransitions(t *testing.T) {
	t.Parallel()

	base := Window().OrderBy(Asc("v"))
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

	frame := Window().OrderBy(Asc("v"), Asc("id")).Rows(PrecedingExpr(Param(3))).Exclude(ExcludeTies)
	named := Window().OrderBy(Asc("v")).Rows(Preceding(2)).Exclude(ExcludeCurrentRow)
	query := Select(
		Sum(Param(11)).Filter(Eq("status", "open")).Over(frame),
		CountAll().OverNamed("w"),
	).Window("w", named)
	checkSQL(t, query, `SELECT sum($1) FILTER (WHERE ("status" = $2)) OVER (ORDER BY "v" ASC, "id" ASC ROWS $3 PRECEDING EXCLUDE TIES), count(*) OVER "w" WINDOW "w" AS (ORDER BY "v" ASC ROWS $4 PRECEDING EXCLUDE CURRENT ROW)`, 11, "open", 3, 2)

	original := Select(Sum(Col("amount")).Over(frame)).Window("w", frame)
	cloned := Clone(original)
	cloned.windows[0].spec.order[0].expr.text = "other"
	checkSQL(t, original, `SELECT sum("amount") OVER (ORDER BY "v" ASC, "id" ASC ROWS $1 PRECEDING EXCLUDE TIES) WINDOW "w" AS (ORDER BY "v" ASC, "id" ASC ROWS $2 PRECEDING EXCLUDE TIES)`, 3, 3)
	checkSQL(t, cloned, `SELECT sum("amount") OVER (ORDER BY "v" ASC, "id" ASC ROWS $1 PRECEDING EXCLUDE TIES) WINDOW "w" AS (ORDER BY "other" ASC, "id" ASC ROWS $2 PRECEDING EXCLUDE TIES)`, 3, 3)
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
		{"range_multiple_orders", frame(Window().OrderBy(Asc("a"), Asc("b")).Range(Preceding(1)))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.stmt, ErrInvalid)
		})
	}
}

func TestWindowLocalInheritanceValidation(t *testing.T) {
	t.Parallel()

	base := Window().OrderBy(Asc("v"))
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
			Window("child", Window().Base("base").OrderBy(Asc("v"))),
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
			Window("child", Window().Base("base").OrderBy(Asc("v")).Range(PrecedingExpr(Param(1)))),
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
			Select(Sum(Col("v")).OverNamed("child")).Window("child", Window().Base("base")).Window("base", Window()),
		},
		{
			"inherited_order",
			Select(Param("earlier"), Sum(Col("v")).OverNamed("child")).
				Window("base", base).
				Window("child", Window().Base("base").OrderBy(Asc("id"))),
		},
		{
			"transitive_inherited_order",
			Select(Sum(Col("v")).OverNamed("child")).
				Window("base", base).
				Window("middle", Window().Base("base")).
				Window("child", Window().Base("middle").OrderBy(Asc("id"))),
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
				Window("base", Window().OrderBy(Asc("v"), Asc("id"))).
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
	checkError(t, Select(Sum(Col("v")).Over(WindowSpec{frame: windowFrame(99), start: CurrentRow(), end: CurrentRow()})), ErrInvalid)
}

func TestExplainBareSerialization(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"bare_bind_order", Explain(Select(Param("payload"))).Analyze(true).Serialize().Timing(false), `EXPLAIN (ANALYZE TRUE, SERIALIZE, TIMING FALSE) SELECT $1`, []any{"payload"}},
		{"one_mode", Explain(Select(LiteralInt(1))).Analyze(true).Serialize(SerializeText), `EXPLAIN (ANALYZE TRUE, SERIALIZE TEXT) SELECT 1`, nil},
		{"bare_to_none", Explain(Select(LiteralInt(1))).Analyze(true).Serialize().Serialize(SerializeNone), `EXPLAIN (ANALYZE TRUE, SERIALIZE NONE) SELECT 1`, nil},
		{"none_to_bare", Explain(Select(LiteralInt(1))).Analyze(true).Serialize(SerializeNone).Serialize(), `EXPLAIN (ANALYZE TRUE, SERIALIZE) SELECT 1`, nil},
	})

	checkError(t, Explain(Select(LiteralInt(1))).Serialize(SerializeText, SerializeBinary), ErrInvalid)
	checkError(t, Explain(Select(LiteralInt(1))).Serialize(), ErrInvalid)
	checkError(t, Explain(Select(LiteralInt(1))).Analyze(false).Serialize(SerializeNone).Serialize(), ErrInvalid)

	bare := Explain(Select(Param("payload"))).Analyze(true).Serialize()
	sql, args, err := bare.ToSQLWith(Options{PostgreSQL: PostgreSQL17})
	if err != nil || sql != `EXPLAIN (ANALYZE TRUE, SERIALIZE) SELECT $1` || len(args) != 1 || args[0] != "payload" {
		t.Fatalf("PostgreSQL 17 bare serialization: %q %#v %v", sql, args, err)
	}
	sql, args, err = bare.ToSQLWith(Options{PostgreSQL: PostgreSQL16})
	if !errors.Is(err, ErrUnsupported) || sql != "" || args != nil {
		t.Fatalf("PostgreSQL 16 bare serialization: %q %#v %v", sql, args, err)
	}
}
