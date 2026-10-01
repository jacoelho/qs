package qx

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
	// Clone is a separate graph with identical behaviour.
	copied, values3, err := Clone(s).ToSQL()
	if err != nil || copied != got || !reflect.DeepEqual(values, values3) {
		t.Fatalf("clone mismatch: %s %#v %v", copied, values3, err)
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
