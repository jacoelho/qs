package qs

import (
	"errors"
	"strings"
	"testing"
)

// A pointer to a builder satisfies Statement and Rowset through the builder's
// value methods, so it must render as the builder does wherever one is
// accepted.
func TestPointerToBuilderRendersAsItsValue(t *testing.T) {
	t.Parallel()

	query := Select(Col("id")).From("users")
	insert := InsertInto("audit").Columns("id").Values(Value(1)).ReturningCols("id")
	union := Union(query, query)
	for _, tc := range []struct {
		name    string
		pointer Statement
		value   Statement
	}{
		{"top_level", &query, query},
		{
			"subquery",
			Select(Star()).FromExpr(Subquery(&query, "s")),
			Select(Star()).FromExpr(Subquery(query, "s")),
		},
		{"exists", Select(Exists(&query).Expr()), Select(Exists(query).Expr())},
		{"scalar", Select(Scalar(&query)), Select(Scalar(query))},
		{
			"cte",
			Select(Col("id")).From("c").With(CTE("c", &query)),
			Select(Col("id")).From("c").With(CTE("c", query)),
		},
		{
			"cte_dml",
			Select(Col("id")).From("c").With(CTE("c", &insert)),
			Select(Col("id")).From("c").With(CTE("c", insert)),
		},
		{"set_branches", Union(&query, &query), Union(query, query)},
		{"set_left_spine", Union(&union, &query), Union(union, query)},
		{
			"insert_source",
			InsertInto("copy").Columns("id").From(&query),
			InsertInto("copy").Columns("id").From(query),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, wantArgs, err := ToSQL(tc.value)
			if err != nil {
				t.Fatalf("value: %v", err)
			}
			checkSQL(t, tc.pointer, want, wantArgs...)
		})
	}
}

func TestPointerToBuilderKeepsWidthChecks(t *testing.T) {
	t.Parallel()

	one, two := Select(Col("a")).From("t"), Select(Col("a"), Col("b")).From("t")
	for name, statement := range map[string]Statement{
		"set_operation": Union(&one, &two),
		"subquery":      Select(Col("x").InQuery(&two).Expr()),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkError(t, statement, ErrInvalid)
		})
	}
}

func TestNilPointerToBuilderIsANilStatement(t *testing.T) {
	t.Parallel()

	var missing *SelectBuilder
	_, _, err := ToSQL(missing)
	renderError, ok := errors.AsType[*RenderError](err)
	if !ok || !errors.Is(err, ErrInvalid) || renderError.Detail != "nil statement" {
		t.Fatalf("got %v", err)
	}
}

func TestPointerToJSONTableColumnRendersAsItsValue(t *testing.T) {
	t.Parallel()

	value, exists, ordinality := JSONColumn("a", TypeText), JSONExistsColumn("e", TypeBool), JSONOrdinality(
		"n",
	)
	nested := JSONNested("$.x", &value)
	checkSQL(t,
		Select(Star()).FromExpr(JSONTable(Col("doc"), "$", &ordinality, &exists, &nested).Ref()),
		`SELECT * FROM JSON_TABLE("doc", E'$' COLUMNS ("n" FOR ORDINALITY, "e" boolean EXISTS, NESTED PATH E'$.x' COLUMNS ("a" text)))`,
	)

	var missing *JSONTableValueColumn
	_, _, err := Select(Star()).FromExpr(JSONTable(Col("doc"), "$", missing).Ref()).ToSQL()
	renderError, ok := errors.AsType[*RenderError](err)
	if !ok || renderError.Detail != "nil column" {
		t.Fatalf("got %v", err)
	}
}

// A pointer is replaced by its builder when the statement is constructed, so
// what was built cannot change afterwards and cannot contain itself.
func TestPointerToBuilderIsReadAtConstruction(t *testing.T) {
	t.Parallel()

	query := Select(Col("a")).From("t")
	union := Union(&query, &query)
	subquery := Select(Star()).FromExpr(Subquery(&query, "s"))
	query = query.Where(Col("a").IsNull())
	checkSQL(t, union, `(SELECT "a" FROM "t") UNION (SELECT "a" FROM "t")`)
	checkSQL(t, subquery, `SELECT * FROM (SELECT "a" FROM "t") AS "s"`)

	var self SetBuilder
	self = Union(&self, query)
	checkError(t, self, ErrInvalid)
}

// A chain assembled through pointers is the chain assembled from values: its
// widths are checked at every depth the renderer reaches, at the same cost.
func TestSetChainBuiltThroughPointersIsCheckedLikeValues(t *testing.T) {
	narrow, wide := Select(LiteralInt(1)), Select(LiteralInt(1), LiteralInt(2))
	chain := func(levels int, pointers bool) Rowset {
		var spine Rowset = narrow
		for range levels {
			union := Union(spine, narrow)
			if pointers {
				spine = &union
			} else {
				spine = union
			}
		}
		return spine
	}
	for _, levels := range []int{1, 100, 130, 200, 250} {
		for _, pointers := range []bool{false, true} {
			valid, invalid := chain(levels, pointers), Union(chain(levels, pointers), wide)
			sql, _, err := ToSQL(valid)
			if err != nil || strings.Count(sql, " UNION ") != levels {
				t.Fatalf("%d levels, pointers %v: %v", levels, pointers, err)
			}
			checkError(t, invalid, ErrInvalid)
		}
	}

	// The outermost statement is a value in both, so only the stored spine differs.
	values, pointers := Union(chain(128, false), narrow), Union(chain(128, true), narrow)
	buffer := make([]byte, 0, 1<<16)
	render := func(statement Statement) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, _, err := AppendSQL(buffer[:0], nil, statement); err != nil {
				t.Fatal(err)
			}
		})
	}
	if fromValues, fromPointers := render(values), render(pointers); fromPointers != fromValues {
		t.Errorf("rendering allocates %v times for a chain of pointers and %v for one of values", fromPointers, fromValues)
	}
}
