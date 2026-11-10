package postgrescorpus

import (
	"slices"
	"strings"
	"testing"
)

func TestRenderOutcomes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		tree string
		want Rendering
	}{
		{name: "built", tree: treeSelectParam, want: Rendering{Outcome: OutcomeBuilt, SQL: "SELECT $1", Args: []string{"qs-postgres-corpus-7-param-1"}}},
		{name: "out_of_scope", tree: treeXMLConcat, want: Rendering{Outcome: OutcomeOutOfScope, Detail: "XMLCONCAT"}},
		{name: "gap", tree: treeEmptyRow, want: Rendering{Outcome: OutcomeGap, Detail: "ROW() has no constructor"}},
		// qs renumbers placeholders, so these would bind the wrong values.
		{name: "swapped_parameters", tree: treeSwappedParams, want: Rendering{Outcome: OutcomeGap, Detail: "parameter numbers do not follow rendering order"}},
		{name: "repeated_parameter", tree: treeRepeatedParam, want: Rendering{Outcome: OutcomeGap, Detail: "parameter numbers do not follow rendering order"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(7, []byte(test.tree))
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != test.want.Outcome || got.Detail != test.want.Detail || got.SQL != test.want.SQL || !slices.Equal(got.Args, test.want.Args) {
				t.Fatalf("got %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestRenderReportsRenderRefusal(t *testing.T) {
	t.Parallel()
	got, err := Render(0, []byte(treeDuplicateWindow))
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != OutcomeRejected || got.Detail == "" || got.SQL != "" {
		t.Fatalf("got %+v, want a rejection carrying the render error", got)
	}
}

func TestRenderRejectsMalformedTree(t *testing.T) {
	t.Parallel()
	for _, tree := range []string{``, `[]`, `{"SelectStmt":`, `{"SelectStmt":{"targetList":7,"op":"SETOP_NONE"}}`, `{"SelectStmt":{"op":"SETOP_NONE"}} {}`} {
		if _, err := Render(12, []byte(tree)); err == nil || !strings.Contains(err.Error(), "id=12") {
			t.Errorf("tree %q: error %v, want one naming id=12", tree, err)
		}
	}
}

func TestVerifyAcceptsFrozenExpectations(t *testing.T) {
	t.Parallel()
	for _, item := range sampleCases() {
		if err := Verify(item); err != nil {
			t.Error(err)
		}
	}
}

func TestVerifyRejectsDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		index  int
		change func(*Case)
		want   string
	}{
		{name: "sql", index: 0, change: func(c *Case) { c.WantSQL = "SELECT $2" }, want: "SQL mismatch"},
		{name: "argument_value", index: 0, change: func(c *Case) { c.WantArgs = []string{ParameterSentinel(1, 1)} }, want: "arguments"},
		{name: "argument_count", index: 0, change: func(c *Case) { c.WantArgs = nil }, want: "arguments"},
		{name: "verified_now_gap", index: 0, change: func(c *Case) { c.Tree = []byte(treeEmptyRow) }, want: "outcome gap"},
		{name: "gap_now_built", index: 1, change: func(c *Case) { c.Tree = []byte(treeSelectParam) }, want: "outcome built"},
		{name: "gap_reason", index: 1, change: func(c *Case) { c.Reason = "something else" }, want: "gap detail"},
		{name: "scope_reason", index: 2, change: func(c *Case) { c.Reason = "XMLFOREST" }, want: "out_of_scope detail"},
		{name: "rejected_now_built", index: 3, change: func(c *Case) { c.Tree = []byte(treeSelectParam) }, want: "outcome built"},
		{name: "malformed_tree", index: 0, change: func(c *Case) { c.Tree = []byte(`{"SelectStmt":7}`) }, want: "malformed parse tree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			item := sampleCases()[test.index]
			test.change(&item)
			err := Verify(item)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want %q", err, test.want)
			}
			// A failure must be traceable to its source without the fixture.
			if !strings.Contains(err.Error(), "source=sql/tiny.sql:") {
				t.Fatalf("error %v does not name the source location", err)
			}
		})
	}
}
