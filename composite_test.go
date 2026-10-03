package qs

import "testing"

func TestCompositeExpressions(t *testing.T) {
	t.Parallel()

	runCases(t, []renderCase{
		{"field", Select(Col("person").Field("first_name")), `SELECT ("person")."first_name"`, nil},
		{"literal_dotted_field", Select(Col("person").Field("a.b")), `SELECT ("person")."a.b"`, nil},
		{"quoted_field", Select(Col("person").Field(`weird"name`)), `SELECT ("person")."weird""name"`, nil},
		{"all_fields", Select(Col("person").Fields()), `SELECT ("person").*`, nil},
		{"full_slice", Select(Col("items").SliceAll()), `SELECT ("items")[:]`, nil},
		{"tuple", Select(Tuple(Col("a"), Param(7)).Expr()), `SELECT ("a", $1)`, []any{7}},
		{"row", Select(Row(Col("a"), Param(7)).Expr()), `SELECT ROW("a", $1)`, []any{7}},
		{"like_escape_expr", Select(Col("name").LikeExpr(Param(`a\\_%`)).EscapeExpr(Param(`\\`)).Expr()), `SELECT ("name" LIKE $1 ESCAPE $2)`, []any{`a\\_%`, `\\`}},
		{"not_like_escape_expr", Select(Col("name").NotLikeExpr(Param(`a\\_%`)).EscapeExpr(Param(`\\`)).Expr()), `SELECT ("name" NOT LIKE $1 ESCAPE $2)`, []any{`a\\_%`, `\\`}},
		{"not_ilike_escape_expr", Select(Col("name").NotILikeExpr(Param(`a\\_%`)).EscapeExpr(Param(`\\`)).Expr()), `SELECT ("name" NOT ILIKE $1 ESCAPE $2)`, []any{`a\\_%`, `\\`}},
		{"complex_insert_targets", InsertInto("people").Targets(Col("profile").Field("name"), Col("items").Index(Param(2))).Values(Write(Param("Ada")), Write(Param("tag"))), `INSERT INTO "people" ("profile"."name", "items"[$1]) VALUES ($2, $3)`, []any{2, "Ada", "tag"}},
		{"multiple_insert_rows", InsertInto("people").Columns("first_name", "age").Values(Write(Param("Ada")), Write(Param(36))).Values(Write(Param("Grace")), Write(Param(37))), `INSERT INTO "people" ("first_name", "age") VALUES ($1, $2), ($3, $4)`, []any{"Ada", 36, "Grace", 37}},
		{"row_assignment", Update("people").Set(AssignRow([]Expr{Col("person").Field("first_name"), Col("person").Field("age")}, WriteTuple(Write(Param("Grace")), Write(Param(37))))), `UPDATE "people" SET ("person"."first_name", "person"."age") = ($1, $2)`, []any{"Grace", 37}},
		{"subquery_row_assignment", Update("people").Set(AssignRowFrom([]Expr{Col("first_name"), Col("age")}, Select(Param("Grace"), Param(37)))), `UPDATE "people" SET ("first_name", "age") = (SELECT $1, $2)`, []any{"Grace", 37}},
	})
}

func TestCompositeUnknownProjectionWidths(t *testing.T) {
	t.Parallel()

	checkSQL(t, Select(Row(Col("person").Fields()).Expr()), `SELECT ROW(("person").*)`)
	checkSQL(t, ValuesExpr(Col("person").Fields()), `VALUES (("person").*)`)
	checkSQL(t, InsertInto("people").Columns("first_name", "age").Values(Write(Col("person").Fields())), `INSERT INTO "people" ("first_name", "age") VALUES (("person").*)`)
	checkSQL(t, Update("people").Set(AssignRow([]Expr{Col("first_name"), Col("age")}, WriteRowExpr(Row(Col("person").Fields())))), `UPDATE "people" SET ("first_name", "age") = ROW(("person").*)`)
}

func TestCompositeKnownWidthErrors(t *testing.T) {
	t.Parallel()

	checkError(t, Select(Tuple(Col("only")).Expr()), ErrInvalid)
	checkError(t, Select(Row().Expr()), ErrInvalid)
	checkError(t, Select(Row(Col("a"), Col("b")).Eq(Row(Col("a"))).Expr()), ErrInvalid)
	checkError(t, ValuesExpr(Param(1)).RowExpr(Param(2), Param(3)), ErrInvalid)
	checkError(t, InsertInto("people").Columns("first_name", "age").Values(Write(Param("Ada"))), ErrInvalid)
	checkError(t, InsertInto("people").Columns("first_name", "age", "first_name").Values(Write(Param("Ada")), Write(Param(36)), Write(Param("duplicate"))), ErrInvalid)
	checkError(t, Update("people").Set(AssignRow([]Expr{Col("first_name"), Col("age")}, WriteRowExpr(Tuple(Param("Ada"))))), ErrInvalid)
	checkError(t, Update("people").Set(AssignRowFrom([]Expr{Col("first_name"), Col("age")}, Select(Param("Ada")))), ErrInvalid)
	checkError(t, Select(Col("name").EqExpr(Param("Ada")).EscapeExpr(Param(`\\`)).Expr()), ErrInvalid)
	checkError(t, InsertInto("people").Targets(Col("first_name"), Col("first_name")).Values(Write(Param("Ada")), Write(Param("Grace"))), ErrInvalid)
}

func TestCompositeSlicesAndCloneIsolation(t *testing.T) {
	t.Parallel()

	targets := []Expr{Col("person").Field("first_name"), Col("person").Field("age")}
	row := WriteTuple(Write(Param("Ada")), Write(Param(36)))
	assignment := AssignRow(targets, row)
	targets[0] = Col("changed")
	checkSQL(t, Update("people").Set(assignment), `UPDATE "people" SET ("person"."first_name", "person"."age") = ($1, $2)`, "Ada", 36)

	insertTargets := []Expr{Col("first_name"), Col("age")}
	insert := InsertInto("people").Targets(insertTargets[0], insertTargets[1]).Values(Write(Param("Ada")), Write(Param(36)))
	insertTargets[0] = Col("changed")
	checkSQL(t, insert, `INSERT INTO "people" ("first_name", "age") VALUES ($1, $2)`, "Ada", 36)

	original := Update("people").Set(AssignRow([]Expr{
		Col("person").Field("first_name"),
		Col("person").Field("age"),
	}, WriteTuple(Write(Param("Ada")), Write(Param(36)))))
	cloned := Clone(original)
	cloned.set[0].columns[0].node.left.text = "other"
	checkSQL(t, original, `UPDATE "people" SET ("person"."first_name", "person"."age") = ($1, $2)`, "Ada", 36)
	checkSQL(t, cloned, `UPDATE "people" SET ("other"."first_name", "person"."age") = ($1, $2)`, "Ada", 36)

	child := Select(Col("source"))
	withChild := InsertInto("archive").Columns("value").From(child)
	childClone := Clone(withChild)
	source, ok := childClone.source.(*SelectBuilder)
	if !ok {
		t.Fatalf("cloned source has type %T, want *SelectBuilder", childClone.source)
	}
	source.columns[0].text = "changed"
	checkSQL(t, withChild, `INSERT INTO "archive" ("value") SELECT "source"`)
	checkSQL(t, childClone, `INSERT INTO "archive" ("value") SELECT "changed"`)
}
