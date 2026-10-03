package qs

import (
	"errors"
	"strings"
)

// Sentinel errors can be matched with errors.Is.
var (
	// ErrInvalid identifies structurally invalid queries, inputs or rendering options.
	ErrInvalid = errors.New("qs: invalid query")
	// ErrUnsupported identifies syntax unavailable in the selected PostgreSQL version.
	ErrUnsupported = errors.New("qs: unsupported PostgreSQL feature")
	// ErrParameterLimit identifies arguments exceeding the configured render limit.
	ErrParameterLimit = errors.New("qs: parameter limit exceeded")
	// ErrDepth identifies a query cycle or traversal exceeding the configured depth.
	ErrDepth = errors.New("qs: cyclic query or nesting limit exceeded")
)

// RenderError identifies a query validation or rendering failure without bound values.
type RenderError struct {
	// Cause supports errors.Is matching and is not allowed to contain bound values.
	Cause error
	// Clause identifies the SQL construct or option that failed validation.
	Clause string
	// Detail explains the validation failure without including bound values.
	Detail string
	// Path locates the failure from outermost to innermost structural scope.
	// Indexes are one-based. Each returned error owns its path; labels contain
	// no bound values, caller names or SQL fragments.
	Path []string
}

// Error describes the failure; its text is diagnostic, not a parsing format.
// A nil receiver returns "<nil>".
func (e *RenderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "qs: render error"
	if e.Cause != nil {
		message = e.Cause.Error()
	}
	if e.Clause != "" || e.Detail != "" {
		message += " (" + e.Clause + "): " + e.Detail
	}
	if len(e.Path) != 0 {
		message += " [path: " + strings.Join(e.Path, " → ") + "]"
	}
	return message
}

// Unwrap returns the sentinel cause, or nil for a nil receiver.
func (e *RenderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
