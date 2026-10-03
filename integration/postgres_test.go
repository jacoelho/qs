//go:build postgres

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jacoelho/qs"
)

var connectionSequence atomic.Uint64

func connect(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("QS_TEST_DSN")
	if dsn == "" {
		t.Fatal("QS_TEST_DSN is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sequence := connectionSequence.Add(1)
	schema := "qs_test_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatUint(sequence, 36)
	schemaIdentifier := (pgx.Identifier{schema}).Sanitize()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := conn.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		if err := conn.Close(cleanupCtx); err != nil {
			t.Errorf("close test connection: %v", err)
		}
	})
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+schemaIdentifier+", pg_temp, public"); err != nil {
		t.Fatalf("set test search_path: %v", err)
	}
	var version int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 180000 {
		t.Fatalf("PostgreSQL 18 required; server_version_num=%d", version)
	}
	return ctx, conn
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestTypedJSONDocuments(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	object := qs.JSONBParam(`{"0":42,"a":null,"items":[{"name":"Ada"}]}`)
	array := qs.JSONBParam(`[{"name":"Ada"},{"name":"Grace"},"0"]`)
	cases := []struct {
		name string
		expr qs.Expr
		want string
		null bool
		json bool
	}{
		{"object_numeric_key", object.Key("0").Expr(), `42`, false, true},
		{"object_integer_index", object.Index(0).Expr(), "", true, false},
		{"array_text_key", array.Key("0").Expr(), "", true, false},
		{"array_index", array.Index(0).Expr(), `{"name":"Ada"}`, false, true},
		{"negative_index", array.Index(-1).Expr(), `"0"`, false, true},
		{"out_of_range", array.Index(99).Expr(), "", true, false},
		{"missing_key", object.Key("missing").Expr(), "", true, false},
		{"json_null", object.Key("a").Expr(), `null`, false, true},
		{"text_null", object.TextKey("a").Expr(), "", true, false},
		{"mixed_path", object.PathText("items", "0", "name").Expr(), "Ada", false, false},
		{"empty_path", object.Path().Expr(), `{"0":42,"a":null,"items":[{"name":"Ada"}]}`, false, true},
		{"json_index", qs.JSONParam(`[1,2]`).Index(-1).Expr(), `2`, false, true},
		{"json_text_index", qs.JSONParam(`[1,2]`).TextIndex(0).Expr(), `1`, false, false},
		{"json_path", qs.JSONParam(`{"a":[1]}`).Path("a", "0").Expr(), `1`, false, true},
		{"json_path_text", qs.JSONParam(`{"a":[1]}`).PathText("a", "0").Expr(), `1`, false, false},
		{"byte_document", qs.JSONBParam([]byte(`{"a":1}`)).TextKey("a").Expr(), `1`, false, false},
		{"contains", object.Contains(qs.JSONBParam(`{"0":42}`)).Expr(), "true", false, false},
		{"contained_by", qs.JSONBParam(`{"0":42}`).ContainedBy(object).Expr(), "true", false, false},
		{"does_not_contain", object.Contains(qs.JSONBParam(`{"0":43}`)).Expr(), "false", false, false},
		{"has_key", object.HasKey("0").Expr(), "true", false, false},
		{"absent_key", object.HasKey("missing").Expr(), "false", false, false},
		{"has_any", object.HasAnyKeys("0", "missing").Expr(), "true", false, false},
		{"has_all", object.HasAllKeys("0", "missing").Expr(), "false", false, false},
		{"empty_any", object.HasAnyKeys().Expr(), "false", false, false},
		{"empty_all", object.HasAllKeys().Expr(), "true", false, false},
		{"delete_key", qs.JSONBParam(`{"0":42,"a":1}`).DeleteKey("0").Expr(), `{"a":1}`, false, true},
		{"delete_string_from_array", array.DeleteKey("0").Expr(), `[{"name":"Ada"},{"name":"Grace"}]`, false, true},
		{"delete_index", array.DeleteIndex(0).Expr(), `[{"name":"Grace"},"0"]`, false, true},
		{"delete_negative_index", array.DeleteIndex(-1).Expr(), `[{"name":"Ada"},{"name":"Grace"}]`, false, true},
		{"delete_keys", object.DeleteKeys("0", "a").Expr(), `{"items":[{"name":"Ada"}]}`, false, true},
		{"delete_path", object.DeletePath("items", "0", "name").Expr(), `{"0":42,"a":null,"items":[{}]}`, false, true},
		{"concat", qs.JSONBParam(`{"a":1,"b":2}`).Concat(qs.JSONBParam(`{"a":3}`)).Expr(), `{"a":3,"b":2}`, false, true},
		{"path_exists", qs.JSONBParam(`{"a":1}`).PathExists("$.a").Expr(), "true", false, false},
		{"selection_is_not_predicate", qs.JSONBParam(`{"a":1}`).PathMatches("$.a").Expr(), "", true, false},
		{"predicate_false", qs.JSONBParam(`{"a":1}`).PathMatches("$.a > 2").Expr(), "false", false, false},
		{"predicate_has_item", qs.JSONBParam(`{"a":1}`).PathExists("$.a > 2").Expr(), "true", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args, err := qs.Select(tc.expr.Cast(qs.TypeText)).ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			var result pgtype.Text
			if err := conn.QueryRow(ctx, query, args...).Scan(&result); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			if result.Valid == tc.null {
				t.Fatalf("SQL NULL=%t; want %t", !result.Valid, tc.null)
			}
			if tc.null {
				return
			}
			if tc.json {
				var got, want any
				if err := json.Unmarshal([]byte(result.String), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %s; want %s", result.String, tc.want)
				}
			} else if result.String != tc.want {
				t.Fatalf("got %q; want %q", result.String, tc.want)
			}
		})
	}
}

func TestTypedJSONFilterAndUpdate(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qs_json_events(id int, payload jsonb);
		INSERT INTO qs_json_events VALUES (1,'{"items":[{"name":"Ada"}]}'), (2,'{"items":[{"name":"Grace"}]}')`); err != nil {
		t.Fatal(err)
	}
	doc := qs.JSONBCol("payload")
	query := qs.Update("qs_json_events").
		Set(doc.Set(doc.Concat(qs.JSONBParam(`{"active":true}`)))).
		Where(doc.Key("items").Index(0).TextKey("name").Eq("Ada")).
		Returning(doc.PathText("items", "0", "name").Expr(), doc.TextKey("active").Expr())
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var name, active string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&name, &active); err != nil {
		t.Fatal(err)
	}
	if name != "Ada" || active != "true" {
		t.Fatalf("updated name=%q active=%q", name, active)
	}
	var unchanged bool
	if err := conn.QueryRow(ctx, `SELECT NOT (payload ? 'active') FROM qs_json_events WHERE id=2`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Fatal("JSON filter updated the nonmatching row")
	}
}

func TestExactLiteralSemantics(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	query := qs.Select(
		qs.LiteralNumeric("123456789012345678901234567890.123456789").Cast(qs.TypeText),
		qs.LiteralBit("00101").Cast(qs.TypeText),
		qs.LiteralHex("Ab09").Cast(qs.TypeText),
		qs.PrefixOperator("|/", qs.LiteralInt(9)).Cast(qs.TypeText),
	)
	sql, args, err := query.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var numeric, bits, hex, root string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&numeric, &bits, &hex, &root); err != nil {
		t.Fatal(err)
	}
	if numeric != "123456789012345678901234567890.123456789" || bits != "00101" || hex != "1010101100001001" || root != "3" {
		t.Fatalf("numeric=%q bits=%q hex=%q root=%q", numeric, bits, hex, root)
	}
}

//nolint:tparallel // Subtests share one pgx.Conn, which cannot be used concurrently.
func TestExplainSerialization(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	for _, tc := range []struct {
		mode   qs.ExplainSerialization
		format string
	}{
		{qs.SerializeText, "text"},
		{qs.SerializeBinary, "binary"},
		{qs.SerializeNone, ""},
	} {
		t.Run(tc.format+"_serialization", func(t *testing.T) {
			query := qs.Explain(qs.Select(qs.LiteralInt(1))).Analyze(true).
				Serialize(tc.mode).Timing(false).Format(qs.ExplainJSON)
			sql, args, err := query.ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			var result []byte
			if err := conn.QueryRow(ctx, sql, args...).Scan(&result); err != nil {
				t.Fatal(err)
			}
			var plans []struct {
				Serialization *struct {
					Format string `json:"Format"`
				} `json:"Serialization"`
			}
			if err := json.Unmarshal(result, &plans); err != nil {
				t.Fatal(err)
			}
			if len(plans) != 1 {
				t.Fatalf("expected one explained statement: %s", result)
			}
			serialization := plans[0].Serialization
			if tc.mode == qs.SerializeNone {
				if serialization != nil {
					t.Fatalf("serialization disabled: %s", result)
				}
			} else if serialization == nil || serialization.Format != tc.format {
				t.Fatalf("expected serialization format %q: %s", tc.format, result)
			}
		})
	}
}

func queryStrings(t *testing.T, ctx context.Context, conn *pgx.Conn, s qs.Statement) []string {
	t.Helper()
	sql, args, err := s.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return values
}

func TestNullsAndArrayEncoding(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	q := qs.Select(
		qs.ParamNull(qs.NullOf[string]()).Cast(qs.TypeText),
		qs.ParamNull(qs.NonNull("value")).Cast(qs.TypeText),
		qs.Param(1).Cast(qs.TypeInt4).EqExpr(qs.NullLiteral()).Expr(),
		qs.Param(1).Cast(qs.TypeInt4).InExpr(qs.Param(2), qs.NullLiteral()).Expr(),
		qs.EqAny(qs.Param(int64(2)), qs.ArrayParam([]int64{1, 2}, qs.TypeInt8)).Expr(),
	)
	sql, args, err := q.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var absent, present pgtype.Text
	var eq, in pgtype.Bool
	var member bool
	if err := conn.QueryRow(ctx, sql, args...).Scan(&absent, &present, &eq, &in, &member); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if absent.Valid || !present.Valid || present.String != "value" || eq.Valid || in.Valid || !member {
		t.Fatal(absent, present, eq, in, member)
	}
}

func TestPostgreSQL18Mutations(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	_, err := conn.Exec(ctx, `CREATE TEMP TABLE target(id bigint PRIMARY KEY,name text);
        CREATE TEMP TABLE source(id bigint PRIMARY KEY,name text);
        INSERT INTO target VALUES(1,'old'); INSERT INTO source VALUES(1,'new'),(2,'inserted');`)
	if err != nil {
		t.Fatal(err)
	}
	upsert := qs.InsertInto("target").Columns("id", "name").Values(qs.Write(qs.Param(int64(1))), qs.Write(qs.Param("updated"))).
		OnConflict(qs.ConflictColumns("id").DoUpdate(qs.Set("name", qs.Write(qs.Excluded("name"))))).ReturningCols("name")
	if got := queryStrings(t, ctx, conn, upsert); !reflect.DeepEqual(got, []string{"updated"}) {
		t.Fatal(got)
	}
	q := qs.MergeInto("target").Using(qs.Table("source")).On(qs.EqColumns("target.id", "source.id")).
		When(qs.Matched().ThenUpdate(qs.Set("name", qs.Write(qs.Col("source.name")))),
			qs.NotMatchedByTarget().ThenInsert(qs.Set("id", qs.Write(qs.Col("source.id"))), qs.Set("name", qs.Write(qs.Col("source.name"))))).
		Returning(qs.MergeAction())
	got := queryStrings(t, ctx, conn, q)
	actions := map[string]int{}
	for _, a := range got {
		actions[a]++
	}
	if actions["INSERT"] != 1 || actions["UPDATE"] != 1 || len(got) != 2 {
		t.Fatal(got)
	}
	oldNew := qs.Update("target").Set(qs.Set("name", qs.Write(qs.Param("latest")))).Where(qs.Eq("id", int64(1))).Returning(qs.Old("name"), qs.New("name"))
	sql, args, err := oldNew.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var oldName, newName string
	if err := conn.QueryRow(ctx, sql, args...).Scan(&oldName, &newName); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if oldName != "new" || newName != "latest" {
		t.Fatal(oldName, newName)
	}
}

func TestWriteDefaultsExecuteAgainstPostgres(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	if _, err := conn.Exec(ctx, `
		CREATE TEMP TABLE write_defaults (
			id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			name text DEFAULT 'server-name',
			count integer DEFAULT 7,
			note text DEFAULT 'server-note'
		);
		CREATE TEMP TABLE merge_defaults_source (id bigint, note text);
		INSERT INTO merge_defaults_source VALUES (3, 'merge-note');`); err != nil {
		t.Fatal(err)
	}

	defaults := qs.InsertInto("write_defaults").DefaultValues().ReturningCols("name", "count", "note")
	sql, args, err := defaults.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	var name, note string
	var count int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&name, &count, &note); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if name != "server-name" || count != 7 || note != "server-note" {
		t.Fatalf("default values name=%q count=%d note=%q", name, count, note)
	}

	writeDefault := qs.InsertInto("write_defaults").Columns("name", "count").Values(qs.Default(), qs.Write(qs.Param(11))).ReturningCols("name", "count")
	sql, args, err = writeDefault.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, sql, args...).Scan(&name, &count); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if name != "server-name" || count != 11 {
		t.Fatalf("direct default name=%q count=%d", name, count)
	}

	rowDefault := qs.Update("write_defaults").Set(qs.AssignRow(
		[]qs.Expr{qs.Col("name"), qs.Col("count")},
		qs.WriteTuple(qs.Default(), qs.Write(qs.Param(23))),
	)).Where(qs.Eq("count", 11)).ReturningCols("name", "count")
	sql, args, err = rowDefault.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, sql, args...).Scan(&name, &count); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if name != "server-name" || count != 23 {
		t.Fatalf("row default name=%q count=%d", name, count)
	}

	merge := qs.MergeInto("write_defaults").Using(qs.Table("merge_defaults_source")).On(qs.EqColumns("write_defaults.id", "merge_defaults_source.id")).
		When(qs.NotMatched().ThenInsertValues([]string{"name", "note"}, qs.Default(), qs.Write(qs.Col("merge_defaults_source.note")))).
		Returning(qs.Col("write_defaults.name"), qs.Col("write_defaults.note"))
	sql, args, err = merge.ToSQL()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, sql, args...).Scan(&name, &note); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if name != "server-name" || note != "merge-note" {
		t.Fatalf("merge default name=%q note=%q", name, note)
	}
}

func TestRecursiveCTEAndSQLJSON(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	seed := qs.Select(qs.LiteralInt(1))
	step := qs.Select(qs.Col("n").Add(qs.LiteralInt(1))).From("numbers").Where(qs.Lt("n", 3))
	numbers := qs.CTE("numbers", qs.UnionAll(seed, step)).Columns("n")
	q := qs.Select(qs.Col("n").Cast(qs.TypeText)).WithRecursive(numbers).From("numbers").OrderBy(qs.Asc("n"))
	if got := queryStrings(t, ctx, conn, q); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatal(got)
	}
	json := qs.JSONValue(qs.Param(`{"name":"Ana"}`).Cast(qs.TypeJSONB), qs.LiteralString("$.name")).Returning(qs.TypeText).Expr()
	if got := queryStrings(t, ctx, conn, qs.Select(json)); !reflect.DeepEqual(got, []string{"Ana"}) {
		t.Fatal(got)
	}
	table := qs.JSONTable(qs.Param(`[{"name":"Ana"},{"name":"João"}]`).Cast(qs.TypeJSONB), "$[*]", qs.JSONOrdinality("position"), qs.JSONColumn("name", qs.TypeText).Path("$.name")).As("j")
	if got := queryStrings(t, ctx, conn, qs.SelectCols("j.name").FromExpr(table).OrderBy(qs.Asc("j.position"))); !reflect.DeepEqual(got, []string{"Ana", "João"}) {
		t.Fatal(got)
	}
}
