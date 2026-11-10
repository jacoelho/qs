package qs

import (
	"errors"
	"reflect"
	"testing"
)

type renderCase struct {
	name      string
	statement Statement
	sql       string
	args      []any
}

func checkSQL(t *testing.T, s Statement, sql string, args ...any) {
	t.Helper()
	got, values, err := s.ToSQL()
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if got != sql {
		t.Fatalf("SQL\n got: %s\nwant: %s", got, sql)
	}
	if !reflect.DeepEqual(values, args) {
		t.Fatalf("arguments\n got: %#v\nwant: %#v", values, args)
	}
	again, values2, err := s.ToSQL()
	if err != nil || again != got || !reflect.DeepEqual(values, values2) {
		t.Fatal("rendering mutated statement")
	}
}
func runCases(t *testing.T, cases []renderCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkSQL(t, tc.statement, tc.sql, tc.args...)
		})
	}
}
func checkError(t *testing.T, s Statement, cause error) {
	t.Helper()
	sql, args, err := ToSQL(s)
	if !errors.Is(err, cause) || sql != "" || args != nil {
		t.Fatalf("expected atomic %v error, got %q %#v %v", cause, sql, args, err)
	}
	if _, ok := errors.AsType[*RenderError](err); !ok {
		t.Fatal("missing RenderError")
	}
}

func checkErrorWithOptions(t *testing.T, s Statement, options Options, cause error) {
	t.Helper()
	sql, args, err := ToSQLWith(s, options)
	if !errors.Is(err, cause) || sql != "" || args != nil {
		t.Fatalf("expected atomic %v error, got %q %#v %v", cause, sql, args, err)
	}
}
