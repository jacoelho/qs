package qs

import "slices"

// LockStrength selects the row-locking strength of a LockClause.
type LockStrength uint8

const (
	lockUpdate LockStrength = iota + 1
	lockNoKeyUpdate
	lockShare
	lockKeyShare
)

type lockWait uint8

const (
	waitNormally lockWait = iota
	waitNoWait
	waitSkipLocked
)

// LockClause identifies row-lock strength, locked relations and wait behavior.
type LockClause struct {
	of       []string
	strength LockStrength
	wait     lockWait
}

// ForUpdate returns a FOR UPDATE lock clause.
func ForUpdate() LockClause { return LockClause{strength: lockUpdate} }

// ForNoKeyUpdate returns a FOR NO KEY UPDATE lock clause.
func ForNoKeyUpdate() LockClause { return LockClause{strength: lockNoKeyUpdate} }

// ForShare returns a FOR SHARE lock clause.
func ForShare() LockClause { return LockClause{strength: lockShare} }

// ForKeyShare returns a FOR KEY SHARE lock clause.
func ForKeyShare() LockClause { return LockClause{strength: lockKeyShare} }

// Of limits a lock clause to the named relations.
func (l LockClause) Of(relations ...string) LockClause { l.of = cloneSlice(relations); return l }

// NoWait makes a lock clause fail immediately when a row cannot be locked.
func (l LockClause) NoWait() LockClause { l.wait = waitNoWait; return l }

// SkipLocked makes a lock clause skip rows that cannot be locked immediately.
func (l LockClause) SkipLocked() LockClause { l.wait = waitSkipLocked; return l }

// rowLimit is the one bound on the number of rows a query returns. A query
// has at most one, so LIMIT and FETCH FIRST cannot both be set.
type rowLimit uint8

const (
	limitNone     rowLimit = iota // no bound
	limitCount                    // LIMIT count
	limitAll                      // LIMIT ALL
	limitWithTies                 // FETCH FIRST count ROWS WITH TIES
)

type queryTail struct {
	limit     Expr // the count of a limitCount or limitWithTies
	offset    Expr
	order     []Order
	locks     []LockClause
	rowLimit  rowLimit
	hasOffset bool
}

// withLimit returns t with its row limit replaced.
func (t queryTail) withLimit(kind rowLimit, count Expr) queryTail {
	t.rowLimit, t.limit = kind, count
	return t
}

func limitValue(n int) Expr {
	if n < 0 {
		return invalidExpr("pagination", "negative limit or offset")
	}
	return Param(n)
}
func (w *renderer) tail(t queryTail, allowLocks bool) {
	if w.err != nil {
		return
	}
	if len(t.order) > 0 {
		w.text(" ORDER BY ")
		w.orders(t.order)
		if w.stopped("ORDER BY", 0) {
			return
		}
	}
	switch t.rowLimit {
	case limitNone, limitWithTies:
	case limitCount:
		w.text(" LIMIT ")
		w.expr(t.limit)
		if w.stopped("LIMIT", 0) {
			return
		}
	case limitAll:
		w.text(" LIMIT ALL")
	default:
		w.fail(ErrInvalid, "pagination", "unknown row limit")
		return
	}
	if t.hasOffset {
		w.text(" OFFSET ")
		w.expr(t.offset)
		if w.stopped("OFFSET", 0) {
			return
		}
	}
	if t.rowLimit == limitWithTies {
		if !w.require(len(t.order) > 0, "FETCH WITH TIES", "requires ORDER BY") {
			return
		}
		skipsLocked := slices.ContainsFunc(t.locks, func(l LockClause) bool { return l.wait == waitSkipLocked })
		if !w.require(!skipsLocked, "FETCH WITH TIES", "cannot combine with SKIP LOCKED") {
			return
		}
		// PostgreSQL accepts WITH TIES only in the FETCH FIRST spelling, which
		// follows OFFSET.
		w.text(" FETCH FIRST (")
		w.expr(t.limit)
		if w.stopped("FETCH", 0) {
			return
		}
		w.text(") ROWS WITH TIES")
	}
	if len(t.locks) > 0 {
		if !w.require(allowLocks && !w.hasWindow, "locking", "not allowed for this query") {
			return
		}
		for _, l := range t.locks {
			w.lock(l)
		}
	}
}
func (w *renderer) lock(l LockClause) {
	switch l.strength {
	case lockUpdate:
		w.text(" FOR UPDATE")
	case lockNoKeyUpdate:
		w.text(" FOR NO KEY UPDATE")
	case lockShare:
		w.text(" FOR SHARE")
	case lockKeyShare:
		w.text(" FOR KEY SHARE")
	default:
		w.fail(ErrInvalid, "locking", "zero or unknown lock strength")
		return
	}
	if len(l.of) > 0 {
		w.text(" OF ")
		w.names(l.of)
	}
	switch l.wait {
	case waitNormally:
	case waitNoWait:
		w.text(" NOWAIT")
	case waitSkipLocked:
		w.text(" SKIP LOCKED")
	default:
		w.fail(ErrInvalid, "locking", "unknown wait behaviour")
	}
}
