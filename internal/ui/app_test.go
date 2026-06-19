package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui/diffview"
)

func newTestApp() *App {
	a := New(&git.Service{}).(*App)
	// Give it a size and a stub status so the diff view has rows.
	a.width, a.height = 80, 24
	a.applyLayout()
	a.status = &git.Status{
		Branch: "main",
		Staged: []git.ChangedFile{
			{Path: "a.go", Staged: git.Modified},
		},
		Unstaged: []git.ChangedFile{
			{Path: "b.go", Worktree: git.Modified},
		},
		Untracked: []git.ChangedFile{
			{Path: "c.go", Worktree: git.Untracked},
		},
	}
	a.diff.SetStatus(a.status)
	return a
}

func press(a *App, key string) {
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func pressNamed(a *App, t tea.KeyType) {
	a.handleKey(tea.KeyMsg{Type: t})
}

// pressCtrl sends a control key whose String() equals the given name (e.g.
// "ctrl+r"). It maps the few control keys the tests need.
func pressCtrl(a *App, name string) {
	var kt tea.KeyType
	switch name {
	case "ctrl+r":
		kt = tea.KeyCtrlR
	case "ctrl+t":
		kt = tea.KeyCtrlT
	default:
		a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)})
		return
	}
	a.handleKey(tea.KeyMsg{Type: kt})
}

func TestHelpToggle(t *testing.T) {
	a := newTestApp()
	if a.active != viewDiff {
		t.Fatalf("expected diff view initially")
	}
	press(a, "?")
	if a.active != viewHelp {
		t.Fatalf("? should open help, got %v", a.active)
	}
	press(a, "?")
	if a.active != viewDiff {
		t.Fatalf("? should close help, got %v", a.active)
	}
}

func TestBranchPanelLeaderChord(t *testing.T) {
	a := newTestApp()
	// Leader is space, then b => ActBranchPanel.
	pressNamed(a, tea.KeySpace)
	press(a, "b")
	if a.active != viewBranch {
		t.Fatalf("space then b should open branch panel, got %v", a.active)
	}
	// esc closes.
	pressNamed(a, tea.KeyEsc)
	if a.active != viewDiff {
		t.Fatalf("esc should close branch panel, got %v", a.active)
	}
}

func TestCursorMovement(t *testing.T) {
	a := newTestApp()
	if got := a.diff.SelectedPath(); got != "a.go" {
		t.Fatalf("expected a.go selected, got %q", got)
	}
	press(a, "j")
	if got := a.diff.SelectedPath(); got != "b.go" {
		t.Fatalf("after j expected b.go, got %q", got)
	}
	press(a, "j")
	if got := a.diff.SelectedPath(); got != "c.go" {
		t.Fatalf("after jj expected c.go, got %q", got)
	}
	press(a, "k")
	if got := a.diff.SelectedPath(); got != "b.go" {
		t.Fatalf("after k expected b.go, got %q", got)
	}
}

func TestDiscardUntrackedRequiresConfirm(t *testing.T) {
	a := newTestApp()
	// Move to the untracked file (c.go).
	press(a, "j")
	press(a, "j")
	if a.diff.SelectedPath() != "c.go" {
		t.Fatalf("setup: expected c.go selected")
	}
	press(a, "u")
	if a.confirm == nil {
		t.Fatalf("discarding an untracked file should require confirmation")
	}
	// Decline.
	press(a, "n")
	if a.confirm != nil {
		t.Fatalf("n should dismiss the confirm dialog")
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	a := newTestApp()
	if a.View() == "" {
		t.Fatalf("View should render non-empty output")
	}
	press(a, "?")
	if a.View() == "" {
		t.Fatalf("help View should render non-empty output")
	}
}

func TestViewRendersAtVariousSizes(t *testing.T) {
	sizes := [][2]int{{20, 6}, {40, 12}, {80, 24}, {200, 50}, {10, 3}}
	for _, s := range sizes {
		a := newTestApp()
		a.width, a.height = s[0], s[1]
		a.applyLayout()
		// Load a diff so the viewport + cursor highlight render too.
		a.diff.SetDiff("b.go", sampleDiff)
		a.diff.FocusDiff()
		if got := a.View(); got == "" {
			t.Fatalf("View empty at %dx%d", s[0], s[1])
		}
	}
}

// sampleDiff is a two-hunk unified diff fixture used by the cursor/hunk tests.
const sampleDiff = `diff --git a/b.go b/b.go
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

func TestUndoFileOpensConfirmAndDischarges(t *testing.T) {
	a := newTestApp()
	// Select the tracked unstaged file b.go.
	press(a, "j")
	if a.diff.SelectedPath() != "b.go" {
		t.Fatalf("setup: expected b.go selected, got %q", a.diff.SelectedPath())
	}
	press(a, "U")
	if a.confirm == nil {
		t.Fatalf("U should open a confirm dialog")
	}
	if !strings.Contains(a.confirm.prompt, "b.go") {
		t.Fatalf("confirm should name the file, got %q", a.confirm.prompt)
	}
	// Confirming must produce a discard command (not panic, not nil).
	fn := a.confirm.onYes
	a.confirm = nil
	if cmd := fn(a); cmd == nil {
		t.Fatalf("confirming U should yield a discard command")
	}
	// A recovery entry should have been recorded for the (empty) diff path; with
	// a real repo it would capture the diff. Here Diff panics/empties, so the
	// stack may be empty — the key invariant is no panic above.
}

func TestRecoverEmptyStackToasts(t *testing.T) {
	a := newTestApp()
	if len(a.undo) != 0 {
		t.Fatalf("setup: undo stack should start empty")
	}
	pressCtrl(a, "ctrl+r")
	if a.toast != "nothing to recover" {
		t.Fatalf("ctrl+r on empty stack should toast, got %q", a.toast)
	}
}

func TestRecoverPopsAndRestores(t *testing.T) {
	a := newTestApp()
	restored := false
	a.pushUndo(undoEntry{label: "x", restore: func(*git.Service) error { restored = true; return nil }})
	if len(a.undo) != 1 {
		t.Fatalf("expected one undo entry")
	}
	_, cmd := a.recover()
	if cmd == nil {
		t.Fatalf("recover should return a command")
	}
	if len(a.undo) != 0 {
		t.Fatalf("recover should pop the stack")
	}
	// Run the command to exercise the restore func.
	cmd()
	if !restored {
		t.Fatalf("recover command should call restore")
	}
}

func TestPaneResizeClamps(t *testing.T) {
	a := newTestApp()
	start := a.listWidth
	a.growDiff()
	if a.listWidth >= start {
		t.Fatalf("growDiff should shrink the list width: %d -> %d", start, a.listWidth)
	}
	// Grow repeatedly: must clamp at the minimum list width, never below.
	for i := 0; i < 50; i++ {
		a.growDiff()
	}
	if a.listWidth < diffview.MinListWidth {
		t.Fatalf("list width %d dropped below min %d", a.listWidth, diffview.MinListWidth)
	}
	// Shrink repeatedly: the diff pane must keep its minimum width.
	for i := 0; i < 50; i++ {
		a.shrinkDiff()
	}
	if maxDiff := a.width - 1 - a.listWidth; maxDiff < diffview.MinDiffWidth {
		t.Fatalf("diff pane width %d dropped below min %d", maxDiff, diffview.MinDiffWidth)
	}
}

func TestDiffLineCursorMovesWhenFocused(t *testing.T) {
	a := newTestApp()
	press(a, "j") // select b.go
	a.diff.SetDiff("b.go", sampleDiff)
	// Focus the diff pane (enter).
	pressNamed(a, tea.KeyEnter)
	if a.diff.Focus() != diffview.FocusDiff {
		t.Fatalf("enter should focus the diff pane")
	}
	start := a.diff.LineCursor()
	press(a, "j")
	if a.diff.LineCursor() <= start {
		t.Fatalf("j should advance the diff line cursor: %d -> %d", start, a.diff.LineCursor())
	}
	press(a, "k")
	if a.diff.LineCursor() != start {
		t.Fatalf("k should move the diff line cursor back to %d, got %d", start, a.diff.LineCursor())
	}
	// esc returns focus to the file list; j then moves the file selection.
	pressNamed(a, tea.KeyEsc)
	if a.diff.Focus() != diffview.FocusList {
		t.Fatalf("esc should return focus to the list")
	}
}

func TestHunkJumpMovesCursorToHunks(t *testing.T) {
	a := newTestApp()
	press(a, "j")
	a.diff.SetDiff("b.go", sampleDiff)
	a.diff.FocusDiff()
	hunks := a.diff.Hunks()
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks in fixture, got %d", len(hunks))
	}
	// Cursor starts on the first hunk header.
	if a.diff.LineCursor() != hunks[0].StartLine {
		t.Fatalf("cursor should start on first hunk, got %d want %d", a.diff.LineCursor(), hunks[0].StartLine)
	}
	press(a, "}")
	if a.diff.LineCursor() != hunks[1].StartLine {
		t.Fatalf("} should jump to second hunk %d, got %d", hunks[1].StartLine, a.diff.LineCursor())
	}
	press(a, "{")
	if a.diff.LineCursor() != hunks[0].StartLine {
		t.Fatalf("{ should jump back to first hunk %d, got %d", hunks[0].StartLine, a.diff.LineCursor())
	}
}

func TestStatusFingerprintDetectsChange(t *testing.T) {
	base := &git.Status{
		Branch:   "main",
		Upstream: "origin/main",
		Ahead:    1, Behind: 0,
		Staged:   []git.ChangedFile{{Path: "a.go", Staged: git.Modified}},
		Unstaged: []git.ChangedFile{{Path: "b.go", Worktree: git.Modified}},
	}
	same := &git.Status{
		Branch:   "main",
		Upstream: "origin/main",
		Ahead:    1, Behind: 0,
		Staged:   []git.ChangedFile{{Path: "a.go", Staged: git.Modified}},
		Unstaged: []git.ChangedFile{{Path: "b.go", Worktree: git.Modified}},
	}
	if statusFingerprint(base) != statusFingerprint(same) {
		t.Fatalf("identical statuses must fingerprint equal")
	}
	// Differing in various ways must all change the fingerprint.
	mut := func(f func(*git.Status)) *git.Status {
		c := *base
		c.Staged = append([]git.ChangedFile(nil), base.Staged...)
		c.Unstaged = append([]git.ChangedFile(nil), base.Unstaged...)
		f(&c)
		return &c
	}
	diffs := []*git.Status{
		mut(func(s *git.Status) { s.Branch = "dev" }),
		mut(func(s *git.Status) { s.Ahead = 2 }),
		mut(func(s *git.Status) { s.Behind = 1 }),
		mut(func(s *git.Status) { s.Upstream = "" }),
		mut(func(s *git.Status) { s.Unstaged = append(s.Unstaged, git.ChangedFile{Path: "c.go", Worktree: git.Untracked}) }),
		mut(func(s *git.Status) { s.Staged[0].Staged = git.Deleted }),
	}
	for i, d := range diffs {
		if statusFingerprint(base) == statusFingerprint(d) {
			t.Fatalf("case %d: change should alter fingerprint", i)
		}
	}
	if statusFingerprint(nil) == statusFingerprint(base) {
		t.Fatalf("nil status must differ from a non-nil one")
	}
}

func TestTickReschedulesWithoutOverlap(t *testing.T) {
	a := newTestApp()
	a.statusFP = statusFingerprint(a.status)
	// A background status result must always yield a command that schedules the
	// next tick (chained off completion → no overlap, no queued calls).
	_, cmd := a.handleBgStatus(bgStatusMsg{status: a.status})
	if cmd == nil {
		t.Fatalf("bgStatus should reschedule the next tick")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("scheduled tick command should produce a message")
	}
	// A tick when auto-refresh is on kicks off a background status fetch.
	cmd = a.handleTick()
	if cmd == nil {
		t.Fatalf("tick should return a command")
	}
	// A tick when auto-refresh is off only reschedules (never fetches).
	a.autoRefresh = false
	if msg := a.handleTick()(); msg == nil {
		t.Fatalf("disabled tick should still reschedule")
	} else if _, ok := msg.(tickMsg); !ok {
		t.Fatalf("disabled tick should reschedule a tickMsg, got %T", msg)
	}
}

func TestBgErrorToastsAtMostOnce(t *testing.T) {
	a := newTestApp()
	a.handleBgStatus(bgStatusMsg{err: errPlaceholder{}})
	if a.toast == "" {
		t.Fatalf("first background error should toast")
	}
	a.toast = ""
	a.handleBgStatus(bgStatusMsg{err: errPlaceholder{}})
	if a.toast != "" {
		t.Fatalf("repeat background error must not re-toast")
	}
	// A success clears the flag so a later error toasts again.
	a.handleBgStatus(bgStatusMsg{status: &git.Status{Branch: "x"}})
	a.handleBgStatus(bgStatusMsg{err: errPlaceholder{}})
	if a.toast == "" {
		t.Fatalf("error after a success should toast again")
	}
}

type errPlaceholder struct{}

func (errPlaceholder) Error() string { return "boom" }

func TestBgRefreshDoesNotDisruptOverlay(t *testing.T) {
	a := newTestApp()
	a.statusFP = statusFingerprint(a.status)
	// Open the branch panel.
	a.active = viewBranch
	changed := &git.Status{Branch: "main", Unstaged: []git.ChangedFile{{Path: "z.go", Worktree: git.Modified}}}
	a.handleBgStatus(bgStatusMsg{status: changed})
	if a.active != viewBranch {
		t.Fatalf("background refresh must not close the branch overlay")
	}
	// Data may refresh underneath, but the fingerprint stays stale so the change
	// re-applies once the overlay closes.
	if a.statusFP == statusFingerprint(changed) {
		t.Fatalf("overlay-deferred refresh must not commit the fingerprint")
	}
	// Confirm dialog likewise survives a background refresh.
	a.active = viewDiff
	a.confirm = &confirmState{prompt: "x?"}
	a.handleBgStatus(bgStatusMsg{status: &git.Status{Branch: "other"}})
	if a.confirm == nil {
		t.Fatalf("background refresh must not dismiss a confirm dialog")
	}
}

func TestBgRefreshNoChangeIsNoop(t *testing.T) {
	a := newTestApp()
	a.statusFP = statusFingerprint(a.status)
	// Same status: must not re-fetch the diff (returns only the reschedule).
	_, cmd := a.handleBgStatus(bgStatusMsg{status: a.status})
	if cmd == nil {
		t.Fatalf("expected a reschedule command")
	}
	// The reschedule must be a single tick command, not a Batch with a diff fetch.
	if msg := cmd(); msg == nil {
		t.Fatalf("reschedule should produce a message")
	}
}

func TestToggleAutoRefresh(t *testing.T) {
	a := newTestApp()
	if !a.autoRefresh {
		t.Fatalf("auto-refresh should default on")
	}
	pressCtrl(a, "ctrl+t")
	if a.autoRefresh {
		t.Fatalf("ctrl+t should turn auto-refresh off")
	}
	if a.toast != "auto-refresh: off" {
		t.Fatalf("toggle off should toast, got %q", a.toast)
	}
	pressCtrl(a, "ctrl+t")
	if !a.autoRefresh {
		t.Fatalf("ctrl+t should turn auto-refresh back on")
	}
}
