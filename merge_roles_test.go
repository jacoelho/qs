package qs

import (
	"errors"
	"testing"
)

func TestMergeRoleActions(t *testing.T) {
	t.Parallel()

	base := func(branch MergeWhen) Statement {
		return MergeInto("target").Using(Table("source")).On(EqColumns("target.id", "source.id")).When(branch)
	}
	for _, tc := range []struct {
		name string
		stmt Statement
		sql  string
		args []any
	}{
		{
			name: "matched_update",
			stmt: base(Matched().ThenUpdate(Set("value", Write(Param(1))))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN MATCHED THEN UPDATE SET "value" = $1`,
			args: []any{1},
		},
		{
			name: "matched_delete",
			stmt: base(Matched().ThenDelete()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN MATCHED THEN DELETE`,
		},
		{
			name: "matched_nothing",
			stmt: base(Matched().ThenDoNothing()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN MATCHED THEN DO NOTHING`,
		},
		{
			name: "not_matched_assignments",
			stmt: base(NotMatched().ThenInsert(Set("id", Write(Param(1))), Set("value", Write(Param(2))))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED THEN INSERT ("id", "value") VALUES ($1, $2)`,
			args: []any{1, 2},
		},
		{
			name: "not_matched_values",
			stmt: base(NotMatched().ThenInsertValues([]string{"id"}, Write(Param(1)))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED THEN INSERT ("id") VALUES ($1)`,
			args: []any{1},
		},
		{
			name: "not_matched_default",
			stmt: base(NotMatched().ThenInsertDefault()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED THEN INSERT DEFAULT VALUES`,
		},
		{
			name: "not_matched_by_target",
			stmt: base(NotMatchedByTarget().ThenDoNothing()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED BY TARGET THEN DO NOTHING`,
		},
		{
			name: "not_matched_by_source_update",
			stmt: base(NotMatchedBySource().ThenUpdate(Set("value", Write(Param(1))))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED BY SOURCE THEN UPDATE SET "value" = $1`,
			args: []any{1},
		},
		{
			name: "not_matched_by_source_delete",
			stmt: base(NotMatchedBySource().ThenDelete()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED BY SOURCE THEN DELETE`,
		},
		{
			name: "not_matched_by_source_nothing",
			stmt: base(NotMatchedBySource().ThenDoNothing()),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED BY SOURCE THEN DO NOTHING`,
		},
		{
			name: "override_system_assignments",
			stmt: base(NotMatched().OverridingSystemValue().ThenInsert(Set("id", Write(Param(1))))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED THEN INSERT ("id") OVERRIDING SYSTEM VALUE VALUES ($1)`,
			args: []any{1},
		},
		{
			name: "override_user_values",
			stmt: base(NotMatched().OverridingUserValue().ThenInsertValues([]string{"id"}, Write(Param(1)))),
			sql:  `MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN NOT MATCHED THEN INSERT ("id") OVERRIDING USER VALUE VALUES ($1)`,
			args: []any{1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, tc.stmt, tc.sql, tc.args...)
		})
	}
}

func TestMergeRoleConditionsAndSiblingImmutability(t *testing.T) {
	t.Parallel()

	matched := Matched().And(Eq("source.state", "open"))
	notMatched := NotMatched().And(Eq("source.state", "new"))
	override := notMatched.OverridingSystemValue()
	query := MergeInto("target").Using(Table("source")).On(EqColumns("target.id", "source.id")).When(
		matched.ThenUpdate(Set("value", Write(Param(1)))),
		matched.ThenDelete(),
		notMatched.ThenInsert(Set("id", Write(Param(2)))),
		notMatched.ThenDoNothing(),
		override.ThenInsertValues([]string{"id"}, Write(Param(3))),
	)
	checkSQL(t, query,
		`MERGE INTO "target" USING "source" ON ("target"."id" = "source"."id") WHEN MATCHED AND ("source"."state" = $1) THEN UPDATE SET "value" = $2 WHEN MATCHED AND ("source"."state" = $3) THEN DELETE WHEN NOT MATCHED AND ("source"."state" = $4) THEN INSERT ("id") VALUES ($5) WHEN NOT MATCHED AND ("source"."state" = $6) THEN DO NOTHING WHEN NOT MATCHED AND ("source"."state" = $7) THEN INSERT ("id") OVERRIDING SYSTEM VALUE VALUES ($8)`,
		"open", 1, "open", "new", 2, "new", "new", 3,
	)
}

func TestMergeZeroRoleRemainsInvalid(t *testing.T) {
	t.Parallel()

	base := func(branch MergeWhen) Statement {
		return MergeInto("target").Using(Table("source")).On(True()).When(branch)
	}
	for _, tc := range []struct {
		name   string
		branch MergeWhen
	}{
		{name: "matched", branch: (MergeMatched{}).ThenDelete()},
		{name: "not_matched", branch: (MergeNotMatched{}).ThenInsertDefault()},
		{name: "override", branch: (MergeInsertOverride{}).ThenInsert(Set("id", Write(Param(1))))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := base(tc.branch).ToSQL(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("zero selector accepted: %v", err)
			}
		})
	}
}
