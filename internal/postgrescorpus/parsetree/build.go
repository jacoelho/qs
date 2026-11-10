package parsetree

import (
	"fmt"

	qs "github.com/jacoelho/qs"
)

// Kind classifies what Build made of a parse tree.
type Kind uint8

const (
	// Built means the tree was expressed through public qs constructors.
	Built Kind = iota + 1
	// OutOfScope means the tree uses syntax qs deliberately does not model.
	OutOfScope
	// Gap means the tree is in scope but has no public qs construction.
	Gap
)

// String returns the fixture spelling of the kind.
func (k Kind) String() string {
	switch k {
	case Built:
		return "built"
	case OutOfScope:
		return "out_of_scope"
	case Gap:
		return "gap"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// Outcome is the result of building one parse tree.
type Outcome struct {
	// Statement is set only when Kind is Built.
	Statement qs.Statement
	// Detail names the removed features for OutOfScope and the reason for Gap.
	Detail string
	Kind   Kind
}

// Build constructs the qs statement for one libpg_query JSON query tree.
// param supplies the Go value bound in place of source parameter $number.
// The error reports a tree that is not libpg_query JSON; unsupported SQL is
// an Outcome, not an error.
func Build(tree []byte, param func(number int) any) (Outcome, error) {
	root, err := decode(tree)
	if err != nil {
		return Outcome{}, err
	}
	// Scope is decided before construction so a query using removed syntax
	// is never reported as a gap for some unrelated node.
	if features := removedFeatures(root); features != "" {
		return Outcome{Kind: OutOfScope, Detail: features}, nil
	}
	b := &builder{param: param}
	statement := b.statement(tagged(root, &b.faults, "root"))
	if b.faults.err != nil {
		return Outcome{}, fmt.Errorf("malformed parse tree: %w", b.faults.err)
	}
	if b.reason != "" {
		return Outcome{Kind: Gap, Detail: b.reason}, nil
	}
	return Outcome{Kind: Built, Statement: statement}, nil
}

// builder carries the state of one tree. Construction continues after the
// first gap with zero values, which are discarded; only the first reason is
// kept.
type builder struct {
	param  func(number int) any
	faults faults
	reason string
}

func (b *builder) gapf(format string, args ...any) {
	if b.reason == "" {
		b.reason = fmt.Sprintf(format, args...)
	}
}

func (b *builder) stopped() bool { return b.reason != "" || b.faults.err != nil }
