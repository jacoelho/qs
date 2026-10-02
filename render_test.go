package qs

import (
	"errors"
	"reflect"
	"testing"
)

func placeholderQuery() *SelectBuilder {
	child := Select(Param("nested")).Where(Eq("id", 42))
	doc := JSONBCol("payload")
	return Select(
		Ident(`odd"column?;`),
		LiteralString("quote'; ? $1 \\end"),
		UnsafeSQL(`'?; $1' /* ?; $1 */`),
		Scalar(child).As("nested"),
		doc.Key("profile").TextKey("name").As("name"),
	).With(CTE("seed", Select(Param("seed"))).Columns("value")).
		FromExpr(child.As("again")).
		Where(doc.HasKey("?;"), doc.HasAnyKeys("red", "blue"),
			doc.HasAllKeys("alpha", "beta"), doc.PathExists("$.a ? (@ > 1)"),
			Eq("name", `x'); DROP TABLE t;--`)).Limit(3)
}

func TestPlaceholderStyles(t *testing.T) {
	t.Parallel()

	query := placeholderQuery()
	args := []any{"seed", "nested", 42, "profile", "name", "nested", 42,
		"?;", "red", "blue", "alpha", "beta", "$.a ? (@ > 1)", `x'); DROP TABLE t;--`, 3}
	cases := []struct {
		name  string
		style PlaceholderStyle
		sql   string
	}{
		{"dollar", Dollar, `WITH "seed" ("value") AS (SELECT $1) SELECT "odd""column?;", E'quote''; ? $1 \\end', '?; $1' /* ?; $1 */, (SELECT $2 WHERE ("id" = $3)) AS "nested", (("payload" -> $4) ->> $5) AS "name" FROM (SELECT $6 WHERE ("id" = $7)) AS "again" WHERE ("payload" ? $8) AND ("payload" ?| (ARRAY[$9, $10])::text[]) AND ("payload" ?& (ARRAY[$11, $12])::text[]) AND ("payload" @? ($13)::jsonpath) AND ("name" = $14) LIMIT $15`},
		{"question", Question, `WITH "seed" ("value") AS (SELECT ?) SELECT "odd""column?;", E'quote''; ? $1 \\end', '?; $1' /* ?; $1 */, (SELECT ? WHERE ("id" = ?)) AS "nested", (("payload" -> ?) ->> ?) AS "name" FROM (SELECT ? WHERE ("id" = ?)) AS "again" WHERE ("payload" ? ?) AND ("payload" ?| (ARRAY[?, ?])::text[]) AND ("payload" ?& (ARRAY[?, ?])::text[]) AND ("payload" @? (?)::jsonpath) AND ("name" = ?) LIMIT ?`},
	}
	for _, graph := range []struct {
		name  string
		query Statement
	}{{"original", query}, {"clone", query.Clone()}} {
		t.Run(graph.name, func(t *testing.T) {
			t.Parallel()

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					for range 2 {
						sql, got, err := graph.query.ToSQLWith(Options{PlaceholderStyle: tc.style})
						if err != nil || sql != tc.sql || !reflect.DeepEqual(got, args) {
							t.Fatalf("SQL=%s\nargs=%#v\nerror=%v", sql, got, err)
						}
					}
				})
			}
			// Rendering options belong to the call, so Question cannot change defaults.
			sql, got, err := graph.query.ToSQL()
			if err != nil || sql != cases[0].sql || !reflect.DeepEqual(got, args) {
				t.Fatalf("default changed: SQL=%s args=%#v error=%v", sql, got, err)
			}
		})
	}
}

func TestPlaceholderAppendPrefix(t *testing.T) {
	t.Parallel()

	query := Select(Param("value")).Where(Eq("id", 17))
	prefix := []any{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	wantArgs := []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "value", 17}
	for _, tc := range []struct {
		name  string
		style PlaceholderStyle
		sql   string
	}{
		{"dollar", Dollar, `/* ? $1 */ SELECT $10 WHERE ("id" = $11)`},
		{"question", Question, `/* ? $1 */ SELECT ? WHERE ("id" = ?)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			buf := append(make([]byte, 0, 256), "/* ? $1 */ "...)
			args := make([]any, len(prefix), 16)
			copy(args, prefix)
			sql, got, err := AppendWith(buf, args, query, Options{PlaceholderStyle: tc.style})
			if err != nil || string(sql) != tc.sql || !reflect.DeepEqual(got, wantArgs) {
				t.Fatalf("SQL=%s args=%#v error=%v", sql, got, err)
			}
			if string(buf) != "/* ? $1 */ " || !reflect.DeepEqual(args, prefix) {
				t.Fatal("rendering changed the visible prefix")
			}
			var statement Statement = query
			sql, got, err = statement.AppendWith(buf, args, Options{PlaceholderStyle: tc.style})
			if err != nil || string(sql) != tc.sql || !reflect.DeepEqual(got, wantArgs) {
				t.Fatalf("method: SQL=%s args=%#v error=%v", sql, got, err)
			}
		})
	}
	var nilSelect *SelectBuilder
	sql, args, err := nilSelect.AppendWith(nil, nil, Options{PlaceholderStyle: Question})
	if !errors.Is(err, ErrInvalid) || sql != nil || args != nil {
		t.Fatalf("typed nil method: SQL=%s args=%#v error=%v", sql, args, err)
	}
}

func TestPlaceholderErrorsAreAtomic(t *testing.T) {
	t.Parallel()

	valid := Select(Param("first"), Param("second"))
	unknownQuantifier := Select(Param("second")).Prefix(Param("first"))
	unknownQuantifier.distinct = selectQuantifier(255)
	unknownCTE := CTE("c", Select(Param("second")))
	unknownCTE.materialization = cteMaterialization(255)
	unknownOverride := InsertInto("t").Values("second").Prefix(Param("first"))
	unknownOverride.overriding = overridingMode(255)
	unknownMergeOverride := NotMatched().ThenInsertValues([]string{"id"}, Param("second"))
	unknownMergeOverride.overriding = overridingMode(255)
	for _, style := range []PlaceholderStyle{2, 255} {
		_, _, err := valid.ToSQLWith(Options{PlaceholderStyle: style})
		detail, ok := errors.AsType[*RenderError](err)
		if !errors.Is(err, ErrInvalid) || !ok || detail.Clause != "options" {
			t.Fatalf("invalid style %d: %v", style, err)
		}
	}
	for _, tc := range []struct {
		name     string
		query    Statement
		options  Options
		cause    error
		appended int
	}{
		{"invalid_version", valid, Options{PostgreSQL: PostgreSQLVersion(19)}, ErrInvalid, 0},
		{"invalid_style", valid, Options{PlaceholderStyle: 2}, ErrInvalid, 0},
		{"late_expression", Select(Param("first"), Param("second"), Expr{}), Options{PlaceholderStyle: Question}, ErrInvalid, 2},
		{"late_grouped_expression", Select(Param("first"), Param("second"), Expr{}.Parenthesized()), Options{PlaceholderStyle: Question}, ErrInvalid, 2},
		{"late_function_cast_type", Select(Param("first")).FromExpr(TableFunc(Param("second").Cast(DataType{}))), Options{PlaceholderStyle: Question}, ErrInvalid, 2},
		{"unknown_select_quantifier", unknownQuantifier, Options{PlaceholderStyle: Question}, ErrInvalid, 1},
		{"unknown_cte_materialization", Select(Param("third")).Prefix(Param("first")).With(unknownCTE), Options{PlaceholderStyle: Question}, ErrInvalid, 1},
		{"unknown_insert_overriding", unknownOverride, Options{PlaceholderStyle: Question}, ErrInvalid, 1},
		{"unknown_merge_overriding", MergeInto("t").Using(Table("s")).On(Eq("t.id", "first")).When(unknownMergeOverride), Options{PlaceholderStyle: Question}, ErrInvalid, 1},
		{"cycle_keyword", cycleValuesQuery(CurrentDate(), LiteralInt(0)), Options{PlaceholderStyle: Question}, ErrInvalid, 1},
		{"parameter_limit", valid, Options{PlaceholderStyle: Question, MaxParameters: 10}, ErrParameterLimit, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			buf := append(make([]byte, 0, 256), "prefix: "...)
			backing := make([]any, 16)
			prefix := []any{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
			copy(backing, prefix)
			for i := 9; i < len(backing); i++ {
				backing[i] = "untouched"
			}
			args := backing[:9]
			var sql []byte
			var got []any
			var err error
			if tc.name == "late_expression" {
				sql, got, err = tc.query.AppendWith(buf, args, tc.options)
			} else {
				sql, got, err = AppendWith(buf, args, tc.query, tc.options)
			}
			if !errors.Is(err, tc.cause) || string(sql) != "prefix: " || !reflect.DeepEqual(got, prefix) || len(got) != 9 {
				t.Fatalf("rollback: SQL=%s args=%#v error=%v", sql, got, err)
			}
			if &sql[0] != &buf[0] || &got[0] != &args[0] {
				t.Fatal("rollback did not return the caller's slices")
			}
			for i := 9; i < 9+tc.appended; i++ {
				if backing[i] != nil {
					t.Fatalf("appended argument slot %d retained %v", i, backing[i])
				}
			}
			options := tc.options
			if errors.Is(tc.cause, ErrParameterLimit) {
				options.MaxParameters = 1
			}
			owned, ownedArgs, err := tc.query.ToSQLWith(options)
			if !errors.Is(err, tc.cause) || owned != "" || ownedArgs != nil {
				t.Fatalf("owned rollback: SQL=%s args=%#v error=%v", owned, ownedArgs, err)
			}
		})
	}
}

func TestPlaceholderReusableAppendAllocations(t *testing.T) {
	query := Select(Param("value")).Where(JSONBCol("payload").HasKey("name"))
	for _, tc := range []struct {
		name  string
		style PlaceholderStyle
		sql   string
	}{
		{"dollar", Dollar, `SELECT $1 WHERE ("payload" ? $2)`},
		{"question", Question, `SELECT ? WHERE ("payload" ? ?)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := Options{PlaceholderStyle: tc.style}
			buf, args := make([]byte, 0, 256), make([]any, 0, 8)
			buf, args, err := query.AppendWith(buf, args, options)
			if err != nil || string(buf) != tc.sql || !reflect.DeepEqual(args, []any{"value", "name"}) {
				t.Fatalf("SQL=%s args=%#v error=%v", buf, args, err)
			}
			allocations := testing.AllocsPerRun(100, func() {
				buf, args, err = query.AppendWith(buf[:0], args[:0], options)
			})
			if err != nil || allocations != 0 {
				t.Fatalf("warmed rendering: %g allocations, error=%v", allocations, err)
			}
		})
	}
}
