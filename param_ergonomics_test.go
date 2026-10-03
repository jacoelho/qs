package qs_test

import (
	"reflect"
	"testing"

	"github.com/jacoelho/qs"
)

type paramNamedText string
type paramNamedInts []int

func TestParamPreservesDynamicValueTypes(t *testing.T) {
	t.Parallel()
	var typedNil *paramNamedText
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"typed_nil", typedNil},
		{"named_scalar", paramNamedText("named")},
		{"named_slice", paramNamedInts{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sql, args, err := qs.Select(qs.Param(tc.value)).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			if sql != "SELECT $1" || !reflect.DeepEqual(args, []any{tc.value}) {
				t.Fatalf("SQL=%q args=%#v, want SELECT $1 and %#v", sql, args, []any{tc.value})
			}
		})
	}
	bind := qs.Param
	sql, args, err := qs.Select(bind(nil)).ToSQL()
	if err != nil || sql != "SELECT $1" || !reflect.DeepEqual(args, []any{nil}) {
		t.Fatalf("Param function value: SQL=%q args=%#v err=%v", sql, args, err)
	}
}
