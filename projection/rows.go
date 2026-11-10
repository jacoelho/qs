package projection

import "iter"

// Rows is the part of a result set that All and Collect read. pgx.Rows and
// *database/sql.Rows both implement it. A caller's own Rows may wrap one of them, with
// a Scan that appends destinations as ScanInto describes.
type Rows interface {
	// Next advances to the next row and reports whether there is one.
	Next() bool
	// Scan reads the current row into dest. It must not keep dest after it returns:
	// All and Collect reuse the list for the next row.
	Scan(dest ...any) error
	// Err returns the error that ended iteration, if any.
	Err() error
}

// All returns an iterator over the remaining rows, each read as Scan reads it. A
// failed row yields the zero D with that row's error, unchanged, and ends the
// iteration. Once rows is exhausted, a non-nil rows.Err is yielded the same way, so a
// loop that checks every error has also checked rows.Err.
//
// All does not close rows, because drivers disagree on Close: the caller keeps its
// deferred Close, which also covers leaving the loop early or on an error. The
// iterator reads rows as it goes, so it is single-use.
//
// All and Collect reuse their working memory from one row to the next, so they
// allocate less than a loop over Scan. The values they produce share nothing with
// each other.
func (p Projection[D]) All(rows Rows) iter.Seq2[D, error] {
	return func(yield func(D, error) bool) {
		var reused scratch
		for rows.Next() {
			var d D
			if err := p.read(&d, rows.Scan, &reused); err != nil {
				var zero D
				yield(zero, err)
				return
			}
			if !yield(d, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			var zero D
			yield(zero, err)
		}
	}
}

// Collect reads the remaining rows into a slice, each as Scan reads it. It returns
// the first error from a row or from rows.Err, unchanged, with a nil slice. A result
// without rows is an empty, non-nil slice. Like All, Collect does not close rows.
func (p Projection[D]) Collect(rows Rows) ([]D, error) {
	collected := []D{}
	var reused scratch
	for rows.Next() {
		// Each row is read into its slot: a D of its own would cost an allocation a row.
		var zero D
		collected = append(collected, zero)
		if err := p.read(&collected[len(collected)-1], rows.Scan, &reused); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return collected, nil
}
