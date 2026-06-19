package diffview

import (
	"fmt"
	"strings"

	"github.com/selton/gui/internal/ui/styles"
)

// lineKind classifies a cleaned diff line for styling.
type lineKind int

const (
	kindContext lineKind = iota
	kindAdd
	kindRemove
	kindHunk
	kindMeta // retained plumbing (only kept in raw mode)
)

// cleanLine is one rendered diff row plus the raw-diff-line index it came from.
// rawIdx is the 0-based index into strings.Split(rawDiff, "\n") — the SAME index
// space git.ParseHunks / HunkAtLine operate on, so the cursor↔hunk mapping is
// preserved even though plumbing rows are dropped from the rendered output.
type cleanLine struct {
	text   string // the displayed text (marker column preserved for +/-)
	kind   lineKind
	rawIdx int
}

// cleanedDiff is the result of cleanDiff: the rows to render plus the two index
// maps that translate between raw-diff-line space and rendered-row space.
type cleanedDiff struct {
	lines         []cleanLine
	rawToRendered []int // len == #raw lines; raw idx -> rendered row (nearest visible if suppressed)
	renderedToRaw []int // len == #rendered rows; rendered row -> raw idx
}

// isPlumbing reports whether a raw diff line is git plumbing that the cleaned
// view suppresses by default. Covers real and --no-index synthetic diffs.
func isPlumbing(ln string) bool {
	switch {
	case strings.HasPrefix(ln, "diff --git "),
		strings.HasPrefix(ln, "diff --cc "),
		strings.HasPrefix(ln, "index "),
		strings.HasPrefix(ln, "new file mode"),
		strings.HasPrefix(ln, "deleted file mode"),
		strings.HasPrefix(ln, "old mode"),
		strings.HasPrefix(ln, "new mode"),
		strings.HasPrefix(ln, "similarity index"),
		strings.HasPrefix(ln, "dissimilarity index"),
		strings.HasPrefix(ln, "rename from"),
		strings.HasPrefix(ln, "rename to"),
		strings.HasPrefix(ln, "copy from"),
		strings.HasPrefix(ln, "copy to"),
		strings.HasPrefix(ln, "--- "),
		strings.HasPrefix(ln, "+++ "):
		return true
	}
	return false
}

// compactHunkHeader turns a raw "@@ -a,b +c,d @@ ctx" line into a clean label.
// It keeps any trailing context (the function/section after the second @@) and
// summarizes the new-file line range as "lines X–Y" when ranges are present.
func compactHunkHeader(ln string) string {
	// Split off trailing context after the closing "@@".
	ctx := ""
	if i := strings.Index(ln, "@@"); i >= 0 {
		if j := strings.Index(ln[i+2:], "@@"); j >= 0 {
			rest := ln[i+2+j+2:]
			ctx = strings.TrimSpace(rest)
		}
	}
	start, count := parseNewRange(ln)
	var label string
	if count <= 0 {
		label = fmt.Sprintf("@@ line %d", start)
	} else if count == 1 {
		label = fmt.Sprintf("@@ line %d", start)
	} else {
		label = fmt.Sprintf("@@ lines %d–%d", start, start+count-1)
	}
	if ctx != "" {
		label += "  " + ctx
	}
	return label
}

// parseNewRange extracts the new-file start and count from "@@ -a,b +c,d @@".
func parseNewRange(ln string) (start, count int) {
	for _, f := range strings.Fields(ln) {
		if len(f) >= 2 && f[0] == '+' {
			s, c := 0, 1
			body := f[1:]
			if comma := strings.IndexByte(body, ','); comma >= 0 {
				s = atoiPrefix(body[:comma])
				c = atoiPrefix(body[comma+1:])
			} else {
				s = atoiPrefix(body)
			}
			return s, c
		}
	}
	return 0, 0
}

func atoiPrefix(s string) int {
	n, end := 0, 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		n = n*10 + int(s[end]-'0')
		end++
	}
	if end == 0 {
		return 0
	}
	return n
}

// cleanDiff is the pure render transform: a raw unified-diff string in, cleaned
// rows + index maps out. With raw=true it keeps every line (plumbing included)
// so the maps are identity — used by the raw-diff toggle. Pure & unit-tested.
func cleanDiff(rawDiff string, raw bool) cleanedDiff {
	rawLines := strings.Split(rawDiff, "\n")
	cd := cleanedDiff{
		rawToRendered: make([]int, len(rawLines)),
	}
	for i := range cd.rawToRendered {
		cd.rawToRendered[i] = -1
	}

	for i, ln := range rawLines {
		if !raw && isPlumbing(ln) {
			continue // suppressed: rawToRendered stays -1 for now
		}
		var cl cleanLine
		cl.rawIdx = i
		switch {
		case strings.HasPrefix(ln, "@@"):
			if raw {
				cl.text = ln
			} else {
				cl.text = compactHunkHeader(ln)
			}
			cl.kind = kindHunk
		case isPlumbing(ln): // only reached when raw==true
			cl.text = ln
			cl.kind = kindMeta
		case strings.HasPrefix(ln, "+"):
			cl.text = ln
			cl.kind = kindAdd
		case strings.HasPrefix(ln, "-"):
			cl.text = ln
			cl.kind = kindRemove
		default:
			cl.text = ln
			cl.kind = kindContext
		}
		rendered := len(cd.lines)
		cd.lines = append(cd.lines, cl)
		cd.rawToRendered[i] = rendered
		cd.renderedToRaw = append(cd.renderedToRaw, i)
	}

	// Backfill suppressed raw lines (still -1) so a cursor that lands on a
	// suppressed line — or a hunk-jump target — maps to the nearest *following*
	// rendered row, falling back to the nearest preceding one. This keeps }/{
	// landing and the line-cursor highlight sane. We record which raw indices
	// are real rendered lines first so the fallbacks don't chase each other.
	isReal := make([]bool, len(rawLines))
	for _, raw := range cd.renderedToRaw {
		isReal[raw] = true
	}
	// Pass 1 (reverse): nearest following rendered row.
	lastVisible := -1
	for i := len(rawLines) - 1; i >= 0; i-- {
		if isReal[i] {
			lastVisible = cd.rawToRendered[i]
		} else {
			cd.rawToRendered[i] = lastVisible
		}
	}
	// Pass 2 (forward): for any still -1 (nothing follows), use nearest preceding.
	firstVisible := -1
	for i := 0; i < len(rawLines); i++ {
		if isReal[i] {
			firstVisible = cd.rawToRendered[i]
		} else if cd.rawToRendered[i] < 0 {
			cd.rawToRendered[i] = firstVisible
		}
	}
	return cd
}

// styleClean renders one cleaned line with theme colors (no cursor highlight).
func styleClean(cl cleanLine) string {
	switch cl.kind {
	case kindHunk:
		return styles.Hunk.Render(cl.text)
	case kindAdd:
		return styles.Added.Render(cl.text)
	case kindRemove:
		return styles.Removed.Render(cl.text)
	case kindMeta:
		return styles.DiffMeta.Render(cl.text)
	default:
		return styles.Context.Render(cl.text)
	}
}
