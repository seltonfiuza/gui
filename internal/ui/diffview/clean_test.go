package diffview

import (
	"strings"
	"testing"

	"github.com/selton/gui/internal/git"
)

const fileDiff = `diff --git a/b.go b/b.go
index 1111111..2222222 100644
--- a/b.go
+++ b/b.go
@@ -1,3 +1,4 @@ func main() {
 package main
+import "fmt"

 func main() {
@@ -10,3 +11,3 @@ func main() {
-	old()
+	new()
 	done()
`

const untrackedDiff = `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..89b24ec
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`

// TestCleanDiffStripsPlumbing asserts the cleaned view drops git plumbing but
// keeps content lines and their +/- markers.
func TestCleanDiffStripsPlumbing(t *testing.T) {
	cd := cleanDiff(fileDiff, false)
	var joined []string
	for _, l := range cd.lines {
		joined = append(joined, l.text)
	}
	out := strings.Join(joined, "\n")

	for _, bad := range []string{"diff --git", "index ", "--- a/", "+++ b/"} {
		if strings.Contains(out, bad) {
			t.Errorf("cleaned diff should not contain %q:\n%s", bad, out)
		}
	}
	// Content + markers preserved.
	for _, want := range []string{`+import "fmt"`, `-	old()`, `+	new()`, ` package main`} {
		if !strings.Contains(out, want) {
			t.Errorf("cleaned diff missing content %q:\n%s", want, out)
		}
	}
	// Hunk headers become compact labels (no raw "@@ -a,b +c,d @@").
	if strings.Contains(out, "@@ -1,3 +1,4 @@") {
		t.Errorf("raw @@ header should be compacted:\n%s", out)
	}
	if !strings.Contains(out, "lines 1") {
		t.Errorf("expected a compact 'lines …' hunk label:\n%s", out)
	}
}

// TestCleanDiffUntrackedStripsDevNull asserts --no-index synthetic-diff noise is
// removed too.
func TestCleanDiffUntrackedStripsDevNull(t *testing.T) {
	cd := cleanDiff(untrackedDiff, false)
	var joined []string
	for _, l := range cd.lines {
		joined = append(joined, l.text)
	}
	out := strings.Join(joined, "\n")
	for _, bad := range []string{"/dev/null", "new file mode", "diff --git", "index "} {
		if strings.Contains(out, bad) {
			t.Errorf("untracked cleaned diff should not contain %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"+hello", "+world"} {
		if !strings.Contains(out, want) {
			t.Errorf("untracked cleaned diff missing %q:\n%s", want, out)
		}
	}
}

// TestCleanDiffRawModeIsIdentity asserts raw mode keeps everything and maps 1:1.
func TestCleanDiffRawModeIsIdentity(t *testing.T) {
	rawLines := strings.Split(fileDiff, "\n")
	cd := cleanDiff(fileDiff, true)
	if len(cd.lines) != len(rawLines) {
		t.Fatalf("raw mode should keep all %d lines, got %d", len(rawLines), len(cd.lines))
	}
	for i := range cd.lines {
		if cd.renderedToRaw[i] != i || cd.rawToRendered[i] != i {
			t.Fatalf("raw mode maps should be identity at %d: r2raw=%d raw2r=%d",
				i, cd.renderedToRaw[i], cd.rawToRendered[i])
		}
	}
}

// TestCleanupPreservesCursorToHunkRoundTrip is the critical guarantee: a cursor
// sitting on a cleaned rendered row maps back to the correct raw hunk index via
// git.HunkAtLine, so `u` discards the right hunk and `}`/`{` still land on hunks.
func TestCleanupPreservesCursorToHunkRoundTrip(t *testing.T) {
	hunks, err := git.ParseHunks(fileDiff)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	cd := cleanDiff(fileDiff, false)

	// For every rendered row, the raw line it maps to must resolve to a hunk via
	// HunkAtLine identical to running HunkAtLine on that raw line directly.
	for row := 0; row < len(cd.lines); row++ {
		raw := cd.renderedToRaw[row]
		// Round-trip: raw -> rendered -> raw must be stable for visible rows, so a
		// HunkAtLine lookup on the mapped raw line is well-defined.
		if cd.rawToRendered[raw] != row {
			t.Fatalf("round-trip broke at row %d (raw %d): rawToRendered=%d",
				row, raw, cd.rawToRendered[raw])
		}
	}

	// Concretely: a cursor on the rendered row showing "+new()" (inside hunk 2)
	// must map to hunk index 1.
	var addRow = -1
	for row, l := range cd.lines {
		if strings.Contains(l.text, "new()") {
			addRow = row
			break
		}
	}
	if addRow < 0 {
		t.Fatal("could not find the +new() row")
	}
	raw := cd.renderedToRaw[addRow]
	if idx := git.HunkAtLine(hunks, raw); idx != 1 {
		t.Fatalf("cursor on +new() should map to hunk 1, got %d (raw line %d)", idx, raw)
	}
}

// TestModelHunkDiscardMappingUnderCleanup drives the model: focusing the diff,
// jumping to the second hunk, then verifying LineCursor() resolves to hunk 1 —
// exactly what discardHunk relies on.
func TestModelHunkDiscardMappingUnderCleanup(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 30)
	m.SetDiff("b.go", fileDiff)
	m.FocusDiff()
	m.HunkNext() // jump to second hunk header
	hunks := m.Hunks()
	if idx := git.HunkAtLine(hunks, m.LineCursor()); idx != 1 {
		t.Fatalf("after HunkNext, cursor should be in hunk 1, got %d", idx)
	}
	// Step the cursor down by rendered rows into the hunk body; still hunk 1.
	m.CursorDown()
	if idx := git.HunkAtLine(hunks, m.LineCursor()); idx != 1 {
		t.Fatalf("after stepping into hunk 1 body, got hunk %d", idx)
	}
}
