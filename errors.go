package qs

import "errors"

// Sentinel errors can be matched with errors.Is.
var (
	ErrInvalid        = errors.New("qs: invalid query")
	ErrUnsupported    = errors.New("qs: unsupported PostgreSQL feature")
	ErrParameterLimit = errors.New("qs: parameter limit exceeded")
	ErrDepth          = errors.New("qs: cyclic query or nesting limit exceeded")
)

// RenderError identifies a query validation or rendering failure without bound values.
type RenderError struct {
	Clause string
	Detail string
	Cause  error
}

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
	return message
}

func (e *RenderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
