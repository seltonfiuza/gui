package diffview

import (
	"testing"

	"github.com/selton/gui/internal/git"
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
	// Walk up to the top.
	for i := 0; i < 100; i++ {
		m.CursorUp()
	}
	if m.LineCursor() != 0 {
		t.Fatalf("cursor should clamp at 0, got %d", m.LineCursor())
	}
	// Walk down past the end.
	for i := 0; i < 1000; i++ {
		m.CursorDown()
	}
	if m.LineCursor() != len(m.diffLines)-1 {
		t.Fatalf("cursor should clamp at last line %d, got %d", len(m.diffLines)-1, m.LineCursor())
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
	m.cursor = 1 // select b.go
	if m.SelectedPath() != "b.go" {
		t.Fatalf("setup: expected b.go selected")
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
