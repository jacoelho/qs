package projection

import (
	"slices"

	"github.com/jacoelho/qs"
)

// Column associates one result expression with metadata of type M.
// Its zero value contains a zero expression, which qs rejects when rendered.
type Column[M any] struct {
	metadata   M
	expression qs.Expr
}

// Projection is an ordered collection of expression/metadata associations.
// Its zero value is empty. All copies retain shallow references to their contents.
type Projection[M any] struct {
	columns []Column[M]
}

// Output associates expression with metadata without interpreting or validating
// either value. Zero, nil and duplicate metadata values are permitted.
func Output[M any](expression qs.Expr, metadata M) Column[M] {
	return Column[M]{expression: expression, metadata: metadata}
}

// Named adds alias to an unaliased, single-result expression and records alias as
// string metadata. It is equivalent to Output(expression.As(alias), alias).
// It adds rather than replaces an alias; alias render checks belong to qs.
func Named(expression qs.Expr, alias string) Column[string] {
	return Output(expression.As(alias), alias)
}

// New constructs a projection, copying the supplied column positions.
// It accepts empty input and performs no expression or metadata validation.
func New[M any](columns ...Column[M]) Projection[M] {
	return Projection[M]{columns: slices.Clone(columns)}
}

// With returns a projection with more appended, copying column positions into
// independent storage. It leaves p unchanged. Empty input preserves its content.
func (p Projection[M]) With(more ...Column[M]) Projection[M] {
	columns := make([]Column[M], len(p.columns)+len(more))
	copy(columns, p.columns)
	copy(columns[len(p.columns):], more)
	return Projection[M]{columns: columns}
}

// Expressions returns the declared expressions in order, with owned slice positions.
// Nested query graphs and application values remain shared. Empty slice nilness is
// unspecified.
func (p Projection[M]) Expressions() []qs.Expr {
	expressions := make([]qs.Expr, len(p.columns))
	for i, column := range p.columns {
		expressions[i] = column.expression
	}
	return expressions
}

// Metadata returns the associated metadata in order, with owned slice positions.
// Metadata contents remain shallow and are never invoked or validated. Empty slice
// nilness is unspecified.
func (p Projection[M]) Metadata() []M {
	metadata := make([]M, len(p.columns))
	for i, column := range p.columns {
		metadata[i] = column.metadata
	}
	return metadata
}
