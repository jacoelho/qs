package qs

import "testing"

func TestCaseRolesRenderDistinctForms(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{
			name: "searched",
			statement: Select(
				Case().When(Col("kind").Eq(Param(1)), Param("searched-then")).Else(
					Param("searched-else"),
				).End(),
			),
			sql:  `SELECT CASE WHEN ("kind" = $1) THEN $2 ELSE $3 END`,
			args: []any{1, "searched-then", "searched-else"},
		},
		{
			name: "simple",
			statement: Select(
				CaseOf(Col("kind")).WhenValue(LiteralInt(1), Param("simple-then")).Else(
					Param("simple-else"),
				).End(),
			),
			sql:  `SELECT CASE "kind" WHEN 1 THEN $1 ELSE $2 END`,
			args: []any{"simple-then", "simple-else"},
		},
	})
}

func TestCaseBuildersBranchWithoutSharing(t *testing.T) {
	t.Parallel()

	searched := Case().When(True(), Param("before"))
	searchedExpr := searched.End()
	extended := searched.When(False(), Param("after")).Else(Param("later")).End()
	checkSQL(t, Select(searchedExpr), `SELECT CASE WHEN TRUE THEN $1 END`, "before")
	checkSQL(t, Select(extended), `SELECT CASE WHEN TRUE THEN $1 WHEN FALSE THEN $2 ELSE $3 END`, "before", "after", "later")

	simple := CaseOf(Col("kind")).WhenValue(LiteralInt(1), Param("before"))
	simpleExpr := simple.End()
	extendedSimple := simple.WhenValue(LiteralInt(2), Param("after")).Else(Param("later")).End()
	checkSQL(t, Select(simpleExpr), `SELECT CASE "kind" WHEN 1 THEN $1 END`, "before")
	checkSQL(t, Select(extendedSimple), `SELECT CASE "kind" WHEN 1 THEN $1 WHEN 2 THEN $2 ELSE $3 END`, "before", "after", "later")
}

func TestCaseZeroBuildersAreInvalid(t *testing.T) {
	t.Parallel()

	var zeroSearched SearchedCaseBuilder
	checkError(t, Select(zeroSearched.End()), ErrInvalid)
	var zeroSimple SimpleCaseBuilder
	checkError(t, Select(zeroSimple.End()), ErrInvalid)

	// A zero simple builder with a branch must still render in simple mode and
	// reject its missing operand rather than silently becoming a searched CASE.
	checkError(t, Select(zeroSimple.WhenValue(LiteralInt(1), Param("then")).End()), ErrInvalid)
}
