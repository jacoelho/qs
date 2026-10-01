package qx

import "testing"

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
			explicit := Select(expressions...).From("users").
				Where(Eq("active", true)).Limit(50)
			shorthand := SelectSQL(tc.projections...).From("users").
				Where(Eq("active", true)).Limit(50)
			checkSQL(t, explicit, tc.sql, true, 50)
			checkSQL(t, shorthand, tc.sql, true, 50)
		})
	}
}

func TestSelectIdentifierExpressionAndValueAreDistinct(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"cast_is_an_expression", Select(Col("id").Cast(Text)), `SELECT ("id")::text`, nil},
		{"cast_text_is_not_an_identifier", Select(Col("id::text")), `SELECT "id::text"`, nil},
		{"raw_sql_is_explicit", Select(UnsafeSQL("id::text")), `SELECT id::text`, nil},
		{"value_is_bound", Select(Param("id::text")), `SELECT $1`, []any{"id::text"}},
		{"column_names_wrapper", SelectCols("u.id", "u.name"), `SELECT "u"."id", "u"."name"`, nil},
		{"projection_order", Select(Col("id"), Col("created_at")).Columns(Col("name")), `SELECT "id", "created_at", "name"`, nil},
	})
}

func TestSelectStructuredReuseOwnsProjectionSlice(t *testing.T) {
	t.Parallel()

	columns := make([]Expr, 2, 8)
	columns[0], columns[1] = Col("id"), Col("created_at")
	q := Select(columns...).Columns(Col("name"))
	columns[0] = Col("different")
	columns = append(columns, Col("another"))
	checkSQL(t, q, `SELECT "id", "created_at", "name"`)
}

func TestSelectWrappersHaveSameParameterNumbering(t *testing.T) {
	t.Parallel()

	projection := `"id", "name", "created_at"`
	raw := SelectSQL(projection).Columns(Param("visible").As("label")).
		Where(Eq("active", true)).Limit(50)
	structured := Select(Col("id"), Col("name"), Col("created_at")).
		Columns(Param("visible").As("label")).
		Where(Eq("active", true)).Limit(50)
	const sql = `SELECT "id", "name", "created_at", $1 AS "label" WHERE ("active" = $2) LIMIT $3`
	checkSQL(t, raw, sql, "visible", true, 50)
	checkSQL(t, structured, sql, "visible", true, 50)
}
