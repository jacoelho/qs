//go:build postgres

package integration_test

import (
	"testing"

	"github.com/jacoelho/qs"
)

func TestCompositeFieldsAndUpdates(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `
		DROP TABLE IF EXISTS qs_composite_people;
		DROP TYPE IF EXISTS qs_composite_person CASCADE;
		CREATE TYPE qs_composite_person AS (
			first_name text,
			last_name text,
			age integer
		);
		CREATE TEMP TABLE qs_composite_people (
			id integer PRIMARY KEY,
			person qs_composite_person NOT NULL DEFAULT ROW('', '', 0)::qs_composite_person,
			note text
		);
		INSERT INTO qs_composite_people VALUES
			(1, ROW('Ada', 'Lovelace', 36)::qs_composite_person, 'keep');`); err != nil {
		t.Fatal(err)
	}
	person := qs.Col("person")
	read := qs.Select(
		person.Field("first_name"),
		person.Field("last_name"),
		person.Field("age"),
		qs.Col("note"),
	).From("qs_composite_people").Where(qs.Eq("id", 1))
	sql, args, err := read.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var first, last, note string
	var age int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&first, &last, &age, &note); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if first != "Ada" || last != "Lovelace" || age != 36 || note != "keep" {
		t.Fatalf("read first=%q last=%q age=%d note=%q", first, last, age, note)
	}

	expanded := qs.Select(person.Fields()).From("qs_composite_people").Where(qs.Eq("id", 1))
	sql, args, err = expanded.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, sql, args...).Scan(&first, &last, &age); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if first != "Ada" || last != "Lovelace" || age != 36 {
		t.Fatalf("expanded first=%q last=%q age=%d", first, last, age)
	}

	update := qs.Update("qs_composite_people").Set(
		qs.Assign(person.Field("first_name"), qs.Write(qs.Param("Grace"))),
		qs.Assign(person.Field("age"), qs.Write(qs.Param(37))),
	).Where(qs.Eq("id", 1))
	sql, args, err = update.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if err := conn.QueryRow(ctx, `SELECT (person).first_name, (person).last_name, (person).age, note FROM qs_composite_people WHERE id = 1`).Scan(&first, &last, &age, &note); err != nil {
		t.Fatal(err)
	}
	if first != "Grace" || last != "Lovelace" || age != 37 || note != "keep" {
		t.Fatalf("updated first=%q last=%q age=%d note=%q", first, last, age, note)
	}

	insert := qs.InsertInto("qs_composite_people").Targets(
		qs.Col("id"),
		person.Field("first_name"),
		person.Field("age"),
	).Values(
		qs.Write(qs.Param(2)), qs.Write(qs.Param("Alan")), qs.Write(qs.Param(41)),
	)
	sql, args, err = insert.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	var insertedLast *string
	if err := conn.QueryRow(ctx, `SELECT (person).first_name, (person).last_name, (person).age FROM qs_composite_people WHERE id = 2`).Scan(&first, &insertedLast, &age); err != nil {
		t.Fatal(err)
	}
	if first != "Alan" || insertedLast != nil || age != 41 {
		t.Fatalf("inserted first=%q last=%v age=%d", first, insertedLast, age)
	}
}

func TestNamedTypeCastChangesResult(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	numeric := func(scale int) qs.Expr {
		return qs.Param("12.345").Cast(qs.TypeNamed("pg_catalog", "numeric").Modifiers(5, scale)).Cast(qs.TypeText)
	}
	query := qs.Select(numeric(2), numeric(1))
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var two, one string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&two, &one); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if two != "12.35" || one != "12.3" {
		t.Fatalf("numeric casts two=%q one=%q", two, one)
	}
}
