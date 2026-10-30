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

func TestConcatKeepsPairsOrdered(t *testing.T) {
	t.Parallel()

	left := projection.New(
		projection.Output(qs.Param("left"), "shared"),
		projection.Output(qs.Col("id"), "id"),
	)
	right := projection.New(projection.Output(qs.Param("right"), "shared"))
	var zero projection.Projection[string]
	parts := []projection.Projection[string]{left, zero, right, left}
	p := projection.Concat(parts...)
	parts[0] = projection.New(projection.Named(qs.Col("changed"), "changed"))
	expressions, metadata := p.Expressions(), p.Metadata()
	expressions[0], metadata[0] = qs.Col("changed"), "changed"

	checkSQL(t, qs.Select(p.Expressions()...), `SELECT $1, "id", $2, $3, "id"`, "left", "right", "left")
	if want := []string{"shared", "id", "shared", "shared", "id"}; !slices.Equal(p.Metadata(), want) {
		t.Fatalf("metadata = %v, want %v", p.Metadata(), want)
	}
	checkSQL(t, qs.Select(left.Expressions()...), `SELECT $1, "id"`, "left")
	if !slices.Equal(left.Metadata(), []string{"shared", "id"}) {
		t.Fatalf("input metadata changed: %v", left.Metadata())
	}
}

func TestConcatRetainsLiveContents(t *testing.T) {
	t.Parallel()

	child := qs.Select(qs.Param("original"))
	key := fieldKey("original")
	p := projection.Concat(
		projection.New(projection.Output(qs.Scalar(child), &key)),
		projection.New(projection.Output(qs.Param(9), (*fieldKey)(nil))),
	)
	child.RemoveColumns().Columns(qs.Param("live"))
	key = "updated"
	checkSQL(t, qs.Select(p.Expressions()...), `SELECT (SELECT $1), $2`, "live", 9)
	if got := p.Metadata(); len(got) != 2 || got[0] != &key || *got[0] != "updated" || got[1] != nil {
		t.Fatalf("metadata did not retain shallow contents: %v", got)
	}
}

func TestAppendPreservesPrefixAndOwnsPositions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		prefix   bool
		capacity int
	}{
		{"nil", false, 0},
		{"empty_reused", false, 4},
		{"prefix_reused", true, 4},
		{"prefix_grows", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := projection.New(
				projection.Output(qs.Col("id"), "id"),
				projection.Output(qs.Param(7).As("status"), "state"),
			)
			var expressions []qs.Expr
			var metadata []string
			if tc.capacity > 0 {
				expressions = make([]qs.Expr, 0, tc.capacity)
				metadata = make([]string, 0, tc.capacity)
			}
			wantSQL := `SELECT "id", $1 AS "status"`
			wantMetadata := []string{"id", "state"}
			if tc.prefix {
				expressions = append(expressions, qs.Col("prefix"))
				metadata = append(metadata, "prefix")
				wantSQL = `SELECT "prefix", "id", $1 AS "status"`
				wantMetadata = []string{"prefix", "id", "state"}
			}
			prefixLen := len(expressions)
			var expressionStorage *qs.Expr
			var metadataStorage *string
			if cap(expressions)-len(expressions) >= 2 {
				expressionStorage = &expressions[:cap(expressions)][0]
				metadataStorage = &metadata[:cap(metadata)][0]
			}
			expressions = p.AppendExpressions(expressions)
			metadata = p.AppendMetadata(metadata)
			if expressionStorage != nil && &expressions[0] != expressionStorage {
				t.Fatal("append discarded available expression capacity")
			}
			if metadataStorage != nil && &metadata[0] != metadataStorage {
				t.Fatal("append discarded available metadata capacity")
			}
			checkSQL(t, qs.Select(expressions...), wantSQL, 7)
			if !slices.Equal(metadata, wantMetadata) {
				t.Fatalf("metadata = %v, want %v", metadata, wantMetadata)
			}
			expressions[prefixLen] = qs.Col("changed")
			metadata[prefixLen] = "changed"
			checkSQL(t, qs.Select(p.Expressions()...), `SELECT "id", $1 AS "status"`, 7)
			if !slices.Equal(p.Metadata(), []string{"id", "state"}) {
				t.Fatalf("appended positions alias projection: %v", p.Metadata())
			}
		})
	}
}

func TestEmptyAppendPreservesDestination(t *testing.T) {
	t.Parallel()
	var p projection.Projection[string]
	if p.AppendExpressions(nil) != nil || p.AppendMetadata(nil) != nil {
		t.Fatal("empty append changed nil destination")
	}
	expressions := make([]qs.Expr, 1, 3)
	expressions[0] = qs.Col("prefix")
	metadata := make([]string, 1, 3)
	metadata[0] = "prefix"
	gotExpressions, gotMetadata := p.AppendExpressions(expressions), p.AppendMetadata(metadata)
	if len(gotExpressions) != 1 || cap(gotExpressions) != 3 || &gotExpressions[0] != &expressions[0] {
		t.Fatal("empty append changed expression destination")
	}
	if len(gotMetadata) != 1 || cap(gotMetadata) != 3 || &gotMetadata[0] != &metadata[0] {
		t.Fatal("empty append changed metadata destination")
	}
	checkSQL(t, qs.Select(gotExpressions...), `SELECT "prefix"`)
	if gotMetadata[0] != "prefix" {
		t.Fatal("empty append changed prefix")
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
	for _, got := range [][]*fieldKey{p.Metadata(), p.AppendMetadata(nil)} {
		if len(got) != 3 || got[0] != &key || got[1] != &key || got[2] != nil || *got[0] != "updated" {
			t.Fatalf("metadata did not retain shallow pointer values: %v", got)
		}
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
	extracted := p.AppendExpressions(nil)
	child.Where(qs.Eq("status", "draft"))
	checkSQL(t, qs.Select(p.Expressions()...),
		`SELECT (SELECT "id" FROM "documents" WHERE ("status" = $1))`, "draft")
	checkSQL(t, qs.Select(extracted...),
		`SELECT (SELECT "id" FROM "documents" WHERE ("status" = $1))`, "draft")
}

func TestEmptyAndDeferredValidation(t *testing.T) {
	t.Parallel()

	var zero projection.Projection[string]
	for _, p := range []projection.Projection[string]{
		zero, projection.New[string](), zero.With(),
		projection.Concat[string](), projection.Concat(zero, projection.New[string]()),
	} {
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

func checkSQL(tb testing.TB, statement qs.Statement, want string, wantArgs ...any) {
	tb.Helper()
	text, args, err := qs.ToSQL(statement)
	if err != nil {
		tb.Fatalf("render: %v", err)
	}
	if text != want || !slices.Equal(args, wantArgs) {
		tb.Fatalf("SQL = %s args=%v, want %s args=%v", text, args, want, wantArgs)
	}
}
