package ui

// Mouse coordinate→target hit-testing. Kept as a pure helper so the mouse
// handling in Update stays thin and the geometry is unit-testable without a
// terminal.

// hitRegion identifies which part of the layout a screen coordinate falls in.
type hitRegion int

const (
	hitNone    hitRegion = iota
	hitList              // inside the file-list pane (line is body-local list line)
	hitDivider           // on the vertical divider between list and diff
	hitDiff              // inside the diff pane (line is a rendered diff row)
)

// hit is the result of a hit-test: the region plus a line index whose meaning
// depends on the region (list: body-local line; diff: rendered diff row).
type hit struct {
	region hitRegion
	line   int
}

// layout is the geometry the hit-test needs. All coordinates are 0-based screen
// columns/rows; (x,y) is the click position.
type layout struct {
	headerHeight int // rows occupied by the header (body starts at this y)
	bodyHeight   int // visible rows of the body (list/diff area)
	width        int // total terminal width
	listWidth    int // width of the file-list pane (columns [0,listWidth))
	diffYOffset  int // diff viewport scroll offset (rendered rows scrolled past top)
}

// dividerColumn returns the x column the vertical divider occupies.
func (l layout) dividerColumn() int { return l.listWidth }

// hitTest maps a screen (x,y) to a target region + line. Pure function.
//
//   - Above/below the body, or outside the width → hitNone.
//   - x in [0,listWidth)            → hitList, line = y - headerHeight.
//   - x == listWidth (divider col)  → hitDivider.
//   - x in (listWidth, width)       → hitDiff, line = (y-headerHeight)+diffYOffset.
func hitTest(l layout, x, y int) hit {
	if x < 0 || y < 0 || x >= l.width {
		return hit{region: hitNone}
	}
	bodyTop := l.headerHeight
	bodyBottom := l.headerHeight + l.bodyHeight // exclusive
	if y < bodyTop || y >= bodyBottom {
		return hit{region: hitNone}
	}
	bodyLine := y - bodyTop
	switch {
	case x < l.listWidth:
		return hit{region: hitList, line: bodyLine}
	case x == l.dividerColumn():
		return hit{region: hitDivider}
	default:
		return hit{region: hitDiff, line: bodyLine + l.diffYOffset}
	}
}
