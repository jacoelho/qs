package qs

import (
	"database/sql/driver"
	"errors"
	"testing"
)

type writeDriverValue struct{ text string }

func (v writeDriverValue) Value() (driver.Value, error) { return v.text, nil }

func TestValueBindsMixedDestinationValues(t *testing.T) {
	t.Parallel()
	checkSQL(t,
		InsertInto("people").Columns("id", "name", "note", "created", "rank").Values(
			Value(1), Value("Ada"), Value(nil), Default(), Write(LiteralInt(7)),
		),
		`INSERT INTO "people" ("id", "name", "note", "created", "rank") VALUES ($1, $2, $3, DEFAULT, 7)`,
		1, "Ada", nil,
	)
}

func TestWriteRolesRenderDefaultsAndPreserveArguments(t *testing.T) {
	t.Parallel()
	checkSQL(t,
		Update("people").Set(SetDefault("name")),
		`UPDATE "people" SET "name" = DEFAULT`,
	)
	checkSQL(t,
		Update("people").Set(Assign(Col("name"), Value("Ada"))),
		`UPDATE "people" SET "name" = $1`, "Ada",
	)
	checkSQL(t,
		InsertInto("people").Columns("id", "name").Values(Default(), Value("Ada")),
		`INSERT INTO "people" ("id", "name") VALUES (DEFAULT, $1)`, "Ada",
	)
	checkSQL(t,
		InsertInto("people").Set(SetDefault("name")),
		`INSERT INTO "people" ("name") VALUES (DEFAULT)`,
	)
	checkSQL(t,
		Update("people").Set(AssignRow(
			[]Expr{Col("first"), Col("last")},
			WriteTuple(Default(), Value("Lovelace")),
		)),
		`UPDATE "people" SET ("first", "last") = (DEFAULT, $1)`, "Lovelace",
	)
	checkSQL(t,
		Update("people").Set(AssignRow(
			[]Expr{Col("first"), Col("last")},
			WriteRow(Default(), Value("Lovelace")),
		)),
		`UPDATE "people" SET ("first", "last") = ROW(DEFAULT, $1)`, "Lovelace",
	)
	checkSQL(t,
		MergeInto("people").Using(Table("incoming")).On(True()).When(
			NotMatched().ThenInsertValues([]string{"name", "note"}, Default(), Value("from-merge")),
		),
		`MERGE INTO "people" USING "incoming" ON TRUE WHEN NOT MATCHED THEN INSERT ("name", "note") VALUES (DEFAULT, $1)`, "from-merge",
	)
}

func TestWriteRejectsNestedDefaultAndPreservesRollback(t *testing.T) {
	t.Parallel()
	privateDefault := Expr{kind: exprKeyword, text: "DEFAULT"}
	nested := Write(privateDefault.Add(Param("nested")))
	for _, statement := range []Statement{
		Update("people").Set(Set("name", nested)),
		Update("people").Set(AssignRow([]Expr{Col("first"), Col("last")}, WriteTuple(nested, Write(Param("Lovelace"))))),
	} {
		checkError(t, statement, ErrInvalid)
	}
	// A rejected nested DEFAULT must not leak either SQL or arguments into the
	// caller-owned buffers.
	statement := Update("people").Set(Set("name", nested))
	sql, args, err := AppendSQL([]byte("prefix "), []any{"existing"}, statement)
	if !errors.Is(err, ErrInvalid) || string(sql) != "prefix " || len(args) != 1 || args[0] != "existing" {
		t.Fatalf("nested DEFAULT rollback: %q %#v %v", sql, args, err)
	}
}

func TestWritePreservesCustomDriverValues(t *testing.T) {
	t.Parallel()
	value := &writeDriverValue{text: "custom"}
	statement := InsertInto("events").Columns("payload").Values(Write(Param(value)))
	sql, args, err := statement.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if sql != `INSERT INTO "events" ("payload") VALUES ($1)` || len(args) != 1 || args[0] != value {
		t.Fatalf("custom driver value: %q %#v", sql, args)
	}
}

func TestInsertTargetCreatesIndependentCompletedRoles(t *testing.T) {
	t.Parallel()
	target := InsertInto("events")
	columns := target.Columns("name")
	rows := columns.Values(Write(Param("row")))
	selectSource := columns.From(Select(Param("select")))
	assignments := target.Set(Set("name", Value("assignment")))
	defaults := columns.DefaultValues()
	checkSQL(t, rows, `INSERT INTO "events" ("name") VALUES ($1)`, "row")
	checkSQL(t, selectSource, `INSERT INTO "events" ("name") SELECT $1`, "select")
	checkSQL(t, assignments, `INSERT INTO "events" ("name") VALUES ($1)`, "assignment")
	checkSQL(t, defaults, `INSERT INTO "events" ("name") DEFAULT VALUES`)
}

func TestInsertColumnSelectorOwnsTargetsAcrossRoles(t *testing.T) {
	t.Parallel()
	selector := InsertInto("events").Columns("name")

	rows := selector.Values(Write(Param("row"))).Values(Write(Param("row-2"))).Into("archive").OnConflict(AnyConflict().DoNothing()).ReturningCols("name")
	selectSource := selector.From(Select(Param("select"))).From(Select(Param("select-2"))).Into("archive").ReturningCols("name")
	defaults := selector.DefaultValues().Into("archive").ReturningCols("name")

	checkSQL(t, rows, `INSERT INTO "archive" ("name") VALUES ($1), ($2) ON CONFLICT DO NOTHING RETURNING "name"`, "row", "row-2")
	checkSQL(t, selectSource, `INSERT INTO "archive" ("name") SELECT $1 RETURNING "name"`, "select-2")
	checkSQL(t, defaults, `INSERT INTO "archive" ("name") DEFAULT VALUES RETURNING "name"`)
}

func TestInsertTargetKeepsNestedChildrenLiveAndClonesThem(t *testing.T) {
	t.Parallel()
	child := Select(Param(1))
	selector := InsertInto("events").Targets(Col("items").Index(Scalar(child)))
	left := selector.Values(Write(Param("left")))
	right := selector.Values(Write(Param("right")))
	clone := Clone(left)

	child.Reset()
	child.Columns(Param(2))
	checkSQL(t, left, `INSERT INTO "events" ("items"[(SELECT $1)]) VALUES ($2)`, 2, "left")
	checkSQL(t, right, `INSERT INTO "events" ("items"[(SELECT $1)]) VALUES ($2)`, 2, "right")
	checkSQL(t, clone, `INSERT INTO "events" ("items"[(SELECT $1)]) VALUES ($2)`, 1, "left")
}

func TestWriteConstructorsCaptureStructuralSlices(t *testing.T) {
	t.Parallel()
	values := []WriteValue{Write(Param("first")), Write(Param("last"))}
	columnNames := []string{"first", "last"}
	columns := InsertInto("people").Columns(columnNames[0], columnNames[1:]...)
	columnNames[0], columnNames[1] = "changed_first", "changed_last"
	insert := columns.Values(values[0], values[1:]...)
	values[0], values[1] = Default(), Write(Param("changed"))
	checkSQL(t, insert, `INSERT INTO "people" ("first", "last") VALUES ($1, $2)`, "first", "last")

	targets := []Expr{Col("first"), Col("last")}
	targetSelector := InsertInto("people").Targets(targets[0], targets[1:]...)
	targets[0], targets[1] = Col("changed_first"), Col("changed_last")
	insertTargets := targetSelector.Values(Write(Param("Ada")), Write(Param("Lovelace")))
	checkSQL(t, insertTargets, `INSERT INTO "people" ("first", "last") VALUES ($1, $2)`, "Ada", "Lovelace")

	rowTargets := []Expr{Col("first"), Col("last")}
	row := AssignRow(rowTargets, WriteTuple(Write(Param("Ada")), Write(Param("Lovelace"))))
	rowTargets[0], rowTargets[1] = Col("changed_first"), Col("changed_last")
	checkSQL(t, Update("people").Set(row), `UPDATE "people" SET ("first", "last") = ($1, $2)`, "Ada", "Lovelace")
}
