package qs

import (
	"errors"
	"strings"
	"testing"
)

// PostgreSQL keeps the first 63 bytes of an identifier, cut at a character
// boundary, and compares names after that. Each expectation here was observed
// on PostgreSQL 18.6, which rejects every statement in the first test.
func TestNamesEqualAfterTruncationAreDuplicates(t *testing.T) {
	t.Parallel()

	prefix := strings.Repeat("a", 63)
	x, y := prefix+"x", prefix+"y"
	for _, tc := range []struct {
		name      string
		statement Statement
		clause    string
		detail    string
	}{
		{"cte", Select(Param(1)).With(CTE(x, Select(Param(1))), CTE(y, Select(Param(2)))), "WITH", "duplicate CTE name"},
		{"window", Select(RowNumber().OverNamed(x)).Window(x, Window()).Window(y, Window()), "WINDOW", "duplicate window name"},
		{"insert_columns", InsertInto("t").Columns(x, y).Values(Value(1), Value(2)), "INSERT", "duplicate target column"},
		{
			"insert_qualified_targets",
			InsertInto("t").Targets(Col("t", x), Col("t", y)).Values(Value(1), Value(2)),
			"INSERT",
			"duplicate target column",
		},
		{"update", Update("t").Set(Set(x, Value(1)), Set(y, Value(2))).AllRows(), "SET", "duplicate assignment target"},
		{"update_row", Update("t").Set(SetRow([]string{x, y}, Value(1), Value(2))).AllRows(), "SET", "duplicate assignment target"},
		{
			"update_schema_qualified",
			Update("t").
				Set(Assign(Col("s", "t", x), Value(1)), Assign(Col("s", "t", y), Value(2))).
				AllRows(),
			"SET",
			"duplicate assignment target",
		},
		{
			"merge_insert",
			MergeInto("t").
				Using(Table("s")).
				On(True()).
				When(NotMatched().ThenInsertValues([]string{x, y}, Value(1), Value(2))),
			"MERGE INSERT",
			"duplicate target column",
		},
		{
			"returning_aliases",
			DeleteFrom("t").
				AllRows().
				ReturningCols("id").
				ReturningRows(ReturningAliases{Old: x, New: y}),
			"RETURNING",
			"OLD and NEW aliases must differ",
		},
		{"longer_than_the_kept_name", InsertInto("t").Columns(prefix, y).Values(Value(1), Value(2)), "INSERT", "duplicate target column"},
		{
			// The two-byte character straddles byte 63, so neither is kept.
			"character_across_the_limit",
			InsertInto("t").Columns(prefix[:62]+"é", prefix[:62]+"ü").Values(Value(1), Value(2)),
			"INSERT", "duplicate target column",
		},
		{
			"json_table_columns",
			Select(Star()).FromExpr(
				JSONTable(Col("doc"), "$", JSONColumn(x, TypeText), JSONColumn(y, TypeText)).Ref(),
			),
			"JSON_TABLE",
			"duplicate column or path name",
		},
		{
			"json_table_column_and_path",
			Select(Star()).FromExpr(
				JSONTable(
					Col("doc"),
					"$",
					JSONColumn(x, TypeText),
					JSONNested("$", JSONColumn("b", TypeText)).PathName(y),
				).Ref(),
			),
			"JSON_TABLE", "duplicate column or path name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkError(t, tc.statement, ErrInvalid)
			_, _, err := tc.statement.ToSQL()
			renderError, _ := errors.AsType[*RenderError](err)
			if renderError.Clause != tc.clause || renderError.Detail != tc.detail {
				t.Fatalf("got (%s) %s, want (%s) %s", renderError.Clause, renderError.Detail, tc.clause, tc.detail)
			}
		})
	}
}

func TestNamesDistinctWithinTheLimitAreDistinct(t *testing.T) {
	t.Parallel()

	prefix := strings.Repeat("a", 62)
	for name, columns := range map[string][2]string{
		"last_kept_byte":      {prefix + "x", prefix + "y"},
		"last_kept_byte_long": {prefix + "x" + "tail", prefix + "y" + "tail"},
		"last_kept_character": {prefix[:61] + "é", prefix[:61] + "ü"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, statement := range []Statement{
				InsertInto("t").Columns(columns[0], columns[1]).Values(Value(1), Value(2)),
				Select(Star()).FromExpr(
					JSONTable(
						Col("doc"),
						"$",
						JSONColumn(columns[0], TypeText),
						JSONColumn(columns[1], TypeText),
					).Ref(),
				),
			} {
				if _, _, err := statement.ToSQL(); err != nil {
					t.Fatalf("ToSQL: %v", err)
				}
			}
		})
	}
}

// A base window is found by the name PostgreSQL keeps, as a duplicate is.
func TestBaseWindowResolvesByTheKeptName(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("w", 70)
	kept := long[:63]
	checkSQL(t,
		Select(RowNumber().OverNamed("b")).
			From("t").
			Window(long, Window().OrderBy(Col("a").Asc())).
			Window("b", Window().Base(kept)),
		`SELECT row_number() OVER "b" FROM "t" WINDOW "`+long+`" AS (ORDER BY "a" ASC), "b" AS ("`+kept+`")`,
	)

	// The inherited ORDER BY is found through the same name: overriding it fails.
	override := Select(RowNumber().OverNamed("c")).
		From("t").
		Window(long, Window().OrderBy(Col("a").Asc())).
		Window("b", Window().Base(kept)).
		Window("c", Window().Base("b").OrderBy(Col("z").Asc()))
	checkError(t, override, ErrInvalid)
	_, _, err := override.ToSQL()
	if renderError, _ := errors.AsType[*RenderError](err); renderError.Detail != "cannot override ORDER BY clause of inherited window" {
		t.Fatalf("got %v", err)
	}
}
