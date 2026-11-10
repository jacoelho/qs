package qs

import (
	"errors"
	"testing"
)

func TestSelectSQLIsConvenienceWrapper(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		projections []string
		sql         string
	}{
		{"one_column", []string{"id"}, `SELECT id FROM "users" WHERE ("active" = $1) LIMIT $2`},
		{"projection_list", []string{"id, name, created_at"}, `SELECT id, name, created_at FROM "users" WHERE ("active" = $1) LIMIT $2`},
		{"multiple_lists", []string{"id, name", "created_at, active"}, `SELECT id, name, created_at, active FROM "users" WHERE ("active" = $1) LIMIT $2`},
		{"cast_and_alias", []string{"team_id::text AS team"}, `SELECT team_id::text AS team FROM "users" WHERE ("active" = $1) LIMIT $2`},
		{"embedded_commas", []string{"jsonb_build_object('id', id, 'name', name)"}, `SELECT jsonb_build_object('id', id, 'name', name) FROM "users" WHERE ("active" = $1) LIMIT $2`},
		{"question_mark_operator", []string{"metadata ? 'email' AS has_email"}, `SELECT metadata ? 'email' AS has_email FROM "users" WHERE ("active" = $1) LIMIT $2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expressions := make([]Expr, len(tc.projections))
			for i, sql := range tc.projections {
				expressions[i] = UnsafeSQL(sql)
			}
			explicit := SelectSlice(expressions).From("users").
				Where(Col("active").Eq(Param(true))).Limit(50)
			shorthand := SelectSQL(tc.projections[0], tc.projections[1:]...).From("users").
				Where(Col("active").Eq(Param(true))).Limit(50)
			checkSQL(t, explicit, tc.sql, true, 50)
			checkSQL(t, shorthand, tc.sql, true, 50)
		})
	}
}

func TestSelectIdentifierExpressionAndValueAreDistinct(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"cast_is_an_expression", Select(Col("id").Cast(TypeText)), `SELECT ("id")::text`, nil},
		{"cast_text_is_not_an_identifier", Select(Col("id::text")), `SELECT "id::text"`, nil},
		{"raw_sql_is_explicit", Select(UnsafeSQL("id::text")), `SELECT id::text`, nil},
		{"value_is_bound", Select(Param("id::text")), `SELECT $1`, []any{"id::text"}},
		{"column_names_wrapper", SelectCols("id", "u.name"), `SELECT "id", "u.name"`, nil},
		{"projection_order", Select(Col("id"), Col("created_at")).Columns(Col("name")), `SELECT "id", "created_at", "name"`, nil},
	})
}

func TestSelectStructuredReuseOwnsProjectionSlice(t *testing.T) {
	t.Parallel()

	columns := make([]Expr, 2, 8)
	columns[0], columns[1] = Col("id"), Col("created_at")
	q := SelectSlice(columns).Columns(Col("name"))
	columns[0] = Col("different")
	columns = columns[:3]
	columns[2] = Col("another")
	checkSQL(t, q, `SELECT "id", "created_at", "name"`)
}

func TestSelectWrappersHaveSameParameterNumbering(t *testing.T) {
	t.Parallel()

	projection := `"id", "name", "created_at"`
	raw := SelectSQL(projection).Columns(Param("visible").As("label")).
		Where(Col("active").Eq(Param(true))).Limit(50)
	structured := Select(Col("id"), Col("name"), Col("created_at")).
		Columns(Param("visible").As("label")).
		Where(Col("active").Eq(Param(true))).Limit(50)
	const sql = `SELECT "id", "name", "created_at", $1 AS "label" WHERE ("active" = $2) LIMIT $3`
	checkSQL(t, raw, sql, "visible", true, 50)
	checkSQL(t, structured, sql, "visible", true, 50)
}

// A query has one row limit. Each limit method replaces it, so LIMIT and
// FETCH FIRST never appear together.
func TestRowLimitHasOneValue(t *testing.T) {
	t.Parallel()

	base := Select(Col("a")).From("t")
	ordered := base.OrderBy(Col("a").Asc())
	const selectSQL, orderedSQL = `SELECT "a" FROM "t"`, `SELECT "a" FROM "t" ORDER BY "a" ASC`
	runCases(t, []renderCase{
		{"limit", base.Limit(5), selectSQL + ` LIMIT $1`, []any{5}},
		{"limit_expression", base.LimitExpr(LiteralInt(5)), selectSQL + ` LIMIT 5`, nil},
		{"limit_all", base.LimitAll(), selectSQL + ` LIMIT ALL`, nil},
		{"with_ties", ordered.LimitWithTies(3), orderedSQL + ` FETCH FIRST ($1) ROWS WITH TIES`, []any{3}},
		{"with_ties_expression", ordered.LimitWithTiesExpr(LiteralInt(3)), orderedSQL + ` FETCH FIRST (3) ROWS WITH TIES`, nil},
		{"with_ties_after_offset", ordered.LimitWithTies(3).Offset(2), orderedSQL + ` OFFSET $1 FETCH FIRST ($2) ROWS WITH TIES`, []any{2, 3}},
		{"limit_replaces_ties", ordered.LimitWithTies(3).Limit(5), orderedSQL + ` LIMIT $1`, []any{5}},
		{"ties_replace_limit", ordered.Limit(5).LimitWithTies(3), orderedSQL + ` FETCH FIRST ($1) ROWS WITH TIES`, []any{3}},
		{"all_replaces_limit", base.Limit(5).LimitAll(), selectSQL + ` LIMIT ALL`, nil},
		{"limit_replaces_all", base.LimitAll().Limit(5), selectSQL + ` LIMIT $1`, []any{5}},
		{"limit_keeps_offset", base.Offset(2).Limit(5), selectSQL + ` LIMIT $1 OFFSET $2`, []any{5, 2}},
		{"with_ties_locked", ordered.LimitWithTies(3).Lock(ForUpdate().NoWait()), orderedSQL + ` FETCH FIRST ($1) ROWS WITH TIES FOR UPDATE NOWAIT`, []any{3}},
	})
}

// An empty run-time projection is the defect. A statement that compares its
// width with another's must report that, not a width mismatch.
func TestEmptySelectSliceIsReportedByStatementsThatEmbedIt(t *testing.T) {
	t.Parallel()
	empty := SelectSlice(nil)
	for name, query := range map[string]Statement{
		"set_operand":   Union(empty, Select(Param(1))),
		"scalar":        Select(Scalar(empty)),
		"insert_source": InsertInto("t").Columns("a").From(empty),
		"cte_columns":   Select(Star()).With(CTE("c", empty).Columns("a")).From("c"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := query.ToSQL()
			var renderErr *RenderError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &renderErr) {
				t.Fatalf("expected invalid RenderError, got %v", err)
			}
			if want := "requires a projection or SelectNoColumns"; renderErr.Detail != want {
				t.Fatalf("detail = %q; want %q", renderErr.Detail, want)
			}
		})
	}
}
