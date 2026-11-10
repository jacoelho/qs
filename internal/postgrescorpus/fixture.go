package postgrescorpus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
)

// Status is the frozen classification of one corpus occurrence.
type Status string

const (
	// StatusVerified marks a tree the adapter builds into SQL that an
	// independent parser mapped back to the source query.
	StatusVerified Status = "verified"
	// StatusOutOfScope marks a tree using syntax qs deliberately omits.
	StatusOutOfScope Status = "out_of_scope"
	// StatusGap marks an in-scope tree with no public qs construction.
	StatusGap Status = "gap"
	// StatusRejected marks a tree the adapter builds but qs refuses to render.
	StatusRejected Status = "rejected"
)

// Case is one corpus occurrence: source provenance, the parse tree to build
// and the frozen expectation.
type Case struct {
	// Source is the PostgreSQL regression SQL file.
	Source string `json:"source"`
	// Origin tells whether the query is a direct statement or a wrapper body.
	Origin string `json:"origin"`
	// Wrapper is the enclosing statement type for a wrapper body.
	Wrapper string `json:"wrapper"`
	// Statement is the parse-node type of the source query.
	Statement string `json:"statement"`
	// ExpectedOutput is the PostgreSQL expected-output file, when one exists.
	ExpectedOutput string `json:"expected_output"`
	Status         Status `json:"status"`
	// Reason names the removed features, the gap or the render error. It is
	// empty for verified cases.
	Reason string `json:"reason"`
	// Shape identifies the normalized query shape across the corpus.
	Shape string `json:"shape"`
	// OriginalSQL is the source text, wrapper included.
	OriginalSQL string `json:"original_sql"`
	// WantSQL is the verified rendering of Tree.
	WantSQL string `json:"want_sql"`
	// Tree is libpg_query JSON for the query to build: the wrapper body for
	// wrapped occurrences, with location fields removed.
	Tree json.RawMessage `json:"tree"`
	// Families lists the grammar families the occurrence exercises.
	Families []string `json:"families"`
	// Normalization lists the documented equivalences the oracle applied.
	Normalization []string `json:"normalization"`
	// WantArgs are the parameter sentinels in source order.
	WantArgs []string `json:"want_args"`
	ID       int      `json:"id"`
	// Line is the one-based source line.
	Line    int  `json:"line"`
	Planner bool `json:"planner"`
	// ExpectedErrorHint records a comment-derived hint; it is not an oracle.
	ExpectedErrorHint bool `json:"expected_error_hint"`
}

// Cases decodes one fixture file. It yields count records whose IDs run
// contiguously from firstID, then stops; any other content ends the sequence
// with an error. Each Case owns its storage.
func Cases(reader io.Reader, firstID, count int) iter.Seq2[Case, error] {
	return func(yield func(Case, error) bool) {
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		for record := 0; ; record++ {
			var item Case
			err := decoder.Decode(&item)
			if errors.Is(err, io.EOF) {
				if record != count {
					yield(Case{}, fmt.Errorf("fixture ended after %d records, want %d", record, count))
				}
				return
			}
			if err != nil {
				yield(Case{}, fmt.Errorf("record %d: decode: %w", record, err))
				return
			}
			if record >= count {
				yield(Case{}, fmt.Errorf("record %d id=%d: extra record", record, item.ID))
				return
			}
			if item.ID != firstID+record {
				yield(Case{}, fmt.Errorf("record %d id=%d: want id %d", record, item.ID, firstID+record))
				return
			}
			if err := item.validate(); err != nil {
				yield(Case{}, fmt.Errorf("record %d id=%d: %w", record, item.ID, err))
				return
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

func (c Case) validate() error {
	if c.Source == "" || c.Line < 1 || c.Origin == "" || c.Statement == "" {
		return errors.New("incomplete source provenance")
	}
	if c.OriginalSQL == "" || c.Shape == "" {
		return errors.New("missing source SQL or shape")
	}
	if len(c.Tree) == 0 || c.Tree[0] != '{' {
		return errors.New("missing parse tree")
	}
	switch c.Status {
	case StatusVerified:
		if c.WantSQL == "" {
			return errors.New("verified case has no SQL expectation")
		}
		if c.Reason != "" {
			return errors.New("verified case has a reason")
		}
	case StatusOutOfScope, StatusGap, StatusRejected:
		if c.Reason == "" {
			return fmt.Errorf("%s case has no reason", c.Status)
		}
		if c.WantSQL != "" || len(c.WantArgs) != 0 {
			return fmt.Errorf("%s case has an SQL expectation", c.Status)
		}
	default:
		return fmt.Errorf("unknown status %q", c.Status)
	}
	return nil
}
