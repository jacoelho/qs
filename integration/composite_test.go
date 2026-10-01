//go:build postgres

package integration_test

import (
	"testing"

	"github.com/jacoelho/qx"
)

func TestCompositeFieldsAndUpdates(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `
		DROP TABLE IF EXISTS qx_composite_people;
		DROP TYPE IF EXISTS qx_composite_person CASCADE;
		CREATE TYPE qx_composite_person AS (
			first_name text,
			last_name text,
			age integer
		);
		CREATE TEMP TABLE qx_composite_people (
			id integer PRIMARY KEY,
			person qx_composite_person NOT NULL DEFAULT ROW('', '', 0)::qx_composite_person,
			note text
		);
		INSERT INTO qx_composite_people VALUES
			(1, ROW('Ada', 'Lovelace', 36)::qx_composite_person, 'keep');`); err != nil {
		t.Fatal(err)
	}
	person := qx.Col("person")
	read := qx.Select(
		person.Field("first_name"),
		person.Field("last_name"),
		person.Field("age"),
		qx.Col("note"),
	).From("qx_composite_people").Where(qx.Eq("id", 1))
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

	expanded := qx.Select(person.Fields()).From("qx_composite_people").Where(qx.Eq("id", 1))
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

	update := qx.Update("qx_composite_people").Set(
		qx.Assign(person.Field("first_name"), qx.Param("Grace")),
		qx.Assign(person.Field("age"), qx.Param(37)),
	).Where(qx.Eq("id", 1))
	sql, args, err = update.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if err := conn.QueryRow(ctx, `SELECT (person).first_name, (person).last_name, (person).age, note FROM qx_composite_people WHERE id = 1`).Scan(&first, &last, &age, &note); err != nil {
		t.Fatal(err)
	}
	if first != "Grace" || last != "Lovelace" || age != 37 || note != "keep" {
		t.Fatalf("updated first=%q last=%q age=%d note=%q", first, last, age, note)
	}

	insert := qx.InsertInto("qx_composite_people").Targets(
		qx.Col("id"),
		person.Field("first_name"),
		person.Field("age"),
	).Values(2, "Alan", 41)
	sql, args, err = insert.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	var insertedLast *string
	if err := conn.QueryRow(ctx, `SELECT (person).first_name, (person).last_name, (person).age FROM qx_composite_people WHERE id = 2`).Scan(&first, &insertedLast, &age); err != nil {
		t.Fatal(err)
	}
	if first != "Alan" || insertedLast != nil || age != 41 {
		t.Fatalf("inserted first=%q last=%v age=%d", first, insertedLast, age)
	}
}

func TestNamedTypeCastChangesResult(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	numeric := func(scale int) qx.Expr {
		return qx.Param("12.345").Cast(qx.NamedType("pg_catalog", "numeric").Modifiers(5, scale)).Cast(qx.Text)
	}
	query := qx.Select(numeric(2), numeric(1))
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
