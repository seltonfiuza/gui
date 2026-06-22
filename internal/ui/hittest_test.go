package ui

import "testing"

func TestHitTest(t *testing.T) {
	// header=1 row, body=20 rows, total width=80, list width=30, divider at x=30,
	// a 1-col scrollbar at x=79, diff scrolled down by 5 rendered rows.
	l := layout{headerHeight: 1, bodyHeight: 20, width: 80, listWidth: 30, scrollbarWidth: 1, diffYOffset: 5, commitBarHeight: 2}

	cases := []struct {
		name       string
		x, y       int
		wantRegion hitRegion
		wantLine   int
	}{
		{"in header → none", 5, 0, hitNone, 0},
		{"below body → none", 5, 30, hitNone, 0},
		{"left of list", 3, 1, hitList, 0},         // body line 0
		{"list lower row", 3, 6, hitList, 5},       // body line 5
		{"last file row", 3, 18, hitList, 17},      // body line 17 (just above commit bar)
		{"commit bar top", 3, 19, hitCommit, 0},    // body line 18 (commit zone, height 2)
		{"commit bar bottom", 3, 20, hitCommit, 0}, // body line 19 (last body row)
		{"on divider", 30, 4, hitDivider, 0},
		{"in diff top → +offset", 40, 1, hitDiff, 5}, // body line 0 + yOffset 5
		{"in diff lower", 40, 6, hitDiff, 10},        // body line 5 + 5
		{"last diff content col", 78, 2, hitDiff, 6}, // body line 1 + 5
		{"scrollbar col → none", 79, 2, hitNone, 0},  // x == width-1 is the scrollbar
		{"past width → none", 80, 2, hitNone, 0},
		{"negative → none", -1, 2, hitNone, 0},
	}
	for _, c := range cases {
		got := hitTest(l, c.x, c.y)
		if got.region != c.wantRegion {
			t.Errorf("%s: region=%v want %v", c.name, got.region, c.wantRegion)
			continue
		}
		if (got.region == hitList || got.region == hitDiff) && got.line != c.wantLine {
			t.Errorf("%s: line=%d want %d", c.name, got.line, c.wantLine)
		}
	}
}
