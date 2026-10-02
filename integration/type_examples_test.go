//go:build postgres

package integration_test

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jacoelho/qs"
)

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestUUIDTypeExamples(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	const text = "550e8400-e29b-41d4-a716-446655440000"
	want := pgtype.UUID{
		Bytes: [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4, 0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00},
		Valid: true,
	}
	var native pgtype.UUID
	if err := native.Scan(text); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		value any
		want  pgtype.UUID
	}{
		{"string_cast", text, want},
		{"native_uuid", native, want},
		{"null_uuid", pgtype.UUID{Bytes: want.Bytes, Valid: false}, pgtype.UUID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args, err := qs.Select(qs.Param(tc.value).Cast(qs.TypeUUID)).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			if query != "SELECT ($1)::uuid" || len(args) != 1 || !reflect.DeepEqual(args[0], tc.value) {
				t.Fatalf("rendered query=%q args=%#v", query, args)
			}
			var result pgtype.UUID
			if err := conn.QueryRow(ctx, query, args...).Scan(&result); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			if result != tc.want {
				t.Fatalf("UUID=%#v, want %#v", result, tc.want)
			}
		})
	}
}

func TestNestedCompositeTypeExample(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `
		CREATE TYPE pet AS (name text, age integer);
		CREATE TYPE person AS (id uuid, pets pet[]);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pet", "_pet", "person"} {
		typ, err := conn.LoadType(ctx, name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		conn.TypeMap().RegisterType(typ)
	}
	var id pgtype.UUID
	if err := id.Scan("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatal(err)
	}
	person := pgtype.CompositeFields{
		id,
		pgtype.FlatArray[pgtype.CompositeFields]{
			{"Fido", int32(3)},
			{"Rex", int32(5)},
		},
	}
	query, args, err := qs.Select(qs.Param(person).Cast(qs.TypeNamed("person"))).ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if query != `SELECT ($1)::"person"` || len(args) != 1 || !reflect.DeepEqual(args[0], person) {
		t.Fatalf("rendered query=%q args=%#v", query, args)
	}
	type pet struct {
		Name string
		Age  int32
	}
	var result struct {
		ID   pgtype.UUID
		Pets []pet
	}
	if err := conn.QueryRow(ctx, query, args...).Scan(&result); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	wantID := pgtype.UUID{
		Bytes: [16]byte{0x55, 0x0e, 0x84, 0x00, 0xe2, 0x9b, 0x41, 0xd4, 0xa7, 0x16, 0x44, 0x66, 0x55, 0x44, 0x00, 0x00},
		Valid: true,
	}
	if result.ID != wantID || !reflect.DeepEqual(result.Pets, []pet{{Name: "Fido", Age: 3}, {Name: "Rex", Age: 5}}) {
		t.Fatalf("decoded composite=%#v", result)
	}
}
