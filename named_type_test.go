package qs

import (
	"errors"
	"testing"
)

func TestNamedTypeRendering(t *testing.T) {
	t.Parallel()

	parts := []string{"billing", "state"}
	modifiers := []int{3, 1}
	typ := NamedType(parts...).Modifiers(modifiers...)
	parts[0] = "changed"
	modifiers[0] = 99

	runCases(t, []renderCase{
		{"qualified", Select(Param("paid").Cast(NamedType("billing", "state"))), `SELECT ($1)::"billing"."state"`, []any{"paid"}},
		{"custom_modifiers", Select(Param("paid").Cast(typ)), `SELECT ($1)::"billing"."state"(3, 1)`, []any{"paid"}},
		{"numeric_precision", Select(Param("12.34").Cast(NamedType("pg_catalog", "numeric").Modifiers(5))), `SELECT ($1)::"pg_catalog"."numeric"(5)`, []any{"12.34"}},
		{"numeric_scale", Select(Param("12.345").Cast(NamedType("pg_catalog", "numeric").Modifiers(5, 2))), `SELECT ($1)::"pg_catalog"."numeric"(5, 2)`, []any{"12.345"}},
		{"negative_numeric_scale", Select(Param("1200").Cast(NamedType("pg_catalog", "numeric").Modifiers(5, -2))), `SELECT ($1)::"pg_catalog"."numeric"(5, -2)`, []any{"1200"}},
		{"varchar_length", Select(Param("paid").Cast(NamedType("pg_catalog", "varchar").Modifiers(12))), `SELECT ($1)::"pg_catalog"."varchar"(12)`, []any{"paid"}},
		{"timestamp_precision", Select(Param("2026-01-01 12:00:00").Cast(NamedType("pg_catalog", "timestamp").Modifiers(6))), `SELECT ($1)::"pg_catalog"."timestamp"(6)`, []any{"2026-01-01 12:00:00"}},
		{"array", Select(Param("paid").Cast(ArrayType(NamedType("billing", "state").Modifiers(3)))), `SELECT ($1)::"billing"."state"(3)[]`, []any{"paid"}},
	})
	checkSQL(t, Select(Param("paid").Cast(typ)), `SELECT ($1)::"billing"."state"(3, 1)`, "paid")
	checkSQL(t, Select(Param("paid").Cast(ArrayType(typ))), `SELECT ($1)::"billing"."state"(3, 1)[]`, "paid")
}

func TestNamedTypeValidation(t *testing.T) {
	t.Parallel()

	cast := func(typ DataType) Statement { return Select(Param(1).Cast(typ)) }
	checkError(t, cast(NamedType()), ErrInvalid)
	checkError(t, cast(NamedType("", "state")), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "numeric").Modifiers()), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "varchar").Modifiers(0)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "varchar").Modifiers(1, 2)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "numeric").Modifiers(0)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "numeric").Modifiers(1001)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "numeric").Modifiers(5, -1001)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "time").Modifiers(-1)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "time").Modifiers(7)), ErrInvalid)
	checkError(t, cast(NamedType("pg_catalog", "time").Modifiers(0, 1)), ErrInvalid)
}

func TestNamedTypeVersionValidation(t *testing.T) {
	t.Parallel()

	negativeScale := Select(Param("1234").Cast(NamedType("pg_catalog", "numeric").Modifiers(5, -1)))
	tooLargeScale := Select(Param("12.345").Cast(NamedType("pg_catalog", "numeric").Modifiers(5, 6)))
	for _, tc := range []struct {
		name string
		stmt Statement
	}{
		{"negative_scale", negativeScale},
		{"scale_exceeds_precision", tooLargeScale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sql, args, err := tc.stmt.ToSQLWith(Options{PostgreSQL: PostgreSQL14})
			if !errors.Is(err, ErrUnsupported) || sql != "" || args != nil {
				t.Fatalf("expected PostgreSQL 14 rejection, got %q %#v %v", sql, args, err)
			}
		})
	}
	checkSQL(t, negativeScale, `SELECT ($1)::"pg_catalog"."numeric"(5, -1)`, "1234")
	checkSQL(t, tooLargeScale, `SELECT ($1)::"pg_catalog"."numeric"(5, 6)`, "12.345")
	if sql, args, err := negativeScale.ToSQLWith(Options{PostgreSQL: PostgreSQL15}); err != nil || sql != `SELECT ($1)::"pg_catalog"."numeric"(5, -1)` || len(args) != 1 || args[0] != "1234" {
		t.Fatalf("PostgreSQL 15 rendering: %q %#v %v", sql, args, err)
	}
}
