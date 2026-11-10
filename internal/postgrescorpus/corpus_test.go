package postgrescorpus

import (
	"errors"
	"os"
	"testing"
)

// pinnedCounts changes only with a deliberate fixture update.
var pinnedCounts = CountsManifest{
	Total:    28197,
	Verified: 27887, OutOfScope: 220, Gap: 40, Rejected: 50,
	PlannerTotal: 5150, PlannerVerified: 5062,
	DistinctShapes: 13722, DistinctVerifiedShapes: 13517,
	PlannerDistinctShapes: 3893, PlannerDistinctVerifiedShapes: 3831,
}

func pinnedManifest(t *testing.T) Manifest {
	t.Helper()
	file, err := os.Open("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, readErr := ReadManifest(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestPinnedCorpusManifest(t *testing.T) {
	t.Parallel()
	manifest := pinnedManifest(t)
	if manifest.Revision != "630e607397424196a0a3ebb14a5658c2473ddadf" ||
		manifest.SQLSHA256 != "ddec651ca5f78d1ae9b721476efe586daf9220d80d5284f6fccd8329ed66218d" ||
		manifest.Parser != (ParserManifest{Package: "pglast", Version: "v8.4", PostgreSQL: "18.4"}) {
		t.Fatalf("unexpected pinned source/parser provenance: %+v", manifest)
	}
	if manifest.Counts != pinnedCounts {
		t.Fatalf("frozen corpus counts %+v, want %+v", manifest.Counts, pinnedCounts)
	}
	if err := Audit(os.DirFS("."), manifest); err != nil {
		t.Fatal(err)
	}
}

// maxReportedFailures keeps one adapter regression from printing thousands
// of near-identical errors per fixture file.
const maxReportedFailures = 10

// TestCorpus builds every frozen parse tree through the public qs API and
// compares the outcome with the independently verified expectation.
func TestCorpus(t *testing.T) {
	t.Parallel()
	manifest := pinnedManifest(t)
	// Per-file outcomes are checked below; the manifest ties their sum to
	// the pinned totals, so an outcome cannot move without a source edit.
	if manifest.Counts != pinnedCounts {
		t.Fatalf("frozen corpus counts %+v, want %+v", manifest.Counts, pinnedCounts)
	}
	for _, file := range manifest.Files {
		t.Run(file.Path, func(t *testing.T) {
			t.Parallel()
			reader, err := os.Open(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			var outcomes StatusCounts
			failures := 0
			for item, err := range Cases(reader, file.FirstID, file.Count) {
				if err != nil {
					t.Fatal(err)
				}
				if err := Verify(item); err != nil {
					if failures++; failures <= maxReportedFailures {
						t.Error(err)
					}
					continue
				}
				outcomes.add(item.Status)
			}
			if failures > maxReportedFailures {
				t.Errorf("%d further failures not shown", failures-maxReportedFailures)
			}
			if outcomes != file.StatusCounts {
				t.Fatalf("adapter outcomes %+v, want %+v", outcomes, file.StatusCounts)
			}
		})
	}
}
