package qx

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
	kind   boundKind
	offset Expr
}

func UnboundedPreceding() Bound { return Bound{kind: boundUnboundedPreceding} }
func UnboundedFollowing() Bound { return Bound{kind: boundUnboundedFollowing} }
func CurrentRow() Bound         { return Bound{kind: boundCurrentRow} }
func Preceding(count int) Bound {
	if count < 0 {
		return Bound{kind: boundPreceding, offset: invalidExpr("frame", "negative offset")}
	}
	return PrecedingExpr(Param(count))
}
func Following(count int) Bound {
	if count < 0 {
		return Bound{kind: boundFollowing, offset: invalidExpr("frame", "negative offset")}
	}
	return FollowingExpr(Param(count))
}
func PrecedingExpr(offset Expr) Bound { return Bound{kind: boundPreceding, offset: offset} }
func FollowingExpr(offset Expr) Bound { return Bound{kind: boundFollowing, offset: offset} }

type FrameExclusion uint8

const (
	ExcludeNoOthers FrameExclusion = iota + 1
	ExcludeCurrentRow
	ExcludeGroup
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
	frame      windowFrame
	start, end Bound
	shorthand  bool
	exclude    FrameExclusion
	hasExclude bool
}

func Window() WindowSpec                         { return WindowSpec{} }
func (s WindowSpec) Base(name string) WindowSpec { s.base = name; return s }
func (s WindowSpec) PartitionBy(expressions ...Expr) WindowSpec {
	s.partition = slices.Concat(s.partition, expressions)
	return s
}
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
	separator := false
	if s.base != "" {
		if !w.require(len(s.partition) == 0, "WINDOW", "an inherited window cannot add PARTITION BY") {
			return
		}
		w.identifierPart(s.base)
		separator = true
	}
	if len(s.partition) != 0 {
		if separator {
			w.byte(' ')
		}
		w.text("PARTITION BY ")
		w.exprs(s.partition, ", ")
		separator = true
	}
	if len(s.order) != 0 {
		if separator {
			w.byte(' ')
		}
		w.text("ORDER BY ")
		w.orders(s.order)
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
		} else {
			w.text(" BETWEEN ")
			w.bound(s.start)
			w.text(" AND ")
			w.bound(s.end)
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

func Rollup(expressions ...Expr) Expr      { return listExpr("ROLLUP", expressions) }
func Cube(expressions ...Expr) Expr        { return listExpr("CUBE", expressions) }
func GroupingSets(sets ...Expr) Expr       { return listExpr("GROUPING SETS ", sets) }
func GroupingSet(expressions ...Expr) Expr { return listExpr("", expressions) }
