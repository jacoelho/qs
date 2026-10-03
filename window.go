package qs

import "slices"

type boundKind uint8

const (
	boundUnboundedPreceding boundKind = iota + 1
	boundPreceding
	boundCurrentRow
	boundFollowing
	boundUnboundedFollowing
)

// Bound is a SQL window-frame boundary.
type Bound struct {
	offset Expr
	kind   boundKind
}

// UnboundedPreceding returns an unbounded lower frame boundary.
func UnboundedPreceding() Bound { return Bound{kind: boundUnboundedPreceding} }

// UnboundedFollowing returns an unbounded upper frame boundary.
func UnboundedFollowing() Bound { return Bound{kind: boundUnboundedFollowing} }

// CurrentRow returns a frame boundary at the current row.
func CurrentRow() Bound { return Bound{kind: boundCurrentRow} }

// Preceding returns a frame boundary count rows before the current row.
func Preceding(count int) Bound {
	if count < 0 {
		return Bound{kind: boundPreceding, offset: invalidExpr("frame", "negative offset")}
	}
	return PrecedingExpr(Param(count))
}

// Following returns a frame boundary count rows after the current row.
func Following(count int) Bound {
	if count < 0 {
		return Bound{kind: boundFollowing, offset: invalidExpr("frame", "negative offset")}
	}
	return FollowingExpr(Param(count))
}

// PrecedingExpr returns a frame boundary whose offset is an expression.
func PrecedingExpr(offset Expr) Bound { return Bound{kind: boundPreceding, offset: offset} }

// FollowingExpr returns a frame boundary whose offset is an expression.
func FollowingExpr(offset Expr) Bound { return Bound{kind: boundFollowing, offset: offset} }

// FrameExclusion selects which rows a window frame excludes.
type FrameExclusion uint8

const (
	// ExcludeNoOthers keeps every row in the frame.
	ExcludeNoOthers FrameExclusion = iota + 1
	// ExcludeCurrentRow excludes the current row from the frame.
	ExcludeCurrentRow
	// ExcludeGroup excludes the peer group containing the current row.
	ExcludeGroup
	// ExcludeTies excludes peers of the current row but keeps the current row.
	ExcludeTies
)

type windowFrame uint8

const (
	frameNone windowFrame = iota
	frameRows
	frameRange
	frameGroups
)

// WindowSpec is an immutable window definition. Named references use Base.
type WindowSpec struct {
	base       string
	partition  []Expr
	order      []Order
	start      Bound
	end        Bound
	frame      windowFrame
	shorthand  bool
	exclude    FrameExclusion
	hasExclude bool
}

// Window returns an empty window definition.
func Window() WindowSpec { return WindowSpec{} }

// Base makes the window inherit from a previously named window definition.
func (s WindowSpec) Base(name string) WindowSpec { s.base = name; return s }

// PartitionBy appends expressions to the window's PARTITION BY clause.
func (s WindowSpec) PartitionBy(expressions ...Expr) WindowSpec {
	s.partition = slices.Concat(s.partition, expressions)
	return s
}

// OrderBy appends terms to the window's ORDER BY clause.
func (s WindowSpec) OrderBy(terms ...Order) WindowSpec {
	s.order = slices.Concat(s.order, terms)
	return s
}

// RowsBetween uses an explicit lower and upper ROWS frame boundary.
func (s WindowSpec) RowsBetween(start, end Bound) WindowSpec {
	s.frame = frameRows
	s.start = start
	s.end = end
	s.shorthand = false
	return s
}

// RangeBetween uses an explicit lower and upper RANGE frame boundary.
func (s WindowSpec) RangeBetween(start, end Bound) WindowSpec {
	s.frame = frameRange
	s.start = start
	s.end = end
	s.shorthand = false
	return s
}

// GroupsBetween uses an explicit lower and upper GROUPS frame boundary.
func (s WindowSpec) GroupsBetween(start, end Bound) WindowSpec {
	s.frame = frameGroups
	s.start = start
	s.end = end
	s.shorthand = false
	return s
}

// Rows uses the PostgreSQL single-bound spelling whose implicit end is CURRENT ROW.
func (s WindowSpec) Rows(start Bound) WindowSpec {
	s = s.RowsBetween(start, CurrentRow())
	s.shorthand = true
	return s
}

// Range uses the PostgreSQL single-bound spelling whose implicit end is CURRENT ROW.
func (s WindowSpec) Range(start Bound) WindowSpec {
	s = s.RangeBetween(start, CurrentRow())
	s.shorthand = true
	return s
}

// Groups uses the PostgreSQL single-bound spelling whose implicit end is CURRENT ROW.
func (s WindowSpec) Groups(start Bound) WindowSpec {
	s = s.GroupsBetween(start, CurrentRow())
	s.shorthand = true
	return s
}

// Exclude adds an explicit frame exclusion. Calling it with an unknown value
// is rejected during rendering; an omitted exclusion remains valid.
func (s WindowSpec) Exclude(exclusion FrameExclusion) WindowSpec {
	s.exclude = exclusion
	s.hasExclude = true
	return s
}

type namedWindow struct {
	name string
	spec WindowSpec
}

// validateWindows checks only the local WINDOW clause. PostgreSQL resolves
// each base name against earlier definitions, so this pass deliberately does
// not inspect OVER references or maintain renderer-wide window state.
func (w *renderer) validateWindows(windows []namedWindow) bool {
	for i, current := range windows {
		for j := range i {
			if windows[j].name == current.name {
				w.fail(ErrInvalid, "WINDOW", "duplicate window name")
				return false
			}
		}
		if current.spec.base == "" {
			continue
		}
		baseIndex := -1
		for j := range i {
			if windows[j].name == current.spec.base {
				baseIndex = j
				break
			}
		}
		if !w.require(baseIndex >= 0, "WINDOW", "base window must be defined earlier") {
			return false
		}
		orderCount := inheritedWindowOrderCount(windows, baseIndex)
		if len(current.spec.order) != 0 && orderCount != 0 {
			w.fail(ErrInvalid, "WINDOW", "cannot override ORDER BY clause of inherited window")
			return false
		}
		if windows[baseIndex].spec.frame != frameNone {
			w.fail(ErrInvalid, "WINDOW", "cannot copy window with a frame clause")
			return false
		}
		if len(current.spec.order) != 0 {
			orderCount = len(current.spec.order)
		}
		if !w.frameOrder(current.spec, orderCount) {
			return false
		}
	}
	return true
}

func inheritedWindowOrderCount(windows []namedWindow, index int) int {
	for index >= 0 {
		spec := windows[index].spec
		if len(spec.order) != 0 {
			return len(spec.order)
		}
		if spec.base == "" {
			return 0
		}
		index = previousWindowIndex(windows, index, spec.base)
	}
	return 0
}

func previousWindowIndex(windows []namedWindow, before int, name string) int {
	for i := before - 1; i >= 0; i-- {
		if windows[i].name == name {
			return i
		}
	}
	return -1
}

func (w *renderer) frameOrder(s WindowSpec, orderCount int) bool {
	if s.frame == frameGroups && !w.require(orderCount > 0, "frame", "GROUPS requires ORDER BY") {
		return false
	}
	offset := s.start.kind == boundPreceding || s.start.kind == boundFollowing || s.end.kind == boundPreceding || s.end.kind == boundFollowing
	return s.frame != frameRange || !offset || w.require(orderCount == 1, "frame", "RANGE with offsets requires exactly one ordering term")
}

func (w *renderer) window(s WindowSpec) {
	if w.err != nil {
		return
	}
	separator := false
	if s.base != "" {
		if !w.require(len(s.partition) == 0, "WINDOW", "an inherited window cannot add PARTITION BY") {
			return
		}
		w.identifierPart(s.base)
		if w.stopped("base window", 0) {
			return
		}
		separator = true
	}
	if len(s.partition) != 0 {
		if separator {
			w.byte(' ')
		}
		w.text("PARTITION BY ")
		w.exprs(s.partition, ", ")
		if w.stopped("PARTITION BY", 0) {
			return
		}
		separator = true
	}
	if len(s.order) != 0 {
		if separator {
			w.byte(' ')
		}
		w.text("ORDER BY ")
		w.orders(s.order)
		if w.stopped("ORDER BY", 0) {
			return
		}
		separator = true
	}
	if s.frame != frameNone {
		if !w.require(s.start.kind >= boundUnboundedPreceding && s.start.kind < boundUnboundedFollowing && s.end.kind > boundUnboundedPreceding && s.end.kind <= boundUnboundedFollowing && s.start.kind <= s.end.kind, "frame", "invalid boundary order") {
			return
		}
		if s.base == "" && !w.frameOrder(s, len(s.order)) {
			return
		}
		if separator {
			w.byte(' ')
		}
		switch s.frame {
		case frameRows:
			w.text("ROWS")
		case frameRange:
			w.text("RANGE")
		case frameGroups:
			w.text("GROUPS")
		default:
			w.fail(ErrInvalid, "frame", "unknown frame mode")
			return
		}
		if s.shorthand {
			w.byte(' ')
			w.bound(s.start)
			if w.stopped("frame start", 0) {
				return
			}
		} else {
			w.text(" BETWEEN ")
			w.bound(s.start)
			if w.stopped("frame start", 0) {
				return
			}
			w.text(" AND ")
			w.bound(s.end)
			if w.stopped("frame end", 0) {
				return
			}
		}
	}
	if s.hasExclude {
		if !w.require(s.frame != frameNone, "frame", "EXCLUDE requires an explicit frame") {
			return
		}
		switch s.exclude {
		case ExcludeNoOthers:
			w.text(" EXCLUDE NO OTHERS")
		case ExcludeCurrentRow:
			w.text(" EXCLUDE CURRENT ROW")
		case ExcludeGroup:
			w.text(" EXCLUDE GROUP")
		case ExcludeTies:
			w.text(" EXCLUDE TIES")
		default:
			w.fail(ErrInvalid, "frame", "unknown frame exclusion")
		}
	}
}
func (w *renderer) bound(b Bound) {
	switch b.kind {
	case boundUnboundedPreceding:
		w.text("UNBOUNDED PRECEDING")
	case boundUnboundedFollowing:
		w.text("UNBOUNDED FOLLOWING")
	case boundCurrentRow:
		w.text("CURRENT ROW")
	case boundPreceding:
		w.expr(b.offset)
		w.text(" PRECEDING")
	case boundFollowing:
		w.expr(b.offset)
		w.text(" FOLLOWING")
	default:
		w.fail(ErrInvalid, "frame", "zero boundary")
	}
}

// Rollup returns a ROLLUP grouping expression.
func Rollup(expressions ...Expr) Expr { return listExpr("ROLLUP", expressions) }

// Cube returns a CUBE grouping expression.
func Cube(expressions ...Expr) Expr { return listExpr("CUBE", expressions) }

// GroupingSets returns a GROUPING SETS expression from the supplied sets.
func GroupingSets(sets ...Expr) Expr { return listExpr("GROUPING SETS ", sets) }

// GroupingSet returns one grouping set from its expressions.
func GroupingSet(expressions ...Expr) Expr { return listExpr("", expressions) }
