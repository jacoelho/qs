package qs

import "testing"

func TestJoinCompletionForms(t *testing.T) {
	t.Parallel()

	left, right := Table("left_table"), Table("right_table")
	for _, tc := range []struct {
		name string
		join Relation
		sql  string
	}{
		{"inner on", InnerJoin(left, right).On(EqColumns("left_table.id", "right_table.id")), `SELECT * FROM ("left_table" JOIN "right_table" ON ("left_table"."id" = "right_table"."id"))`},
		{"left on", LeftJoin(left, right).On(EqColumns("left_table.id", "right_table.id")), `SELECT * FROM ("left_table" LEFT JOIN "right_table" ON ("left_table"."id" = "right_table"."id"))`},
		{"right on", RightJoin(left, right).On(EqColumns("left_table.id", "right_table.id")), `SELECT * FROM ("left_table" RIGHT JOIN "right_table" ON ("left_table"."id" = "right_table"."id"))`},
		{"full on", FullJoin(left, right).On(EqColumns("left_table.id", "right_table.id")), `SELECT * FROM ("left_table" FULL JOIN "right_table" ON ("left_table"."id" = "right_table"."id"))`},
		{"inner using", InnerJoin(left, right).Using("id"), `SELECT * FROM ("left_table" JOIN "right_table" USING ("id"))`},
		{"left using", LeftJoin(left, right).Using("id"), `SELECT * FROM ("left_table" LEFT JOIN "right_table" USING ("id"))`},
		{"right using", RightJoin(left, right).Using("id"), `SELECT * FROM ("left_table" RIGHT JOIN "right_table" USING ("id"))`},
		{"full using", FullJoin(left, right).Using("id"), `SELECT * FROM ("left_table" FULL JOIN "right_table" USING ("id"))`},
	} {
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); checkSQL(t, Select(Star()).FromExpr(tc.join), tc.sql) })
	}
}

func TestJoinUsingAliasAndRelationAlias(t *testing.T) {
	t.Parallel()

	checkSQL(t,
		Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).UsingAs("joined", "id").As("whole")),
		`SELECT * FROM ("a" JOIN "b" USING ("id") AS "joined") AS "whole"`,
	)
}

func TestJoinCompletionOwnsQualifiersAndSharesOperands(t *testing.T) {
	t.Parallel()

	left, right := Table("a"), Table("b")
	pending := InnerJoin(left, right)
	conditions := []Condition{Eq("a.id", 1), Eq("b.active", true)}
	completed := pending.On(conditions[0], conditions[1:]...)
	conditions[0] = Eq("a.id", 99)
	conditions[1] = Condition{}
	checkSQL(t, Select(Star()).FromExpr(completed), `SELECT * FROM ("a" JOIN "b" ON ("a"."id" = $1) AND ("b"."active" = $2))`, 1, true)

	columns := []string{"id", "tenant_id"}
	using := pending.Using(columns[0], columns[1:]...)
	columns[0], columns[1] = "changed", "also_changed"
	checkSQL(t, Select(Star()).FromExpr(using), `SELECT * FROM ("a" JOIN "b" USING ("id", "tenant_id"))`)

	checkSQL(t, Select(Star()).FromExpr(pending.On(EqColumns("a.id", "b.id"))), `SELECT * FROM ("a" JOIN "b" ON ("a"."id" = "b"."id"))`)
	checkSQL(t, Select(Star()).FromExpr(pending.Using("id")), `SELECT * FROM ("a" JOIN "b" USING ("id"))`)
}

func TestNestedJoinAndCloneLiveSubquery(t *testing.T) {
	t.Parallel()

	child := Select(Param("before"))
	query := Select(Star()).FromExpr(
		InnerJoin(InnerJoin(Subquery(child, "a"), Table("b")).On(True()), Table("c")).On(True()),
	)
	cloned := query.Clone()
	child.RemoveColumns().Columns(Param("after"))
	checkSQL(t, query, `SELECT * FROM (((SELECT $1) AS "a" JOIN "b" ON TRUE) JOIN "c" ON TRUE)`, "after")
	checkSQL(t, cloned, `SELECT * FROM (((SELECT $1) AS "a" JOIN "b" ON TRUE) JOIN "c" ON TRUE)`, "before")
}

func TestJoinCompletionInvalidInputsRemainAtomic(t *testing.T) {
	t.Parallel()

	checkError(t, Select(Star()).FromExpr((PendingJoin{}).On(True())), ErrInvalid)
	checkError(t, Select(Star()).FromExpr((PendingJoin{}).Using("id")), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).On(Condition{})), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(InnerJoin(Relation{}, Table("b")).On(True())), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).Using("")), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).UsingAs("bad\x00", "id")), ErrInvalid)
	checkError(t, Select(Star()).FromExpr(InnerJoin(Table("a"), Table("b")).Using("id").As("bad\x00")), ErrInvalid)
}

func TestSelectJoinZeroConditionAndMissingFrom(t *testing.T) {
	t.Parallel()

	checkError(t, Select(Star()).From("a").Join("b", Condition{}), ErrInvalid)
	checkError(t, Select(Star()).Join("b", True()), ErrInvalid)
	checkSQL(t, Select(Star()).From("a").Join("b", True()), `SELECT * FROM ("a" JOIN "b" ON TRUE)`)
	checkSQL(t, Select(Star()).From("a").CrossJoin("b"), `SELECT * FROM ("a" CROSS JOIN "b")`)
}
