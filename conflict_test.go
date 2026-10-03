package qs

import (
	"errors"
	"testing"
)

func TestSetAllExcludedUsesLiteralColumnNames(t *testing.T) {
	t.Parallel()

	assignments := SetAllExcluded("name", "a.b", `a"b`)
	statement := InsertInto("settings").
		Columns("id", "name", "a.b", `a"b`).
		Values(Value(42), Value("plain"), Value("dotted"), Value("quoted")).
		OnConflict(ConflictColumns("id").DoUpdate(assignments[0], assignments[1:]...))
	checkSQL(t, statement,
		`INSERT INTO "settings" ("id", "name", "a.b", "a""b") VALUES ($1, $2, $3, $4) ON CONFLICT ("id") DO UPDATE SET "name" = "excluded"."name", "a.b" = "excluded"."a.b", "a""b" = "excluded"."a""b"`,
		42, "plain", "dotted", "quoted",
	)
}

func TestConflictUpdateSlice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		complete func([]Assignment) ConflictUpdate
		sql      string
		args     []any
	}{
		{
			name:     "columns",
			complete: ConflictColumns("id").TargetWhere(Eq("active", true)).DoUpdateSlice,
			sql:      `INSERT INTO "users" ("id") VALUES ($1) ON CONFLICT ("id") WHERE ("active" = $2) DO UPDATE SET "name" = (SELECT $3), "note" = $4 WHERE ("version" = $5)`,
			args:     []any{42, true, "live", "retained", 7},
		},
		{
			name:     "index",
			complete: ConflictIndex(IndexColumn("id")).TargetWhere(Eq("enabled", false)).DoUpdateSlice,
			sql:      `INSERT INTO "users" ("id") VALUES ($1) ON CONFLICT ("id") WHERE ("enabled" = $2) DO UPDATE SET "name" = (SELECT $3), "note" = $4 WHERE ("version" = $5)`,
			args:     []any{42, false, "live", "retained", 7},
		},
		{
			name:     "constraint",
			complete: ConflictConstraint("users_pkey").DoUpdateSlice,
			sql:      `INSERT INTO "users" ("id") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_pkey" DO UPDATE SET "name" = (SELECT $2), "note" = $3 WHERE ("version" = $4)`,
			args:     []any{42, "live", "retained", 7},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Run("owns_slice_with_live_child", func(t *testing.T) {
				t.Parallel()
				child := Select(Param("original"))
				assignments := []Assignment{
					Set("name", Write(Scalar(child))),
					Set("note", Value("retained")),
				}
				action := tc.complete(assignments).Where(Eq("version", 7))
				assignments[0] = Set("wrong", Value("replaced"))
				assignments[1] = Set("other", Value("also replaced"))
				child.RemoveColumns().Columns(Param("live"))
				statement := InsertInto("users").Columns("id").Values(Value(42)).OnConflict(action)
				checkSQL(t, statement, tc.sql, tc.args...)
			})
			for _, invalid := range []struct {
				name        string
				assignments []Assignment
			}{
				{name: "nil"},
				{name: "empty", assignments: []Assignment{}},
				{name: "invalid_assignment", assignments: []Assignment{{}}},
			} {
				t.Run(invalid.name, func(t *testing.T) {
					t.Parallel()
					statement := InsertInto("users").Columns("id").Values(Value(42)).OnConflict(tc.complete(invalid.assignments))
					checkError(t, statement, ErrInvalid)
				})
			}
		})
	}
}

func TestConflictUpdateSliceFailurePreservesAppendPrefixes(t *testing.T) {
	t.Parallel()

	statement := InsertInto("users").Columns("id").Values(Value(42)).
		OnConflict(ConflictColumns("id").DoUpdateSlice(nil))
	sql, args, err := statement.AppendSQL([]byte("prefix "), []any{"existing"})
	if !errors.Is(err, ErrInvalid) || string(sql) != "prefix " || len(args) != 1 || args[0] != "existing" {
		t.Fatalf("empty conflict update rollback: sql=%q args=%#v err=%v", sql, args, err)
	}
}
