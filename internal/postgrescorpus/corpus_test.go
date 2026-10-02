package postgrescorpus

import (
	"errors"
	"os"
	"testing"
)

func TestPinnedCorpusManifest(t *testing.T) {
	t.Parallel()
	file, err := os.Open("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, readErr := ReadManifest(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != "630e607397424196a0a3ebb14a5658c2473ddadf" ||
		manifest.SQLSHA256 != "ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d" ||
		manifest.Parser != (ParserManifest{Package: "pglast", Version: "v8.4", PostgreSQL: "18.4"}) {
		t.Fatalf("unexpected pinned source/parser provenance: %+v", manifest)
	}
	want := CountsManifest{
		Total: 28197, Verified: 28111, ConstructionError: 53, Unsupported: 33,
		PlannerTotal: 5150, PlannerVerified: 5124,
		DistinctShapes: 13722, DistinctVerifiedShapes: 13645,
		PlannerDistinctShapes: 3893, PlannerDistinctVerifiedShapes: 3867,
	}
	if manifest.Counts != want {
		t.Fatalf("frozen corpus counts %+v, want %+v", manifest.Counts, want)
	}
	if err := Audit(os.DirFS("."), manifest); err != nil {
		t.Fatal(err)
	}
}
