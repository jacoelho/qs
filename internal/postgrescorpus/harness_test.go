package postgrescorpus

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	qx "github.com/jacoelho/qx"
)

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

func sampleCases() []Case {
	return []Case{
		{ID: 0, Source: "sql/select.sql", Line: 3, Origin: "direct", Statement: "SelectStmt", Shape: "a", OriginalSQL: "SELECT $1", Status: StatusVerified, WantSQL: "SELECT $1", WantArgs: []string{"first"}},
		{ID: 8, Source: "sql/join.sql", Line: 7, Origin: "direct", Statement: "SelectStmt", Shape: "b", OriginalSQL: "SELECT 2", Status: StatusConstructionErr, Reason: "requires JOIN qualification", WantArgs: []string{"second", "third"}},
		{ID: 16, Source: "sql/json.sql", Line: 11, Origin: "view", Statement: "SelectStmt", Shape: "c", OriginalSQL: "SELECT 3", Status: StatusUnsupported, Reason: "wrapper unavailable"},
	}
}

func sampleFactories() []Factory {
	return []Factory{
		{ID: 0, Status: StatusVerified, Build: func() qx.Statement { return qx.Select(qx.Param("first")) }},
		{ID: 8, Status: StatusConstructionErr, Build: func() qx.Statement { return qx.Select(qx.Param("second"), qx.Param("third")) }},
		{ID: 16, Status: StatusUnsupported},
	}
}

func TestStreamShardPreservesAssociationAndBounds(t *testing.T) {
	t.Parallel()
	cases := sampleCases()
	// A valid record must not be constrained by Scanner's default 64 KiB limit.
	cases[2].OriginalSQL = strings.Repeat("x", 70000)
	data := strings.TrimSuffix(encodeCases(t, cases), "\n")
	var groups [][]Case
	err := StreamShard(strings.NewReader(data), ShardSpec{Name: "shard00", Index: 0, ExpectedIDs: []int{0, 8, 16}, GroupSize: 2}, sampleFactories(), func(group int, items []Case) error {
		if group != len(groups) || len(items) > 2 {
			t.Fatalf("unexpected group %d size %d", group, len(items))
		}
		groups = append(groups, items)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 1 {
		t.Fatalf("group inventory: %+v", groups)
	}
	if groups[0][0].ID != 0 || groups[0][0].Source != "sql/select.sql" || groups[0][0].WantArgs[0] != "first" ||
		groups[0][1].ID != 8 || groups[0][1].WantArgs[0] != "second" || groups[0][1].WantArgs[1] != "third" ||
		groups[1][0].ID != 16 || len(groups[1][0].OriginalSQL) != 70000 {
		t.Fatal("record provenance or argument storage was corrupted")
	}
}

func TestStreamShardRejectsCorruption(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func([]Case, []Factory) ([]Case, []Factory)
		tail   string
		want   string
	}{
		{name: "truncated_json", tail: "{", want: "decode"},
		{name: "premature_eof", change: func(c []Case, f []Factory) ([]Case, []Factory) { return c[:2], f }, want: "clean EOF"},
		{name: "extra_record", change: func(c []Case, f []Factory) ([]Case, []Factory) { return append(c, c[2]), f }, want: "extra record"},
		{name: "duplicate", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[1].ID = 0; return c, f }, want: "expected shard ID"},
		{name: "wrong_shard", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[1].ID = 9; return c, f }, want: "expected shard ID"},
		{name: "out_of_order", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[0], c[1] = c[1], c[0]; return c, f }, want: "expected shard ID"},
		{name: "gap", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[1].ID = 16; return c, f }, want: "expected shard ID"},
		{name: "provenance", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[0].Line = 0; return c, f }, want: "provenance"},
		{name: "unknown_status", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[0].Status = "maybe"; return c, f }, want: "unknown status"},
		{name: "missing_sql", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[0].WantSQL = ""; return c, f }, want: "SQL expectation"},
		{name: "missing_reason", change: func(c []Case, f []Factory) ([]Case, []Factory) { c[2].Reason = ""; return c, f }, want: "no reason"},
		{name: "factory_id", change: func(c []Case, f []Factory) ([]Case, []Factory) { f[1].ID = 16; return c, f }, want: "factory association"},
		{name: "factory_status", change: func(c []Case, f []Factory) ([]Case, []Factory) { f[0].Status = StatusUnsupported; return c, f }, want: "factory association"},
		{name: "missing_factory", change: func(c []Case, f []Factory) ([]Case, []Factory) { f[0].Build = nil; return c, f }, want: "factory presence"},
		{name: "unsupported_factory", change: func(c []Case, f []Factory) ([]Case, []Factory) { f[2].Build = f[0].Build; return c, f }, want: "factory presence"},
		{name: "factory_count", change: func(c []Case, f []Factory) ([]Case, []Factory) { return c, f[:2] }, want: "factory count"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cases, factories := sampleCases(), sampleFactories()
			if test.change != nil {
				cases, factories = test.change(cases, factories)
			}
			data := encodeCases(t, cases) + test.tail
			err := StreamShard(strings.NewReader(data), ShardSpec{Name: "shard00", Index: 0, ExpectedIDs: []int{0, 8, 16}}, factories, func(_ int, _ []Case) error { return nil })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyCaseUsesFrozenOracle(t *testing.T) {
	t.Parallel()
	item, factory := sampleCases()[0], sampleFactories()[0]
	if err := VerifyCase(item, factory); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		item    Case
		factory Factory
		want    string
	}{
		{name: "sql", item: Case{ID: 0, Status: StatusVerified, WantSQL: "SELECT $2", WantArgs: []string{"first"}}, factory: factory, want: "SQL mismatch"},
		{name: "args", item: Case{ID: 0, Status: StatusVerified, WantSQL: "SELECT $1", WantArgs: []string{"other"}}, factory: factory, want: "argument 0"},
		{name: "order", item: Case{ID: 0, Status: StatusVerified, WantSQL: "SELECT $1, $2", WantArgs: []string{"second", "first"}}, factory: Factory{ID: 0, Status: StatusVerified, Build: func() qx.Statement { return qx.Select(qx.Param("first"), qx.Param("second")) }}, want: "argument 0"},
		{name: "type", item: item, factory: Factory{ID: 0, Status: StatusVerified, Build: func() qx.Statement { return qx.Select(qx.Param(1)) }}, want: "argument 0"},
		{name: "count", item: Case{ID: 0, Status: StatusVerified, WantSQL: "SELECT $1"}, factory: factory, want: "argument count"},
		{name: "nil", item: item, factory: Factory{ID: 0, Status: StatusVerified, Build: func() qx.Statement { return nil }}, want: "nil statement"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := VerifyCase(test.item, test.factory); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
		})
	}
}

func tinyFixture(t *testing.T) (Manifest, fstest.MapFS) {
	t.Helper()
	cases := []Case{
		{ID: 0, Shape: "a", Planner: true, Status: StatusVerified},
		{ID: 1, Shape: "b", Status: StatusConstructionErr, Reason: "invalid join"},
		{ID: 2, Shape: "c", Planner: true, Status: StatusVerified},
		{ID: 3, Shape: "d", Status: StatusVerified},
		{ID: 4, Shape: "e", Status: StatusVerified},
		{ID: 5, Shape: "f", Status: StatusVerified},
		{ID: 6, Shape: "g", Status: StatusVerified},
		{ID: 7, Shape: "h", Status: StatusVerified},
		{ID: 8, Shape: "a", Status: StatusUnsupported, Reason: "unknown wrapper"},
		{ID: 9, Shape: "i", Planner: true, Status: StatusVerified},
	}
	for index := range cases {
		cases[index].Source = "sql/tiny.sql"
		cases[index].Line = index + 1
		cases[index].Origin = "direct"
		cases[index].Statement = "SelectStmt"
		cases[index].OriginalSQL = "SELECT 1"
		if cases[index].Status == StatusVerified {
			cases[index].WantSQL = "SELECT 1"
		}
	}
	manifest := Manifest{
		Schema: "qx-postgres-corpus-v2", Revision: strings.Repeat("a", 40), SQLSHA256: strings.Repeat("b", 64),
		Parser: ParserManifest{Package: "pglast", Version: "v8.4", PostgreSQL: "18.4"},
		Counts: CountsManifest{Total: 10, Verified: 8, ConstructionError: 1, Unsupported: 1, PlannerTotal: 3, PlannerVerified: 3, DistinctShapes: 9, DistinctVerifiedShapes: 7, PlannerDistinctShapes: 3, PlannerDistinctVerifiedShapes: 2},
	}
	files := fstest.MapFS{}
	partitions := [][]int{{0, 8}, {1, 9}, {2}, {3}, {4}, {5}, {6}, {7}}
	for index, ids := range partitions {
		name := fmt.Sprintf("shard%02d", index)
		var rows []Case
		for _, id := range ids {
			rows = append(rows, cases[id])
		}
		data := encodeCases(t, rows)
		verified, construction, unsupported := 1, 0, 0
		if index == 0 {
			unsupported = 1
		}
		if index == 1 {
			construction = 1
		}
		manifest.Shards = append(manifest.Shards, ShardManifest{Index: index, Package: name, JSONL: name + "/corpus.jsonl", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(data))), Count: len(ids), FirstID: ids[0], LastID: ids[len(ids)-1], Verified: verified, ConstructionError: construction, Unsupported: unsupported})
		files[name+"/corpus.jsonl"] = &fstest.MapFile{Data: []byte(data)}
		files[name+"/corpus_generated_test.go"] = &fstest.MapFile{Data: []byte("package " + name)}
	}
	return manifest, files
}

func TestAuditRetainsGlobalShapeSemantics(t *testing.T) {
	t.Parallel()
	manifest, files := tinyFixture(t)
	// Shape a spans two shards. Its non-planner failure also disqualifies it
	// from verified planner shapes, despite its verified planner occurrence.
	if err := Audit(files, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRejectsIncompleteInventory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*Manifest, fstest.MapFS)
		want   string
	}{
		{name: "missing_package", change: func(_ *Manifest, f fstest.MapFS) {
			delete(f, "shard07/corpus.jsonl")
			delete(f, "shard07/corpus_generated_test.go")
		}, want: "corpus packages"},
		{name: "extra_package", change: func(_ *Manifest, f fstest.MapFS) {
			f["shard08/corpus.jsonl"] = f["shard07/corpus.jsonl"]
			f["shard08/corpus_generated_test.go"] = f["shard07/corpus_generated_test.go"]
		}, want: "corpus packages"},
		{name: "stale_monolith", change: func(_ *Manifest, f fstest.MapFS) { f["corpus_generated_test.go"] = &fstest.MapFile{} }, want: "stale monolithic"},
		{name: "extra_artifact", change: func(_ *Manifest, f fstest.MapFS) { f["shard00/stale_test.go"] = &fstest.MapFile{} }, want: "artifact inventory"},
		{name: "missing_builder_file", change: func(_ *Manifest, f fstest.MapFS) { delete(f, "shard00/corpus_generated_test.go") }, want: "artifact inventory"},
		{name: "hash", change: func(m *Manifest, _ fstest.MapFS) { m.Shards[0].SHA256 = strings.Repeat("0", 64) }, want: "JSONL hash"},
		{name: "local_counts", change: func(m *Manifest, _ fstest.MapFS) {
			m.Shards[0].Unsupported = 0
			m.Shards[0].Verified = 2
			m.Counts.Unsupported = 0
			m.Counts.Verified = 9
		}, want: "status counts"},
		{name: "global_shapes", change: func(m *Manifest, _ fstest.MapFS) { m.Counts.PlannerDistinctVerifiedShapes = 3 }, want: "corpus counts"},
		{name: "count", change: func(m *Manifest, _ fstest.MapFS) { m.Shards[0].Count = 1 }, want: "inventory/counts"},
		{name: "schema", change: func(m *Manifest, _ fstest.MapFS) { m.Schema = "v1" }, want: "schema"},
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
	for _, input := range []string{string(data) + "{}", string(data) + "{", string(data[:len(data)-1]), strings.Replace(string(data), `"schema":`, `"unknown":0,"schema":`, 1)} {
		if _, err := ReadManifest(strings.NewReader(input)); err == nil {
			t.Fatal("accepted a corrupt manifest")
		}
	}
	if _, err := ReadManifest(strings.NewReader(string(data))); err != nil {
		t.Fatal(err)
	}
}
