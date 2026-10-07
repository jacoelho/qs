package projection_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/jacoelho/qs"
	"github.com/jacoelho/qs/projection"
)

type fieldKey string

func TestDeclarationOrderAndReturning(t *testing.T) {
	t.Parallel()

	p := projection.New(
		projection.Output(qs.Col("d.id").As("id"), fieldKey("document-id")),
		projection.Output(qs.Param("draft").As("status"), fieldKey("state")),
	)
	checkSQL(t, qs.Select(p.Expressions()...).FromExpr(qs.Table("documents").As("d")),
		`SELECT "d"."id" AS "id", $1 AS "status" FROM "documents" AS "d"`, "draft")
	if want := []fieldKey{"document-id", "state"}; !slices.Equal(p.Metadata(), want) {
		t.Fatalf("metadata = %v, want %v", p.Metadata(), want)
	}
	checkSQL(t, qs.UpdateTable(qs.Table("documents").As("d")).
		Set(qs.SetNull("archived_at")).Where(qs.Eq("d.id", "doc-1")).Returning(p.Expressions()...),
		`UPDATE "documents" AS "d" SET "archived_at" = NULL WHERE ("d"."id" = $1) RETURNING "d"."id" AS "id", $2 AS "status"`,
		"doc-1", "draft")
}

func TestNamedAndOwnedPositions(t *testing.T) {
	t.Parallel()

	input := make([]projection.Column[string], 2, 16)
	input[0] = projection.Named(qs.Col("d.id"), "id")
	input[1] = projection.Named(qs.Col("d.payload"), "payload")
	p := projection.New(input...)
	copied := p
	input[0] = projection.Named(qs.Col("changed"), "changed")
	expressions, metadata := p.Expressions(), p.Metadata()
	expressions[0] = qs.Col("changed")
	metadata[1] = "changed"
	for _, candidate := range []projection.Projection[string]{p, copied} {
		checkSQL(t, qs.Select(candidate.Expressions()...), `SELECT "d"."id" AS "id", "d"."payload" AS "payload"`)
		if !slices.Equal(candidate.Metadata(), []string{"id", "payload"}) {
			t.Fatalf("metadata changed: %v", candidate.Metadata())
		}
	}
}

func TestWithKeepsBaseAndSiblingsIndependent(t *testing.T) {
	t.Parallel()

	base := projection.New(projection.Named(qs.Col("id"), "id"))
	base = base.With(projection.Named(qs.Col("name"), "name"))
	base = base.With(projection.Named(qs.Col("status"), "status"))
	copied := base
	more := []projection.Column[string]{projection.Named(qs.Col("email"), "email")}
	email := base.With(more...)
	more[0] = projection.Named(qs.Col("changed"), "changed")
	phone := copied.With(projection.Named(qs.Col("phone"), "phone"))

	for _, candidate := range []projection.Projection[string]{base, copied, base.With()} {
		checkSQL(t, qs.Select(candidate.Expressions()...),
			`SELECT "id" AS "id", "name" AS "name", "status" AS "status"`)
		if !slices.Equal(candidate.Metadata(), []string{"id", "name", "status"}) {
			t.Fatalf("base metadata changed: %v", candidate.Metadata())
		}
	}
	checkSQL(t, qs.Select(email.Expressions()...),
		`SELECT "id" AS "id", "name" AS "name", "status" AS "status", "email" AS "email"`)
	if !slices.Equal(email.Metadata(), []string{"id", "name", "status", "email"}) {
		t.Fatalf("email metadata = %v", email.Metadata())
	}
	checkSQL(t, qs.Select(phone.Expressions()...),
		`SELECT "id" AS "id", "name" AS "name", "status" AS "status", "phone" AS "phone"`)
	if !slices.Equal(phone.Metadata(), []string{"id", "name", "status", "phone"}) {
		t.Fatalf("phone metadata = %v", phone.Metadata())
	}
}

func TestMetadataRemainsShallowAndOpaque(t *testing.T) {
	t.Parallel()

	key := fieldKey("id")
	p := projection.New(
		projection.Output(qs.Col("id"), &key),
		projection.Output(qs.Col("other_id"), &key),
		projection.Output(qs.Col("status"), (*fieldKey)(nil)),
	)
	metadata := p.Metadata()
	metadata[0] = nil
	key = "updated"
	got := p.Metadata()
	if len(got) != 3 || got[0] != &key || got[1] != &key || got[2] != nil || *got[0] != "updated" {
		t.Fatalf("metadata did not retain shallow pointer values: %v", got)
	}

	callback := func() { t.Fatal("projection invoked metadata") }
	callbacks := projection.New(projection.Output(qs.Col("id"), callback)).With(
		projection.Output(qs.Col("status"), (func())(nil)),
	)
	checkSQL(t, qs.Select(callbacks.Expressions()...), `SELECT "id", "status"`)
	if got := callbacks.Metadata(); len(got) != 2 || got[0] == nil || got[1] != nil {
		t.Fatal("function metadata was changed")
	}
	zeros := projection.New(projection.Output(qs.Col("id"), fieldKey("")))
	if got := zeros.Metadata(); !slices.Equal(got, []fieldKey{""}) {
		t.Fatalf("zero metadata = %v", got)
	}
}

func TestNestedQueriesStayLive(t *testing.T) {
	t.Parallel()

	child := qs.SelectCols("id").From("documents")
	p := projection.New(projection.Output(qs.Scalar(child), fieldKey("id")))
	child.Where(qs.Eq("status", "draft"))
	checkSQL(t, qs.Select(p.Expressions()...),
		`SELECT (SELECT "id" FROM "documents" WHERE ("status" = $1))`, "draft")
}

func TestEmptyAndDeferredValidation(t *testing.T) {
	t.Parallel()

	var zero projection.Projection[string]
	for _, p := range []projection.Projection[string]{zero, projection.New[string](), zero.With()} {
		if len(p.Expressions()) != 0 || len(p.Metadata()) != 0 {
			t.Fatal("empty projection has content")
		}
		_, _, err := qs.Select(p.Expressions()...).ToSQL()
		if !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("empty SELECT error = %v, want ErrInvalid", err)
		}
		checkSQL(t, qs.Update("documents").Set(qs.SetNull("archived_at")).Returning(p.Expressions()...),
			`UPDATE "documents" SET "archived_at" = NULL`)
	}
	checkSQL(t, qs.Select(zero.With(projection.Named(qs.Col("id"), "id")).Expressions()...),
		`SELECT "id" AS "id"`)

	invalid := []projection.Projection[string]{
		projection.New(projection.Column[string]{}),
		projection.New(projection.Named(qs.Col("id"), "")),
		projection.New(projection.Named(qs.Col("id"), "bad\x00alias")),
	}
	for _, p := range invalid {
		_, _, err := qs.Select(p.Expressions()...).ToSQL()
		if !errors.Is(err, qs.ErrInvalid) {
			t.Fatalf("render error = %v, want ErrInvalid", err)
		}
	}
}

func checkSQL(t *testing.T, statement qs.Statement, want string, wantArgs ...any) {
	t.Helper()
	text, args, err := qs.ToSQL(statement)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if text != want || !slices.Equal(args, wantArgs) {
		t.Fatalf("SQL = %s args=%v, want %s args=%v", text, args, want, wantArgs)
	}
}
