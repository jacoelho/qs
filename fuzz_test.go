package qs

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzIdentifier(f *testing.F) {
	for _, name := range []string{"id", `a"b`, "a.b", "*", "", "\x00", "名字", `x"; DROP TABLE y; --`} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4096 {
			t.Skip()
		}
		sql, args, err := Select(Ident(name)).ToSQL()
		valid := name != "" && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
		if !valid {
			if err == nil || sql != "" || args != nil {
				t.Fatal("invalid identifier accepted")
			}
			return
		}
		want := `SELECT "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err != nil || sql != want || len(args) != 0 {
			t.Fatalf("%q %v", sql, err)
		}
	})
}

func FuzzValueIsolation(f *testing.F) {
	for _, v := range []string{"", "normal", `'); DROP TABLE t;--`, "? $1 $$", "\x00", "名字"} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v string) {
		if len(v) > 1<<16 {
			t.Skip()
		}
		sql, args, err := SelectCols("id").From("t").Where(Eq("name", v)).ToSQL()
		if err != nil || sql != `SELECT "id" FROM "t" WHERE ("name" = $1)` || !reflect.DeepEqual(args, []any{v}) {
			t.Fatalf("value changed SQL: %q %#v %v", sql, args, err)
		}
	})
}

func FuzzNestedNumbering(f *testing.F) {
	f.Add([]byte{1, 2, 3})
	f.Add([]byte{})
	f.Add([]byte{0, 255, 34, 0, 1, 127})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32 {
			t.Skip()
		}
		q := SelectCols("id").From("t")
		expected := make([]any, 0, len(data)*2)
		for i, v := range data {
			sub := Select(LiteralInt(1)).From("s").Where(Eq("v", v))
			q.Where(Or(Eq("n", i), Exists(sub)))
			expected = append(expected, i, v)
		}
		sql, args, err := q.ToSQL()
		if err != nil || !reflect.DeepEqual(args, emptyToNil(expected)) {
			t.Fatalf("%q %#v %v", sql, args, err)
		}
		for n := range args {
			want := fmt.Sprintf("$%d", n+1)
			if !strings.Contains(sql, want) {
				t.Fatal("missing", want)
			}
		}
		// The buffer path must have identical output and preserve its prefix.
		prefix := []byte("EXPLAIN ")
		b, a, err := q.AppendSQL(append(make([]byte, 0, len(sql)+8), prefix...), nil)
		if err != nil || !bytes.Equal(b, append(prefix, sql...)) || !reflect.DeepEqual(a, args) {
			t.Fatal("append mismatch")
		}
	})
}
func emptyToNil[T any](v []T) []T {
	if len(v) == 0 {
		return nil
	}
	return v
}
