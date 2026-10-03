package qs_test

import (
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jacoelho/qs"
)

type embeddedWriteValue struct {
	qs.WriteValue
	calls atomic.Int32
}

func (v *embeddedWriteValue) Value() (driver.Value, error) {
	v.calls.Add(1)
	return "embedded-write-value", nil
}

type embeddedWriteRowValue struct {
	qs.WriteRowValue
	calls atomic.Int32
}

func (v *embeddedWriteRowValue) Value() (driver.Value, error) {
	v.calls.Add(1)
	return "embedded-write-row-value", nil
}

func boundWriteConstructors() []struct {
	name string
	bind func(any) qs.WriteValue
} {
	return []struct {
		name string
		bind func(any) qs.WriteValue
	}{
		{name: "write_param", bind: func(v any) qs.WriteValue { return qs.Write(qs.Param(v)) }},
		{name: "value", bind: qs.Value},
	}
}

func TestParamRejectsWriteDescriptorsAtExternalBoundary(t *testing.T) {
	t.Parallel()
	write := qs.Default()
	row := qs.WriteRow(qs.Write(qs.Param(1)))
	var nilWrite *qs.WriteValue
	var nilRow *qs.WriteRowValue
	cases := []struct {
		name  string
		value any
	}{
		{name: "write", value: write},
		{name: "row", value: row},
		{name: "write_pointer", value: &write},
		{name: "row_pointer", value: &row},
		{name: "typed_nil_write_pointer", value: nilWrite},
		{name: "typed_nil_row_pointer", value: nilRow},
		{name: "expression", value: qs.Param(1)},
		{name: "statement", value: qs.Select(qs.Param(1))},
		{name: "null", value: qs.NullOf[int]()},
		{name: "optional", value: qs.Some(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			statement := qs.Select(qs.Param(tc.value))
			sql, args, err := statement.ToSQL()
			if !errors.Is(err, qs.ErrInvalid) || sql != "" || args != nil {
				t.Fatalf("descriptor boundary: sql=%q args=%#v err=%v", sql, args, err)
			}
			for _, constructor := range boundWriteConstructors() {
				t.Run(constructor.name, func(t *testing.T) {
					statement := qs.InsertInto("events").Values(constructor.bind(tc.value))
					sql, args, err := statement.ToSQL()
					if !errors.Is(err, qs.ErrInvalid) || sql != "" || args != nil {
						t.Fatalf("write boundary: sql=%q args=%#v err=%v", sql, args, err)
					}
				})
			}
		})
	}
}

func TestParamDescriptorRejectionRollsBackEarlierOutput(t *testing.T) {
	t.Parallel()
	write := qs.Default()
	statements := []qs.Statement{qs.Select(qs.Param("first"), qs.Param(&write))}
	for _, constructor := range boundWriteConstructors() {
		statements = append(statements, qs.InsertInto("events").Values(constructor.bind("first"), constructor.bind(&write)))
	}
	for _, statement := range statements {
		prefix := []byte("prefix ")
		args := []any{"existing"}
		sql, gotArgs, err := statement.AppendSQL(prefix, args)
		if !errors.Is(err, qs.ErrInvalid) || string(sql) != string(prefix) || len(gotArgs) != 1 || gotArgs[0] != args[0] {
			t.Fatalf("descriptor rollback: sql=%q args=%#v err=%v", sql, gotArgs, err)
		}
		if len(sql) > 0 && &sql[0] != &prefix[0] {
			t.Fatal("descriptor rollback allocated a replacement SQL slice")
		}
		if len(gotArgs) > 0 && &gotArgs[0] != &args[0] {
			t.Fatal("descriptor rollback allocated a replacement args slice")
		}
	}
}

func TestExternalEmbeddedWriteDescriptorsRemainDriverValues(t *testing.T) {
	t.Parallel()
	write := &embeddedWriteValue{WriteValue: qs.Write(qs.Param("payload"))}
	row := &embeddedWriteRowValue{WriteRowValue: qs.WriteRow(qs.Write(qs.Param("payload")))}
	cases := []struct {
		name   string
		driver any
		calls  func() int32
	}{
		{name: "write", driver: write, calls: func() int32 { return write.calls.Load() }},
		{name: "row", driver: row, calls: func() int32 { return row.calls.Load() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, constructor := range boundWriteConstructors() {
				t.Run(constructor.name, func(t *testing.T) {
					statement := qs.InsertInto("events").Values(constructor.bind(tc.driver))
					if got := tc.calls(); got != 0 {
						t.Fatalf("Value called during construction: %d", got)
					}
					sql, args, err := statement.ToSQL()
					if err != nil {
						t.Fatal(err)
					}
					if sql != `INSERT INTO "events" VALUES ($1)` || len(args) != 1 || args[0] != tc.driver {
						t.Fatalf("rendered driver: sql=%q args=%#v", sql, args)
					}
					if got := tc.calls(); got != 0 {
						t.Fatalf("Value called during render: %d", got)
					}
					clone := statement.Clone()
					cloneSQL, cloneArgs, err := clone.ToSQL()
					if err != nil {
						t.Fatal(err)
					}
					if cloneSQL != sql || len(cloneArgs) != 1 || cloneArgs[0] != tc.driver {
						t.Fatalf("cloned driver: sql=%q args=%#v", cloneSQL, cloneArgs)
					}
					if got := tc.calls(); got != 0 {
						t.Fatalf("Value called during clone/render: %d", got)
					}
				})
			}
		})
	}
}
