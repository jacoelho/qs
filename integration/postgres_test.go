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
	"github.com/jacoelho/qx"
)

var connectionSequence atomic.Uint64

func connect(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	dsn := os.Getenv("QX_TEST_DSN")
	if dsn == "" {
		t.Fatal("QX_TEST_DSN is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sequence := connectionSequence.Add(1)
	schema := "qx_test_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatUint(sequence, 36)
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

func TestTypedJSONDocuments(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	object := qx.JSONBParam(`{"0":42,"a":null,"items":[{"name":"Ada"}]}`)
	array := qx.JSONBParam(`[{"name":"Ada"},{"name":"Grace"},"0"]`)
	cases := []struct {
		name string
		expr qx.Expr
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
		{"json_index", qx.JSONParam(`[1,2]`).Index(-1).Expr(), `2`, false, true},
		{"json_text_index", qx.JSONParam(`[1,2]`).TextIndex(0).Expr(), `1`, false, false},
		{"json_path", qx.JSONParam(`{"a":[1]}`).Path("a", "0").Expr(), `1`, false, true},
		{"json_path_text", qx.JSONParam(`{"a":[1]}`).PathText("a", "0").Expr(), `1`, false, false},
		{"byte_document", qx.JSONBParam([]byte(`{"a":1}`)).TextKey("a").Expr(), `1`, false, false},
		{"contains", object.Contains(qx.JSONBParam(`{"0":42}`)).Expr(), "true", false, false},
		{"contained_by", qx.JSONBParam(`{"0":42}`).ContainedBy(object).Expr(), "true", false, false},
		{"does_not_contain", object.Contains(qx.JSONBParam(`{"0":43}`)).Expr(), "false", false, false},
		{"has_key", object.HasKey("0").Expr(), "true", false, false},
		{"absent_key", object.HasKey("missing").Expr(), "false", false, false},
		{"has_any", object.HasAnyKeys("0", "missing").Expr(), "true", false, false},
		{"has_all", object.HasAllKeys("0", "missing").Expr(), "false", false, false},
		{"empty_any", object.HasAnyKeys().Expr(), "false", false, false},
		{"empty_all", object.HasAllKeys().Expr(), "true", false, false},
		{"delete_key", qx.JSONBParam(`{"0":42,"a":1}`).DeleteKey("0").Expr(), `{"a":1}`, false, true},
		{"delete_string_from_array", array.DeleteKey("0").Expr(), `[{"name":"Ada"},{"name":"Grace"}]`, false, true},
		{"delete_index", array.DeleteIndex(0).Expr(), `[{"name":"Grace"},"0"]`, false, true},
		{"delete_negative_index", array.DeleteIndex(-1).Expr(), `[{"name":"Ada"},{"name":"Grace"}]`, false, true},
		{"delete_keys", object.DeleteKeys("0", "a").Expr(), `{"items":[{"name":"Ada"}]}`, false, true},
		{"delete_path", object.DeletePath("items", "0", "name").Expr(), `{"0":42,"a":null,"items":[{}]}`, false, true},
		{"concat", qx.JSONBParam(`{"a":1,"b":2}`).Concat(qx.JSONBParam(`{"a":3}`)).Expr(), `{"a":3,"b":2}`, false, true},
		{"path_exists", qx.JSONBParam(`{"a":1}`).PathExists("$.a").Expr(), "true", false, false},
		{"selection_is_not_predicate", qx.JSONBParam(`{"a":1}`).PathMatches("$.a").Expr(), "", true, false},
		{"predicate_false", qx.JSONBParam(`{"a":1}`).PathMatches("$.a > 2").Expr(), "false", false, false},
		{"predicate_has_item", qx.JSONBParam(`{"a":1}`).PathExists("$.a > 2").Expr(), "true", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args, err := qx.Select(tc.expr.Cast(qx.Text)).ToSQL()
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
	if _, err := conn.Exec(ctx, `CREATE TEMP TABLE qx_json_events(id int, payload jsonb);
		INSERT INTO qx_json_events VALUES (1,'{"items":[{"name":"Ada"}]}'), (2,'{"items":[{"name":"Grace"}]}')`); err != nil {
		t.Fatal(err)
	}
	doc := qx.JSONBCol("payload")
	query := qx.Update("qx_json_events").
		Set(doc.Set(doc.Concat(qx.JSONBParam(`{"active":true}`)))).
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
	if err := conn.QueryRow(ctx, `SELECT NOT (payload ? 'active') FROM qx_json_events WHERE id=2`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Fatal("JSON filter updated the nonmatching row")
	}
}

func TestExactLiteralSemantics(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	query := qx.Select(
		qx.LiteralNumeric("123456789012345678901234567890.123456789").Cast(qx.Text),
		qx.LiteralBit("00101").Cast(qx.Text),
		qx.LiteralHex("Ab09").Cast(qx.Text),
		qx.PrefixOperator("|/", qx.LiteralInt(9)).Cast(qx.Text),
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

func TestExplainSerialization(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	for _, tc := range []struct {
		mode   qx.ExplainSerialization
		format string
	}{
		{qx.SerializeText, "text"},
		{qx.SerializeBinary, "binary"},
		{qx.SerializeNone, ""},
	} {
		t.Run(tc.format+"_serialization", func(t *testing.T) {
			query := qx.Explain(qx.Select(qx.LiteralInt(1))).Analyze(true).
				Serialize(tc.mode).Timing(false).Format(qx.ExplainJSON)
			sql, args, err := query.ToSQL()
			if err != nil {
				t.Fatal(err)
			}
			var result []byte
			if err := conn.QueryRow(ctx, sql, args...).Scan(&result); err != nil {
				t.Fatal(err)
			}
			var plans []struct {
				Serialization *struct{ Format string }
			}
			if err := json.Unmarshal(result, &plans); err != nil {
				t.Fatal(err)
			}
			if len(plans) != 1 {
				t.Fatalf("expected one explained statement: %s", result)
			}
			serialization := plans[0].Serialization
			if tc.mode == qx.SerializeNone {
				if serialization != nil {
					t.Fatalf("serialization disabled: %s", result)
				}
			} else if serialization == nil || serialization.Format != tc.format {
				t.Fatalf("expected serialization format %q: %s", tc.format, result)
			}
		})
	}
}

func queryStrings(t *testing.T, ctx context.Context, conn *pgx.Conn, s qx.Statement) []string {
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
	q := qx.Select(
		qx.ParamNull(qx.NullOf[string]()).Cast(qx.Text),
		qx.ParamNull(qx.NonNull("value")).Cast(qx.Text),
		qx.Param(1).Cast(qx.Int4).EqExpr(qx.NullLiteral()).Expr(),
		qx.Param(1).Cast(qx.Int4).InExpr(qx.Param(2), qx.NullLiteral()).Expr(),
		qx.EqAny(qx.Param(int64(2)), qx.ArrayParam([]int64{1, 2}, qx.Int8)).Expr(),
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
	upsert := qx.InsertInto("target").Columns("id", "name").Values(int64(1), "updated").
		OnConflict(qx.ConflictColumns("id").DoUpdate(qx.SetExpr("name", qx.Excluded("name")))).ReturningCols("name")
	if got := queryStrings(t, ctx, conn, upsert); !reflect.DeepEqual(got, []string{"updated"}) {
		t.Fatal(got)
	}
	q := qx.MergeInto("target").Using(qx.Table("source")).On(qx.EqColumns("target.id", "source.id")).
		When(qx.Matched().ThenUpdate(qx.SetExpr("name", qx.Col("source.name"))),
			qx.NotMatchedByTarget().ThenInsert(qx.SetExpr("id", qx.Col("source.id")), qx.SetExpr("name", qx.Col("source.name")))).
		Returning(qx.MergeAction())
	got := queryStrings(t, ctx, conn, q)
	actions := map[string]int{}
	for _, a := range got {
		actions[a]++
	}
	if actions["INSERT"] != 1 || actions["UPDATE"] != 1 || len(got) != 2 {
		t.Fatal(got)
	}
	oldNew := qx.Update("target").Set(qx.Set("name", "latest")).Where(qx.Eq("id", int64(1))).Returning(qx.Old("name"), qx.New("name"))
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

func TestRecursiveCTEAndSQLJSON(t *testing.T) {
	t.Parallel()
	ctx, conn := connect(t)
	seed := qx.Select(qx.LiteralInt(1))
	step := qx.Select(qx.Col("n").Add(qx.LiteralInt(1))).From("numbers").Where(qx.Lt("n", 3))
	numbers := qx.CTE("numbers", qx.UnionAll(seed, step)).Columns("n")
	q := qx.Select(qx.Col("n").Cast(qx.Text)).WithRecursive(numbers).From("numbers").OrderBy(qx.Asc("n"))
	if got := queryStrings(t, ctx, conn, q); !reflect.DeepEqual(got, []string{"1", "2", "3"}) {
		t.Fatal(got)
	}
	json := qx.JSONValue(qx.Param(`{"name":"Ana"}`).Cast(qx.JSONB), qx.LiteralString("$.name")).Returning(qx.Text).Expr()
	if got := queryStrings(t, ctx, conn, qx.Select(json)); !reflect.DeepEqual(got, []string{"Ana"}) {
		t.Fatal(got)
	}
	table := qx.JSONTable(qx.Param(`[{"name":"Ana"},{"name":"João"}]`).Cast(qx.JSONB), "$[*]", qx.JSONOrdinality("position"), qx.JSONColumn("name", qx.Text).Path("$.name")).As("j")
	if got := queryStrings(t, ctx, conn, qx.SelectCols("j.name").FromExpr(table).OrderBy(qx.Asc("j.position"))); !reflect.DeepEqual(got, []string{"Ana", "João"}) {
		t.Fatal(got)
	}
}
