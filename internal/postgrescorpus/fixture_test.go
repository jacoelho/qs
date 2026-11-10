package postgrescorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

// Trees below are libpg_query JSON as the offline tooling freezes it.
const (
	// SELECT $1
	treeSelectParam = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"ParamRef":{"number":1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT $2, $1
	treeSwappedParams = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"ParamRef":{"number":2}}}},{"ResTarget":{"val":{"ParamRef":{"number":1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT $1, $1
	treeRepeatedParam = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"ParamRef":{"number":1}}}},{"ResTarget":{"val":{"ParamRef":{"number":1}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT ROW()
	treeEmptyRow = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"RowExpr":{"row_format":"COERCE_EXPLICIT_CALL"}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT xmlconcat(a)
	treeXMLConcat = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"XmlExpr":{"op":"IS_XMLCONCAT","args":[{"ColumnRef":{"fields":[{"String":{"sval":"a"}}]}}],"xmloption":"XMLOPTION_DOCUMENT"}}}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
	// SELECT count(*) OVER w FROM t WINDOW w AS (), w AS ()
	treeDuplicateWindow = `{"SelectStmt":{"targetList":[{"ResTarget":{"val":{"FuncCall":{"funcname":[{"String":{"sval":"count"}}],"agg_star":true,"over":{"name":"w","frameOptions":1058},"funcformat":"COERCE_EXPLICIT_CALL"}}}}],"fromClause":[{"RangeVar":{"relname":"t","inh":true,"relpersistence":"p"}}],"windowClause":[{"WindowDef":{"name":"w","frameOptions":1058}},{"WindowDef":{"name":"w","frameOptions":1058}}],"limitOption":"LIMIT_OPTION_DEFAULT","op":"SETOP_NONE"}}`
)

func sampleCases() []Case {
	cases := []Case{
		{ID: 0, Status: StatusVerified, Tree: json.RawMessage(treeSelectParam), WantSQL: "SELECT $1", WantArgs: []string{ParameterSentinel(0, 1)}, Shape: "a", Planner: true},
		{ID: 1, Status: StatusGap, Tree: json.RawMessage(treeEmptyRow), Reason: "ROW() has no constructor", Shape: "b"},
		{ID: 2, Status: StatusOutOfScope, Tree: json.RawMessage(treeXMLConcat), Reason: "XMLCONCAT", Shape: "c"},
		{ID: 3, Status: StatusRejected, Tree: json.RawMessage(treeDuplicateWindow), Reason: "duplicate window name", Shape: "d"},
		{ID: 4, Status: StatusVerified, Tree: json.RawMessage(treeSelectParam), WantSQL: "SELECT $1", WantArgs: []string{ParameterSentinel(4, 1)}, Shape: "b", Planner: true},
	}
	for index := range cases {
		cases[index].Source = "sql/tiny.sql"
		cases[index].Line = index + 1
		cases[index].Origin = "direct"
		cases[index].Statement = "SelectStmt"
		cases[index].OriginalSQL = "SELECT source"
	}
	return cases
}

func encodeCases(t *testing.T, cases []Case) string {
	t.Helper()
	var data strings.Builder
	encoder := json.NewEncoder(&data)
	for _, item := range cases {
		if err := encoder.Encode(item); err != nil {
			t.Fatal(err)
		}
	}
	return data.String()
}

func readCases(data string, firstID, count int) ([]Case, error) {
	var cases []Case
	for item, err := range Cases(strings.NewReader(data), firstID, count) {
		if err != nil {
			return cases, err
		}
		cases = append(cases, item)
	}
	return cases, nil
}

func TestCasesPreservesRecords(t *testing.T) {
	t.Parallel()
	want := sampleCases()
	// A record must not be limited to bufio.Scanner's 64 KiB default.
	want[2].OriginalSQL = strings.Repeat("x", 70000)
	got, err := readCases(strings.TrimSuffix(encodeCases(t, want), "\n"), 0, len(want))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d records, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].ID != want[index].ID || got[index].Status != want[index].Status ||
			string(got[index].Tree) != string(want[index].Tree) || got[index].WantSQL != want[index].WantSQL ||
			strings.Join(got[index].WantArgs, ",") != strings.Join(want[index].WantArgs, ",") ||
			got[index].OriginalSQL != want[index].OriginalSQL || got[index].Reason != want[index].Reason {
			t.Fatalf("record %d: got %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestCasesStopsWhenConsumerStops(t *testing.T) {
	t.Parallel()
	seen := 0
	for _, err := range Cases(strings.NewReader(encodeCases(t, sampleCases())), 0, 5) {
		if err != nil {
			t.Fatal(err)
		}
		seen++
		break
	}
	if seen != 1 {
		t.Fatalf("consumed %d records, want 1", seen)
	}
}

func TestCasesRejectsCorruption(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func([]Case) []Case
		tail   string
		first  int
		want   string
	}{
		{name: "truncated_json", tail: "{", want: "decode"},
		{name: "unknown_field", tail: `{"surprise":1}`, want: "decode"},
		{name: "premature_eof", change: func(c []Case) []Case { return c[:4] }, want: "ended after 4 records"},
		{name: "extra_record", change: func(c []Case) []Case { extra := c[4]; extra.ID = 5; return append(c, extra) }, want: "extra record"},
		{name: "duplicate_id", change: func(c []Case) []Case { c[1].ID = 0; return c }, want: "want id 1"},
		{name: "gap_in_ids", change: func(c []Case) []Case { c[1].ID = 2; return c }, want: "want id 1"},
		{name: "wrong_first_id", first: 8, want: "want id 8"},
		{name: "provenance", change: func(c []Case) []Case { c[0].Line = 0; return c }, want: "provenance"},
		{name: "missing_shape", change: func(c []Case) []Case { c[0].Shape = ""; return c }, want: "shape"},
		{name: "missing_tree", change: func(c []Case) []Case { c[0].Tree = json.RawMessage("null"); return c }, want: "parse tree"},
		{name: "unknown_status", change: func(c []Case) []Case { c[0].Status = "maybe"; return c }, want: "unknown status"},
		{name: "verified_without_sql", change: func(c []Case) []Case { c[0].WantSQL = ""; return c }, want: "no SQL expectation"},
		{name: "verified_with_reason", change: func(c []Case) []Case { c[0].Reason = "why"; return c }, want: "has a reason"},
		{name: "gap_without_reason", change: func(c []Case) []Case { c[1].Reason = ""; return c }, want: "no reason"},
		{name: "gap_with_sql", change: func(c []Case) []Case { c[1].WantSQL = "SELECT 1"; return c }, want: "has an SQL expectation"},
		{name: "rejected_with_args", change: func(c []Case) []Case { c[3].WantArgs = []string{"x"}; return c }, want: "has an SQL expectation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cases := sampleCases()
			if test.change != nil {
				cases = test.change(cases)
			}
			_, err := readCases(encodeCases(t, cases)+test.tail, test.first, 5)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
		})
	}
}

// tinyFixture spreads sampleCases over two files. Shape b has a verified
// planner occurrence and a gap, so it is a planner shape that is not verified.
func tinyFixture(t *testing.T) (Manifest, fstest.MapFS) {
	t.Helper()
	cases := sampleCases()
	manifest := Manifest{
		Schema: manifestSchema, Revision: strings.Repeat("a", 40), SQLSHA256: strings.Repeat("b", 64),
		Parser: ParserManifest{Package: "pglast", Version: "v8.4", PostgreSQL: "18.4"},
		Counts: CountsManifest{
			Total:        5,
			StatusCounts: StatusCounts{Verified: 2, OutOfScope: 1, Gap: 1, Rejected: 1},
			PlannerTotal: 2, PlannerVerified: 2,
			DistinctShapes: 4, DistinctVerifiedShapes: 1,
			PlannerDistinctShapes: 2, PlannerDistinctVerifiedShapes: 1,
		},
	}
	files := fstest.MapFS{}
	for index, part := range []struct {
		cases  []Case
		counts StatusCounts
	}{
		{cases: cases[:3], counts: StatusCounts{Verified: 1, OutOfScope: 1, Gap: 1}},
		{cases: cases[3:], counts: StatusCounts{Verified: 1, Rejected: 1}},
	} {
		data := []byte(encodeCases(t, part.cases))
		digest := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, FileManifest{
			Path: fixturePath(index), SHA256: hex.EncodeToString(digest[:]),
			FirstID: part.cases[0].ID, Count: len(part.cases), StatusCounts: part.counts,
		})
		files[fixturePath(index)] = &fstest.MapFile{Data: data}
	}
	return manifest, files
}

func TestAuditAcceptsConsistentFixture(t *testing.T) {
	t.Parallel()
	manifest, files := tinyFixture(t)
	if err := Audit(files, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRejectsInconsistency(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*Manifest, fstest.MapFS)
		want   string
	}{
		{name: "missing_file", change: func(_ *Manifest, f fstest.MapFS) { delete(f, fixturePath(1)) }, want: "open testdata/corpus-01.jsonl"},
		{name: "unlisted_file", change: func(_ *Manifest, f fstest.MapFS) { f[fixturePath(2)] = f[fixturePath(1)] }, want: "unlisted fixture artifact"},
		{name: "edited_record", change: func(_ *Manifest, f fstest.MapFS) {
			f[fixturePath(0)].Data = []byte(strings.Replace(string(f[fixturePath(0)].Data), "SELECT $1", "SELECT $2", 1))
		}, want: "hash"},
		{name: "hash", change: func(m *Manifest, _ fstest.MapFS) { m.Files[0].SHA256 = strings.Repeat("0", 64) }, want: "hash"},
		{name: "file_status_counts", change: func(m *Manifest, _ fstest.MapFS) {
			m.Files[0].Gap, m.Files[0].Verified = 0, 2
			m.Counts.Gap, m.Counts.Verified = 0, 3
		}, want: "status counts"},
		{name: "planner_counts", change: func(m *Manifest, _ fstest.MapFS) { m.Counts.PlannerVerified = 1 }, want: "corpus counts"},
		{name: "shape_counts", change: func(m *Manifest, _ fstest.MapFS) { m.Counts.PlannerDistinctVerifiedShapes = 2 }, want: "corpus counts"},
		{name: "file_count", change: func(m *Manifest, _ fstest.MapFS) { m.Files[0].Count = 2 }, want: "inventory/counts"},
		{name: "id_range", change: func(m *Manifest, _ fstest.MapFS) { m.Files[1].FirstID = 4 }, want: "inventory/counts"},
		{name: "file_path", change: func(m *Manifest, _ fstest.MapFS) { m.Files[1].Path = "testdata/other.jsonl" }, want: "invalid path"},
		{name: "global_status_counts", change: func(m *Manifest, _ fstest.MapFS) { m.Counts.Gap, m.Counts.Rejected = 2, 0 }, want: "disagree with global"},
		{name: "schema", change: func(m *Manifest, _ fstest.MapFS) { m.Schema = "qs-postgres-corpus-v2" }, want: "schema"},
		{name: "provenance", change: func(m *Manifest, _ fstest.MapFS) { m.Revision = "main" }, want: "provenance"},
		{name: "no_files", change: func(m *Manifest, _ fstest.MapFS) { m.Files = nil }, want: "no fixture files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, files := tinyFixture(t)
			test.change(&manifest, files)
			if err := Audit(files, manifest); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadManifestRejectsCorruption(t *testing.T) {
	t.Parallel()
	manifest, _ := tinyFixture(t)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	valid := string(data)
	if _, err := ReadManifest(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"trailing_value":   valid + "{}",
		"trailing_garbage": valid + "{",
		"truncated":        valid[:len(valid)-1],
		"unknown_field":    strings.Replace(valid, `"schema":`, `"unknown":0,"schema":`, 1),
	} {
		if _, err := ReadManifest(strings.NewReader(input)); err == nil {
			t.Errorf("%s: accepted a corrupt manifest", name)
		}
	}
}
