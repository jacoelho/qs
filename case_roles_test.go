package qs

import "testing"

func TestCaseRolesRenderDistinctForms(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{
			name:      "searched",
			statement: Select(Case().When(Eq("kind", 1), Param("searched-then")).Else(Param("searched-else")).End()),
			sql:       `SELECT CASE WHEN ("kind" = $1) THEN $2 ELSE $3 END`,
			args:      []any{1, "searched-then", "searched-else"},
		},
		{
			name:      "simple",
			statement: Select(CaseOf(Ident("kind")).WhenValue(LiteralInt(1), Param("simple-then")).Else(Param("simple-else")).End()),
			sql:       `SELECT CASE "kind" WHEN 1 THEN $1 ELSE $2 END`,
			args:      []any{"simple-then", "simple-else"},
		},
	})
}

func TestCaseEndSnapshotsBranchesAndElse(t *testing.T) {
	t.Parallel()

	searched := Case().When(True(), Param("before"))
	searchedExpr := searched.End()
	searched.When(False(), Param("after")).Else(Param("later"))
	checkSQL(t, Select(searchedExpr), `SELECT CASE WHEN TRUE THEN $1 END`, "before")

	simple := CaseOf(Ident("kind")).WhenValue(LiteralInt(1), Param("before"))
	simpleExpr := simple.End()
	simple.WhenValue(LiteralInt(2), Param("after")).Else(Param("later"))
	checkSQL(t, Select(simpleExpr), `SELECT CASE "kind" WHEN 1 THEN $1 END`, "before")
}

func TestCaseCloneClonesNestedLiveQuery(t *testing.T) {
	t.Parallel()

	child := Select(Param("before"))
	query := Select(Case().When(True(), Scalar(child)).End())
	clone := query.Clone()

	child.Columns(Param("after"))
	checkError(t, query, ErrInvalid)
	checkSQL(t, clone, `SELECT CASE WHEN TRUE THEN (SELECT $1) END`, "before")
}

func TestCaseNilAndZeroBuildersAreInvalid(t *testing.T) {
	t.Parallel()

	var nilSearched *SearchedCaseBuilder
	checkError(t, Select(nilSearched.End()), ErrInvalid)
	var nilSimple *SimpleCaseBuilder
	checkError(t, Select(nilSimple.End()), ErrInvalid)

	var zeroSearched SearchedCaseBuilder
	checkError(t, Select(zeroSearched.End()), ErrInvalid)
	var zeroSimple SimpleCaseBuilder
	checkError(t, Select(zeroSimple.End()), ErrInvalid)

	// A zero simple builder with a branch must still render in simple mode and
	// reject its missing operand rather than silently becoming a searched CASE.
	checkError(t, Select(zeroSimple.WhenValue(LiteralInt(1), Param("then")).End()), ErrInvalid)
}
