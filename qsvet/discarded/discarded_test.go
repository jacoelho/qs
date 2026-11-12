package discarded_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/jacoelho/qs/qsvet/discarded"
)

// The client package is checked against the real qs package, which its module
// replaces with this checkout, so the expectations follow the actual API.
func TestDiscardedResults(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), discarded.Analyzer, "./...")
}
