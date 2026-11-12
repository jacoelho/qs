package projection

import (
	"errors"
	"fmt"
	"iter"
)

// ErrNoClose reports a Rows that has neither Close() nor Close() error. All,
// Collect and CollectPtr return it before reading a row.
var ErrNoClose = errors.New("projection: rows has no Close method")

// Rows is the part of a result set that All, Collect and CollectPtr read. pgx.Rows and
// *database/sql.Rows both implement it. A caller's own Rows may wrap one of them, with
// a Scan that appends destinations as ScanInto describes.
//
// They also close the result set. The drivers disagree on the signature,
// pgx.Rows having Close() and *sql.Rows Close() error, so Close is not a method here:
// the value must have one of the two. A wrapper that embeds a driver's rows has it
// already; one that holds them in a named field must forward it.
type Rows interface {
	// Next advances to the next row and reports whether there is one.
	Next() bool
	// Scan reads the current row into dest. It must not keep dest after it returns:
	// the row loops reuse the list for the next row.
	Scan(dest ...any) error
	// Err returns the error that ended iteration, if any. It is called after Close.
	Err() error
}

// closerOf returns the Close of rows as a function, whichever signature it has.
func closerOf(rows Rows) (func() error, error) {
	switch c := rows.(type) {
	case interface{ Close() error }:
		return c.Close, nil
	case interface{ Close() }:
		return func() error { c.Close(); return nil }, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrNoClose, rows)
	}
}

// finish closes rows and returns the error that ended the result set: rows.Err,
// else the error from Close. Close comes first because a driver may only learn of a
// failure while closing and report it through Err.
func finish(rows Rows, closeRows func() error) error {
	closeErr := closeRows()
	if err := rows.Err(); err != nil {
		return err
	}
	return closeErr
}

// All returns an iterator over the remaining rows, each read as Scan reads it. A
// failed row yields the zero D with that row's error, unchanged, and ends the
// iteration. Once rows is exhausted, a non-nil rows.Err, or else a non-nil error from
// Close, is yielded the same way, so a loop that checks every error has checked all
// three.
//
// All closes rows when the iteration ends, however it ends: exhausted, failed, left
// with break or return, or by a panic. A loop left early has nowhere to receive an
// error from Close, so that error is dropped; of the two drivers only *sql.Rows
// returns one. A deferred Close the caller keeps is harmless. The iterator reads rows
// as it goes, so it is single-use, and an iterator that is never ranged over closes
// nothing. A Rows without Close yields ErrNoClose and no rows.
//
// All, Collect and CollectPtr reuse their working memory from one row to the next, so they
// allocate less than a loop over Scan. The values they produce share nothing with
// each other.
func (p Projection[D]) All(rows Rows) iter.Seq2[D, error] {
	return func(yield func(D, error) bool) {
		var zero D
		closeRows, err := closerOf(rows)
		if err != nil {
			yield(zero, err)
			return
		}
		exhausted := false
		defer func() {
			if !exhausted {
				_ = closeRows() //nolint:errcheck // a loop left early has nowhere to receive it.
			}
		}()
		var reused scratch
		for rows.Next() {
			var d D
			if err := p.read(&d, rows.Scan, &reused); err != nil {
				yield(zero, err)
				return
			}
			if !yield(d, nil) {
				return
			}
		}
		exhausted = true
		if err := finish(rows, closeRows); err != nil {
			yield(zero, err)
		}
	}
}

// Collect reads the remaining rows into a slice, each as Scan reads it, and closes
// rows before it returns, also when a row fails or a dest panics. It returns the
// first error with a nil slice: a row's error, else rows.Err, else the error from
// Close, each unchanged. A result without rows is an empty, non-nil slice. A Rows
// without Close returns ErrNoClose and reads nothing. A deferred Close the caller
// keeps is harmless.
func (p Projection[D]) Collect(rows Rows) (collected []D, err error) {
	closeRows, err := closerOf(rows)
	if err != nil {
		return nil, err
	}
	defer func() {
		ended := finish(rows, closeRows)
		if err == nil {
			err = ended
		}
		if err != nil {
			collected = nil
		}
	}()
	collected = []D{}
	var reused scratch
	for rows.Next() {
		// Each row is read into its slot: a D of its own would cost an allocation a row.
		var zero D
		collected = append(collected, zero)
		if err := p.read(&collected[len(collected)-1], rows.Scan, &reused); err != nil {
			return nil, err
		}
	}
	return collected, nil
}

// CollectPtr is Collect for callers that hold rows by pointer. Each row is read as
// ScanInto reads into a new D, so no element is nil, no two share a D, and a pointer a
// set kept into its row points into the element returned for that row. Rows are closed
// and errors returned as Collect does: the first error comes with a nil slice, and a
// result without rows is an empty, non-nil slice. It allocates each D, which Collect
// does not.
func (p Projection[D]) CollectPtr(rows Rows) (collected []*D, err error) {
	closeRows, err := closerOf(rows)
	if err != nil {
		return nil, err
	}
	defer func() {
		ended := finish(rows, closeRows)
		if err == nil {
			err = ended
		}
		if err != nil {
			collected = nil
		}
	}()
	collected = []*D{}
	var reused scratch
	for rows.Next() {
		d := new(D)
		if err := p.read(d, rows.Scan, &reused); err != nil {
			return nil, err
		}
		collected = append(collected, d)
	}
	return collected, nil
}
