package qx

type FetchMode uint8

const (
	OnlyRows FetchMode = iota + 1
	WithTies
)

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

// LockClause identifies row-lock strength, locked relations and wait behaviour.
type LockClause struct {
	strength LockStrength
	of       []string
	wait     lockWait
}

func ForUpdate() LockClause                            { return LockClause{strength: lockUpdate} }
func ForNoKeyUpdate() LockClause                       { return LockClause{strength: lockNoKeyUpdate} }
func ForShare() LockClause                             { return LockClause{strength: lockShare} }
func ForKeyShare() LockClause                          { return LockClause{strength: lockKeyShare} }
func (l LockClause) Of(relations ...string) LockClause { l.of = cloneSlice(relations); return l }
func (l LockClause) NoWait() LockClause                { l.wait = waitNoWait; return l }
func (l LockClause) SkipLocked() LockClause            { l.wait = waitSkipLocked; return l }

type queryTail struct {
	order                                   []Order
	limit, offset, fetch                    Expr
	hasLimit, limitAll, hasOffset, hasFetch bool
	fetchMode                               FetchMode
	locks                                   []LockClause
}

func limitValue(n int) Expr {
	if n < 0 {
		return invalidExpr("pagination", "negative limit, offset or fetch count")
	}
	return Param(n)
}
func (w *renderer) tail(t queryTail, allowLocks bool) {
	if !w.require(!(t.hasLimit && t.hasFetch), "pagination", "LIMIT and FETCH are mutually exclusive") {
		return
	}
	if len(t.order) > 0 {
		w.text(" ORDER BY ")
		w.orders(t.order)
	}
	if t.hasLimit {
		w.text(" LIMIT ")
		if t.limitAll {
			w.text("ALL")
		} else {
			w.expr(t.limit)
		}
	}
	if t.hasOffset {
		w.text(" OFFSET ")
		w.expr(t.offset)
	}
	if t.hasFetch {
		if !w.require(t.fetchMode == OnlyRows || t.fetchMode == WithTies, "FETCH", "unknown mode") {
			return
		}
		if t.fetchMode == WithTies {
			w.feature(PostgreSQL13, "FETCH WITH TIES")
			if !w.require(len(t.order) > 0, "FETCH WITH TIES", "requires ORDER BY") {
				return
			}
			if !w.require(len(t.locks) == 0, "FETCH WITH TIES", "cannot combine with row locking") {
				return
			}
		}
		w.text(" FETCH FIRST (")
		w.expr(t.fetch)
		w.text(") ROWS ")
		if t.fetchMode == WithTies {
			w.text("WITH TIES")
		} else {
			w.text("ONLY")
		}
	}
	if len(t.locks) > 0 {
		if !w.require(allowLocks, "locking", "not allowed for this query") {
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
