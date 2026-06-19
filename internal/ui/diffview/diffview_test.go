package diffview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/seltonfiuza/gui/internal/git"
)

const twoHunkDiff = `diff --git a/b.go b/b.go
index 1111111..2222222 100644
--- a/b.go
+++ b/b.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"

 func main() {
@@ -10,3 +11,3 @@ func main() {
-	old()
+	new()
 	done()
`

func TestClampListWidth(t *testing.T) {
	cases := []struct {
		total, want, expect int
	}{
		// Comfortable terminal: want honored.
		{total: 100, want: 30, expect: 30},
		// Want below min list width: clamped up to MinListWidth.
		{total: 100, want: 5, expect: MinListWidth},
		// Want too large: clamped so the diff keeps MinDiffWidth.
		{total: 100, want: 95, expect: 100 - dividerWidth - MinDiffWidth},
		// Narrow terminal that can't honor both minimums: list gets what's left.
		{total: 10, want: 8, expect: 10 - dividerWidth},
		// Degenerate.
		{total: 0, want: 5, expect: 0},
	}
	for _, c := range cases {
		got := ClampListWidth(c.total, c.want)
		if got != c.expect {
			t.Errorf("ClampListWidth(%d,%d)=%d want %d", c.total, c.want, got, c.expect)
		}
		// Invariant: the list never consumes more than the total minus divider.
		if c.total > dividerWidth && got > c.total-dividerWidth {
			t.Errorf("ClampListWidth(%d,%d)=%d overflows total", c.total, c.want, got)
		}
	}
}

func TestSetDiffParsesHunksAndStartsCursorOnFirstHunk(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 30)
	m.SetDiff("b.go", twoHunkDiff)
	hs := m.Hunks()
	if len(hs) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hs))
	}
	if m.LineCursor() != hs[0].StartLine {
		t.Fatalf("cursor should start on first hunk header %d, got %d", hs[0].StartLine, m.LineCursor())
	}
}

func TestHunkNextPrevRequiresDiffFocus(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 30)
	m.SetDiff("b.go", twoHunkDiff)
	hs := m.Hunks()

	// Not focused on diff: hunk jumps are no-ops.
	m.FocusList()
	before := m.LineCursor()
	m.HunkNext()
	if m.LineCursor() != before {
		t.Fatalf("HunkNext should be a no-op when list-focused")
	}

	// Focused: jumps land on hunk headers.
	m.FocusDiff()
	m.HunkNext()
	if m.LineCursor() != hs[1].StartLine {
		t.Fatalf("HunkNext should land on hunk 2 header %d, got %d", hs[1].StartLine, m.LineCursor())
	}
	m.HunkNext() // no further hunk → stays put
	if m.LineCursor() != hs[1].StartLine {
		t.Fatalf("HunkNext past last hunk should stay put")
	}
	m.HunkPrev()
	if m.LineCursor() != hs[0].StartLine {
		t.Fatalf("HunkPrev should land on hunk 1 header %d, got %d", hs[0].StartLine, m.LineCursor())
	}
}

func TestLineCursorClampsAtBounds(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 30)
	m.SetDiff("b.go", twoHunkDiff)
	m.FocusDiff()
	// Walk up to the top. The cleaned view suppresses the leading plumbing
	// (diff/index/---/+++), so the top stops on the first *rendered* row's raw
	// line — the first @@ header — not raw line 0.
	for i := 0; i < 100; i++ {
		m.CursorUp()
	}
	topRaw := m.rawForRendered(0)
	if m.LineCursor() != topRaw {
		t.Fatalf("cursor should clamp at first rendered row (raw %d), got %d", topRaw, m.LineCursor())
	}
	// Walk down past the end: stops on the last rendered row's raw line.
	for i := 0; i < 1000; i++ {
		m.CursorDown()
	}
	bottomRaw := m.rawForRendered(m.renderedRows() - 1)
	if m.LineCursor() != bottomRaw {
		t.Fatalf("cursor should clamp at last rendered row (raw %d), got %d", bottomRaw, m.LineCursor())
	}
}

func TestReconcileSelection(t *testing.T) {
	cases := []struct {
		name     string
		oldPath  string
		oldIdx   int
		newPaths []string
		want     int
	}{
		{"path kept follows file", "b.go", 1, []string{"a.go", "b.go", "c.go"}, 1},
		{"path moved index follows path", "b.go", 1, []string{"b.go", "c.go"}, 0},
		{"path removed picks neighbor at old slot", "b.go", 1, []string{"a.go", "c.go"}, 1},
		{"path removed at end clamps", "c.go", 2, []string{"a.go", "b.go"}, 1},
		{"empty list is safe", "b.go", 1, nil, 0},
		{"no prior path keeps slot", "", 2, []string{"a.go", "b.go", "c.go"}, 2},
	}
	for _, c := range cases {
		if got := ReconcileSelection(c.oldPath, c.oldIdx, c.newPaths); got != c.want {
			t.Errorf("%s: ReconcileSelection(%q,%d,%v)=%d want %d", c.name, c.oldPath, c.oldIdx, c.newPaths, got, c.want)
		}
	}
}

func TestSetStatusPreservesSelectionByPath(t *testing.T) {
	m := New()
	st := &git.Status{
		Unstaged: []git.ChangedFile{
			{Path: "a.go", Worktree: git.Modified},
			{Path: "b.go", Worktree: git.Modified},
			{Path: "c.go", Worktree: git.Modified},
		},
	}
	m.SetStatus(st)
	m.SelectRow(1) // select b.go (node index 1 in the flat root list)
	if m.SelectedPath() != "b.go" {
		t.Fatalf("setup: expected b.go selected, got %q", m.SelectedPath())
	}
	// a.go removed: b.go must stay selected (now at index 0).
	st2 := &git.Status{
		Unstaged: []git.ChangedFile{
			{Path: "b.go", Worktree: git.Modified},
			{Path: "c.go", Worktree: git.Modified},
		},
	}
	m.SetStatus(st2)
	if m.SelectedPath() != "b.go" {
		t.Fatalf("selection should follow b.go after a.go removed, got %q", m.SelectedPath())
	}
	// b.go removed: selection should land on a neighbor (the row at old slot 0).
	st3 := &git.Status{
		Unstaged: []git.ChangedFile{
			{Path: "c.go", Worktree: git.Modified},
		},
	}
	m.SetStatus(st3)
	if m.SelectedPath() != "c.go" {
		t.Fatalf("selection should fall to neighbor c.go, got %q", m.SelectedPath())
	}
}

func TestSetDiffPreservingKeepsCursorWhenUnchanged(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 30)
	m.SetDiff("b.go", twoHunkDiff)
	m.FocusDiff()
	m.lineCursorDown()
	m.lineCursorDown()
	want := m.LineCursor()
	// Re-applying identical diff must not move the cursor.
	m.SetDiffPreserving("b.go", twoHunkDiff)
	if m.LineCursor() != want {
		t.Fatalf("unchanged diff moved cursor: got %d want %d", m.LineCursor(), want)
	}
	// A changed diff resets to the first hunk.
	changed := twoHunkDiff + "\n@@ -20,1 +21,1 @@\n-x\n+y\n"
	m.SetDiffPreserving("b.go", changed)
	if m.LineCursor() != m.Hunks()[0].StartLine {
		t.Fatalf("changed diff should reset cursor to first hunk")
	}
}

// TestListClickMapsToRenderedFileWhenScrolled is the regression guard for the
// "click a lot upper to hit the right file" bug: with a long, scrolled list of
// long-path files, every clickable screen line must map (via ListLineToRow) to
// the file actually rendered on that line, and no line may wrap.
func TestListClickMapsToRenderedFileWhenScrolled(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(50, 8, 24) // small height forces scroll; listWidth=24
	var files []git.ChangedFile
	for i := 0; i < 30; i++ {
		files = append(files, git.ChangedFile{
			Path:     fmt.Sprintf("dir/subdir/long-file-name-%02d.go", i),
			Worktree: git.Modified,
		})
	}
	m.SetStatus(&git.Status{Unstaged: files})
	for i := 0; i < 29; i++ { // select the last file -> list must scroll
		m.CursorDown()
	}
	if m.listOffset == 0 {
		t.Fatalf("list should have scrolled for 30 files in height 8")
	}

	cells := m.listCells()
	lines := strings.Split(m.renderList(), "\n")
	if len(lines) > 8 {
		t.Fatalf("rendered %d screen lines, exceeds height 8", len(lines))
	}
	for screen, ln := range lines {
		if w := lipgloss.Width(ln); w > 24 {
			t.Errorf("screen line %d width %d overflows listWidth 24: %q", screen, w, ln)
		}
		ci := m.listOffset + screen
		row, ok := m.ListLineToRow(screen)
		wantRow := cells[ci].row
		if wantRow < 0 {
			if ok {
				t.Errorf("screen %d is a non-file line but hit-test returned row %d", screen, row)
			}
			continue
		}
		if !ok || row != wantRow {
			t.Errorf("screen %d: hit-test row=%d ok=%v, want row %d", screen, row, ok, wantRow)
		}
	}
	// The selected (last) file's first line must be within the visible window.
	first, _, _ := m.cursorCellRange()
	if first < m.listOffset || first >= m.listOffset+8 {
		t.Errorf("selected row first line %d not visible (offset=%d height=8)", first, m.listOffset)
	}
}

// TestCompactsSingleChildChains asserts a deep single-child directory chain
// collapses to one folder node (so a 6-deep path is two lines, not seven).
func TestCompactsSingleChildChains(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(60, 40, 36)
	m.SetStatus(&git.Status{Unstaged: []git.ChangedFile{
		{Path: "a/b/c/d/e/f.go", Worktree: git.Modified},
	}})
	nodes := m.nodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 1 folder + 1 file node, got %d", len(nodes))
	}
	if nodes[0].kind != folderNode || nodes[0].label != "a/b/c/d/e" {
		t.Errorf("compacted folder = %q (kind %d), want a/b/c/d/e", nodes[0].label, nodes[0].kind)
	}
	if nodes[1].kind != fileNode || nodes[1].label != "f.go" {
		t.Errorf("file leaf = %q (kind %d), want f.go", nodes[1].label, nodes[1].kind)
	}
	if nodes[1].depth != nodes[0].depth+1 {
		t.Errorf("file depth %d should be folder depth %d + 1", nodes[1].depth, nodes[0].depth)
	}
}

// TestLongBasenameWraps asserts a long file name wraps onto multiple screen
// lines and that EVERY wrapped line maps back to the same node — so clicking any
// of them selects the right file.
func TestLongBasenameWraps(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(50, 20, 20) // listWidth=20
	long := "this_is_a_really_long_basename_that_will_wrap.go"
	m.SetStatus(&git.Status{Unstaged: []git.ChangedFile{{Path: long, Worktree: git.Modified}}})

	nodes := m.nodes()
	fileIdx := -1
	for i, n := range nodes {
		if n.kind == fileNode {
			fileIdx = i
		}
	}
	if fileIdx < 0 {
		t.Fatalf("no file node found")
	}
	cells := m.listCells()
	var lines []int
	for ci, c := range cells {
		if c.row == fileIdx {
			lines = append(lines, ci)
		}
	}
	if len(lines) < 2 {
		t.Fatalf("long basename should wrap to multiple lines, got %d", len(lines))
	}
	for _, ci := range lines {
		if row, ok := m.ListLineToRow(ci - m.listOffset); !ok || row != fileIdx {
			t.Errorf("wrapped line %d: got row=%d ok=%v want %d", ci, row, ok, fileIdx)
		}
	}
}

// TestFolderTreeStructure asserts nested files render as a folder tree: the right
// number of folder vs file nodes, and every cell maps consistently to its node.
func TestFolderTreeStructure(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(60, 40, 36)
	m.SetStatus(&git.Status{Unstaged: []git.ChangedFile{
		{Path: "internal/ui/app.go", Worktree: git.Modified},
		{Path: "internal/ui/app_test.go", Worktree: git.Modified},
		{Path: "internal/git/git.go", Worktree: git.Modified},
		{Path: "README.md", Worktree: git.Modified},
	}})
	var folders, files int
	for _, n := range m.nodes() {
		if n.kind == folderNode {
			folders++
		} else {
			files++
		}
	}
	if files != 4 {
		t.Errorf("expected 4 file leaves, got %d", files)
	}
	// internal/ (branches to git + ui), git/, ui/ -> 3 folder nodes.
	if folders != 3 {
		t.Errorf("expected 3 folder nodes, got %d", folders)
	}

	cells := m.listCells()
	for ci, c := range cells {
		row, ok := m.ListLineToRow(ci) // listOffset is 0 here
		if c.row < 0 {
			if ok {
				t.Errorf("cell %d (%q) is chrome but hit-test returned row %d", ci, c.text, row)
			}
			continue
		}
		if !ok || row != c.row {
			t.Errorf("cell %d: hit-test row=%d ok=%v want %d", ci, row, ok, c.row)
		}
	}
}

// TestCollapseHidesChildrenAndToggles asserts collapsing a folder hides its
// files and re-expanding restores them.
func TestCollapseHidesChildrenAndToggles(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(60, 40, 36)
	m.SetStatus(&git.Status{Unstaged: []git.ChangedFile{
		{Path: "internal/ui/app.go", Worktree: git.Modified},
		{Path: "internal/ui/app_test.go", Worktree: git.Modified},
	}})
	// Compacted to one folder "internal/ui" + 2 files.
	if got := len(m.nodes()); got != 3 {
		t.Fatalf("expected 3 nodes before collapse, got %d", got)
	}
	m.SelectRow(0) // the folder
	if !m.Activate() {
		t.Fatalf("Activate on a folder should report a toggle")
	}
	if got := len(m.nodes()); got != 1 {
		t.Errorf("collapsed folder should hide its 2 files (1 node), got %d", got)
	}
	if _, ok := m.Selected(); ok {
		t.Errorf("a folder is selected; Selected() should report no file")
	}
	m.Activate() // expand again
	if got := len(m.nodes()); got != 3 {
		t.Errorf("expanded folder should restore 3 nodes, got %d", got)
	}
}

// TestFlatModeShowsFullPaths asserts flat mode emits one file node per file with
// the full path and no folder nodes.
func TestFlatModeShowsFullPaths(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New()
	m.SetSize(60, 40, 36)
	m.SetStatus(&git.Status{Unstaged: []git.ChangedFile{
		{Path: "internal/ui/app.go", Worktree: git.Modified},
		{Path: "internal/git/git.go", Worktree: git.Modified},
	}})
	m.ToggleTreeMode() // -> flat
	nodes := m.nodes()
	if len(nodes) != 2 {
		t.Fatalf("flat mode: expected 2 file nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.kind != fileNode {
			t.Errorf("flat mode should have no folder nodes, got kind %d", n.kind)
		}
		if !strings.Contains(n.label, "/") {
			t.Errorf("flat node label should be a full path, got %q", n.label)
		}
	}
}
