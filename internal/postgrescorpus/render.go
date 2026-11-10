package postgrescorpus

import (
	"fmt"
	"slices"

	"github.com/jacoelho/qs/internal/postgrescorpus/parsetree"
)

// Outcome is what the adapter and renderer made of one tree, before any
// oracle has judged the SQL.
type Outcome string

const (
	// OutcomeBuilt carries SQL and arguments.
	OutcomeBuilt Outcome = "built"
	// OutcomeOutOfScope names the removed features in Detail.
	OutcomeOutOfScope Outcome = "out_of_scope"
	// OutcomeGap names the missing construction in Detail.
	OutcomeGap Outcome = "gap"
	// OutcomeRejected carries the qs render error in Detail.
	OutcomeRejected Outcome = "rejected"
)

// Rendering is the adapter's answer for one tree.
type Rendering struct {
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail"`
	SQL     string  `json:"sql"`
	// Args are the bound parameter sentinels in placeholder order.
	Args []string `json:"args"`
}

// ParameterSentinel is the value bound for source parameter $number of
// occurrence id. It is unique per occurrence and slot, so a rendering that
// drops, repeats or reorders parameters is detectable from its arguments.
func ParameterSentinel(id, number int) string {
	return fmt.Sprintf("qs-postgres-corpus-%d-param-%d", id, number)
}

// Render builds tree through the public qs API and renders it. The error
// reports a tree that is not a libpg_query query; every SQL-level result is
// a Rendering.
func Render(id int, tree []byte) (Rendering, error) {
	built, err := parsetree.Build(tree, func(number int) any { return ParameterSentinel(id, number) })
	if err != nil {
		return Rendering{}, fmt.Errorf("id=%d: %w", id, err)
	}
	switch built.Kind {
	case parsetree.OutOfScope:
		return Rendering{Outcome: OutcomeOutOfScope, Detail: built.Detail}, nil
	case parsetree.Gap:
		return Rendering{Outcome: OutcomeGap, Detail: built.Detail}, nil
	case parsetree.Built:
	}
	sql, bound, renderErr := built.Statement.ToSQL()
	if renderErr != nil {
		// A refusal by qs is a measured outcome, not a failure to measure.
		return Rendering{Outcome: OutcomeRejected, Detail: renderErr.Error()}, nil //nolint:nilerr // see above
	}
	args := make([]string, len(bound))
	for index, value := range bound {
		text, ok := value.(string)
		// qs numbers placeholders in rendering order and cannot repeat one,
		// so only sources numbered $1..$n in that order survive a round trip.
		if !ok || text != ParameterSentinel(id, index+1) {
			return Rendering{Outcome: OutcomeGap, Detail: "parameter numbers do not follow rendering order"}, nil
		}
		args[index] = text
	}
	return Rendering{Outcome: OutcomeBuilt, SQL: sql, Args: args}, nil
}

// frozenOutcomes maps each fixture status to the outcome that reproduces it.
var frozenOutcomes = map[Status]Outcome{
	StatusVerified:   OutcomeBuilt,
	StatusOutOfScope: OutcomeOutOfScope,
	StatusGap:        OutcomeGap,
	StatusRejected:   OutcomeRejected,
}

// Verify renders item's tree and compares the result with its frozen
// expectation.
func Verify(item Case) error {
	where := fmt.Sprintf("id=%d source=%s:%d origin=%s", item.ID, item.Source, item.Line, item.Origin)
	got, err := Render(item.ID, item.Tree)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	want := frozenOutcomes[item.Status]
	if got.Outcome != want {
		return fmt.Errorf("%s: outcome %s (%s), want %s for status %s; source=%q", where, got.Outcome, got.describe(), want, item.Status, item.OriginalSQL)
	}
	switch item.Status {
	case StatusVerified:
		if got.SQL != item.WantSQL {
			return fmt.Errorf("%s: SQL mismatch; got=%q want=%q source=%q", where, got.SQL, item.WantSQL, item.OriginalSQL)
		}
		if !slices.Equal(got.Args, item.WantArgs) {
			return fmt.Errorf("%s: arguments got %q want %q; source=%q", where, got.Args, item.WantArgs, item.OriginalSQL)
		}
	case StatusOutOfScope, StatusGap:
		if got.Detail != item.Reason {
			return fmt.Errorf("%s: %s detail %q, want %q; source=%q", where, got.Outcome, got.Detail, item.Reason, item.OriginalSQL)
		}
	case StatusRejected:
		// The render error text belongs to qs; only the refusal is frozen.
	}
	return nil
}

func (r Rendering) describe() string {
	if r.Outcome == OutcomeBuilt {
		return r.SQL
	}
	return r.Detail
}
