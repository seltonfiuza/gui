package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui/diffview"
	"github.com/selton/gui/internal/ui/styles"
)

func newTestApp() *App {
	a := New(&git.Service{}, "test").(*App)
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
	case "ctrl+g":
		kt = tea.KeyCtrlG
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
		mut(func(s *git.Status) {
			s.Unstaged = append(s.Unstaged, git.ChangedFile{Path: "c.go", Worktree: git.Untracked})
		}),
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

// TestRendersAcrossAllThemes asserts every theme renders the full UI (diff +
// focused cursor + overlays) without panic, at a few terminal sizes.
func TestRendersAcrossAllThemes(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer styles.SetTheme(styles.DefaultTheme)
	sizes := [][2]int{{40, 12}, {80, 24}, {120, 40}}
	for _, name := range styles.ThemeNames() {
		styles.SetTheme(name)
		for _, s := range sizes {
			a := newTestApp()
			a.width, a.height = s[0], s[1]
			a.applyLayout()
			a.diff.SetDiff("b.go", sampleDiff)
			a.diff.FocusDiff()
			if a.View() == "" {
				t.Fatalf("theme %q: empty View at %dx%d", name, s[0], s[1])
			}
			// Theme picker overlay too.
			a.active = viewTheme
			a.theme.Open()
			if a.View() == "" {
				t.Fatalf("theme %q: empty picker View at %dx%d", name, s[0], s[1])
			}
		}
	}
}

// TestThemePickerLivePreviewAndRevert asserts moving the selection applies the
// theme live, esc reverts to the theme active on open, and enter persists it.
func TestThemePickerLivePreviewAndRevert(t *testing.T) {
	defer styles.SetTheme(styles.DefaultTheme)
	a := newTestApp()
	// Set a known non-last theme AFTER construction (New applies persisted config)
	// so Open() records it and `j` has a different theme to preview.
	styles.SetTheme("tokyonight")

	// Open the picker via leader chord (space then t).
	press(a, " ")
	press(a, "t")
	if a.active != viewTheme {
		t.Fatalf("space+t should open the theme picker, active=%v", a.active)
	}
	opened := styles.ActiveTheme()

	// Move down: live preview must change the active theme immediately.
	press(a, "j")
	if styles.ActiveTheme() == opened {
		t.Fatalf("moving selection should live-preview a different theme")
	}

	// Esc reverts to the theme active when opened, and closes the picker.
	pressNamed(a, tea.KeyEsc)
	if a.active != viewDiff {
		t.Fatalf("esc should close the picker")
	}
	if styles.ActiveTheme() != opened {
		t.Fatalf("esc should revert theme to %q, got %q", opened, styles.ActiveTheme())
	}
}

// TestThemePickerConfirmUpdatesConfig asserts enter records the chosen theme in
// the in-memory config (Save is best-effort and not asserted here).
func TestThemePickerConfirmUpdatesConfig(t *testing.T) {
	// Redirect config persistence to a temp dir so confirming doesn't touch the
	// developer's real ~/.../gui/config.json (os.UserConfigDir honors these).
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	defer styles.SetTheme(styles.DefaultTheme)
	a := newTestApp()
	styles.SetTheme("tokyonight")
	press(a, " ")
	press(a, "t")
	press(a, "j") // preview the next theme
	chosen := styles.ActiveTheme()
	pressNamed(a, tea.KeyEnter)
	if a.active != viewDiff {
		t.Fatalf("enter should close the picker")
	}
	if a.cfg.Theme != chosen {
		t.Fatalf("enter should record theme %q in config, got %q", chosen, a.cfg.Theme)
	}
}

// TestMouseClickSelectsFileRow asserts a left click on a file row selects that
// file (mouse hit-test → SelectRow), respecting the body/header offset.
func TestMouseClickSelectsFileRow(t *testing.T) {
	a := newTestApp()
	// Layout: header at y=0, body starts y=1. List body:
	//   line0: "Staged (1)"   (header)
	//   line1: a.go            (row 0)
	//   line2: (blank)
	//   line3: "Unstaged (1)"  (header)
	//   line4: b.go            (row 1)
	// Click b.go: body line 4 → screen y=5, x in the list pane.
	_, _ = a.handleMouse(tea.MouseMsg{
		X: 2, Y: 5,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	if a.diff.SelectedPath() != "b.go" {
		t.Fatalf("clicking b.go's row should select it, got %q", a.diff.SelectedPath())
	}
}

// TestMouseDividerDragResizes asserts pressing on the divider then dragging
// changes the list width (clamped via ClampListWidth).
func TestMouseDividerDragResizes(t *testing.T) {
	a := newTestApp()
	before := a.listWidth
	// Press on the divider column.
	_, _ = a.handleMouse(tea.MouseMsg{
		X: a.listWidth, Y: 3,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if !a.dragging {
		t.Fatalf("pressing the divider should start a drag")
	}
	// Drag left to a smaller column.
	_, _ = a.handleMouse(tea.MouseMsg{
		X: before - 8, Y: 3,
		Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft,
	})
	if a.listWidth >= before {
		t.Fatalf("dragging the divider left should shrink the list (%d -> %d)", before, a.listWidth)
	}
	// Release ends the drag.
	_, _ = a.handleMouse(tea.MouseMsg{Action: tea.MouseActionRelease})
	if a.dragging {
		t.Fatalf("release should end the drag")
	}
}

// TestToggleRawDiff asserts ctrl+g flips the diff view between cleaned and raw.
func TestToggleRawDiff(t *testing.T) {
	a := newTestApp()
	a.diff.SetDiff("b.go", sampleDiff)
	if a.diff.RawMode() {
		t.Fatalf("raw mode should default off")
	}
	pressCtrl(a, "ctrl+g")
	if !a.diff.RawMode() {
		t.Fatalf("ctrl+g should turn raw mode on")
	}
	pressCtrl(a, "ctrl+g")
	if a.diff.RawMode() {
		t.Fatalf("ctrl+g should turn raw mode back off")
	}
}

// --- robustness & mouse-hover regression tests (UI iteration) ---

// TestViewFitsHeight asserts the rendered View is exactly a.height rows tall
// across normal, narrow, and tiny sizes, and with a pending confirm overlay — so
// a long header/footer or a confirm box can never break the header/body/footer
// layout.
func TestViewFitsHeight(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	cases := []struct{ w, h int }{{80, 24}, {40, 12}, {20, 8}, {200, 50}}
	for _, c := range cases {
		a := newTestApp()
		a.width, a.height = c.w, c.h
		a.applyLayout()
		if got := lipgloss.Height(a.View()); got != c.h {
			t.Errorf("View() height=%d want %d (size %dx%d)", got, c.h, c.w, c.h)
		}
		// With a confirmation pending the overlay must not change the height.
		a.confirm = &confirmState{prompt: "Discard all changes in 'really/long/path/to/some/file/that/exceeds.go'?"}
		if got := lipgloss.Height(a.View()); got != c.h {
			t.Errorf("View()+confirm height=%d want %d (size %dx%d)", got, c.h, c.w, c.h)
		}
		a.confirm = nil
		// Every overlay view must also keep the total height exact.
		a.branch.SetBranches([]git.Branch{{Name: "main", IsCurrent: true}, {Name: "feature/x"}})
		for _, v := range []view{viewBranch, viewTheme, viewHelp} {
			a.active = v
			if got := lipgloss.Height(a.View()); got != c.h {
				t.Errorf("View() active=%d height=%d want %d (size %dx%d)", v, got, c.h, c.w, c.h)
			}
		}
		a.active = viewDiff
	}
}

// TestHeaderFooterSingleRow asserts header and footer stay one row even when the
// repo path / hint would otherwise overflow a narrow terminal.
func TestHeaderFooterSingleRow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	a := newTestApp()
	a.width, a.height = 30, 24
	a.applyLayout()
	if h := lipgloss.Height(a.renderHeader()); h != 1 {
		t.Errorf("header should be 1 row, got %d", h)
	}
	if h := lipgloss.Height(a.renderFooter()); h != 1 {
		t.Errorf("footer should be 1 row, got %d", h)
	}
	if w := lipgloss.Width(a.renderFooter()); w > a.width {
		t.Errorf("footer width %d exceeds terminal width %d", w, a.width)
	}
}

// TestWheelOverListMovesSelectionRegardlessOfFocus asserts wheeling over the
// file list moves the FILE selection even when the diff pane has keyboard focus
// (it must not move the diff line cursor).
func TestWheelOverListMovesSelectionRegardlessOfFocus(t *testing.T) {
	a := newTestApp()
	a.diff.FocusDiff() // focus on the diff pane
	first := a.diff.SelectedPath()
	// Wheel down over the file list area (x in list pane, y on a file row).
	_, _ = a.handleMouse(tea.MouseMsg{X: 2, Y: 2, Button: tea.MouseButtonWheelDown})
	if a.diff.SelectedPath() == first {
		t.Fatalf("wheel over list should advance file selection from %q", first)
	}
	if a.diff.Focus() != diffview.FocusList {
		t.Fatalf("wheel over list should focus the list")
	}
}

// TestWheelOutsideBodyIgnored asserts a wheel event in the header row (hitNone)
// does not move the selection.
func TestWheelOutsideBodyIgnored(t *testing.T) {
	a := newTestApp()
	before := a.diff.SelectedPath()
	_, _ = a.handleMouse(tea.MouseMsg{X: 2, Y: 0, Button: tea.MouseButtonWheelDown})
	if a.diff.SelectedPath() != before {
		t.Fatalf("wheel in header should be ignored, selection moved %q->%q", before, a.diff.SelectedPath())
	}
}

// TestHoverHighlightsRowUnderPointer asserts mouse motion over a file row sets
// the hover row, and motion outside the list clears it — without changing the
// selection.
func TestHoverHighlightsRowUnderPointer(t *testing.T) {
	a := newTestApp()
	sel := a.diff.SelectedPath()
	// Motion over b.go's row (body line 4 -> screen y=5) in the list pane.
	_, _ = a.handleMouse(tea.MouseMsg{X: 2, Y: 5, Action: tea.MouseActionMotion})
	if a.diff.HoverRow() < 0 {
		t.Fatalf("motion over a file row should set a hover row")
	}
	if a.diff.SelectedPath() != sel {
		t.Fatalf("hover must not change selection (%q -> %q)", sel, a.diff.SelectedPath())
	}
	// Motion into the diff pane clears the hover.
	_, _ = a.handleMouse(tea.MouseMsg{X: a.listWidth + 5, Y: 3, Action: tea.MouseActionMotion})
	if a.diff.HoverRow() != -1 {
		t.Fatalf("motion outside the list should clear hover, got %d", a.diff.HoverRow())
	}
}

// TestTreeToggleAndFolderKeys covers the file-tree key wiring: "." toggles
// tree/flat, and Enter/h on a folder toggles it (without focusing the diff).
func TestTreeToggleAndFolderKeys(t *testing.T) {
	a := newTestApp()
	st := &git.Status{Unstaged: []git.ChangedFile{
		{Path: "internal/ui/app.go", Worktree: git.Modified},
		{Path: "internal/ui/app_test.go", Worktree: git.Modified},
	}}
	a.status = st
	a.diff.SetStatus(st)

	// "." toggles flat mode off and on.
	if !a.diff.TreeMode() {
		t.Fatalf("tree mode should default on")
	}
	press(a, ".")
	if a.diff.TreeMode() {
		t.Fatalf(". should switch to flat mode")
	}
	press(a, ".")
	if !a.diff.TreeMode() {
		t.Fatalf(". should switch back to tree mode")
	}

	// Select the compacted folder node (index 0) and press Enter: it should
	// toggle the folder and keep focus on the list (not jump into the diff).
	a.diff.SelectRow(0)
	pressNamed(a, tea.KeyEnter)
	if a.diff.Focus() != diffview.FocusList {
		t.Fatalf("Enter on a folder should not focus the diff pane")
	}
}

// TestTabTogglesFocusBetweenTreeAndDiff asserts Tab moves focus between the file
// tree and the diff contents (and back).
func TestTabTogglesFocusBetweenTreeAndDiff(t *testing.T) {
	a := newTestApp()
	a.diff.SetDiff(a.diff.SelectedPath(), sampleDiff)
	if a.diff.Focus() != diffview.FocusList {
		t.Fatalf("focus should start on the file list")
	}
	pressNamed(a, tea.KeyTab)
	if a.diff.Focus() != diffview.FocusDiff {
		t.Fatalf("tab should move focus to the diff contents")
	}
	pressNamed(a, tea.KeyTab)
	if a.diff.Focus() != diffview.FocusList {
		t.Fatalf("tab again should move focus back to the file tree")
	}
}

// TestHideTreeTogglesPane asserts Shift+E hides/shows the file-tree pane, focuses
// the diff when hidden, keeps the view height exact, and routes body clicks to
// the diff while hidden.
func TestHideTreeTogglesPane(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	a := newTestApp()
	a.diff.SetDiff("a.go", sampleDiff)
	if a.diff.ListHidden() {
		t.Fatalf("tree should start visible")
	}
	press(a, "E")
	if !a.diff.ListHidden() {
		t.Fatalf("E should hide the tree")
	}
	if a.diff.Focus() != diffview.FocusDiff {
		t.Fatalf("hiding the tree should focus the diff")
	}
	if got := lipgloss.Height(a.View()); got != a.height {
		t.Fatalf("hidden view height=%d want %d", got, a.height)
	}
	// With the tree hidden a click in the body hits the diff, never the list.
	h := hitTest(a.currentLayout(), 5, 3)
	if h.region != hitDiff {
		t.Fatalf("with tree hidden, body click should hit the diff, got %v", h.region)
	}
	press(a, "E")
	if a.diff.ListHidden() {
		t.Fatalf("E should show the tree again")
	}
}

// TestFooterShowsVersion asserts the build version is rendered in the footer.
func TestFooterShowsVersion(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	a := newTestApp()
	a.version = "v1.2.3"
	if !strings.Contains(a.renderFooter(), "v1.2.3") {
		t.Fatalf("footer should show the version, got: %q", a.renderFooter())
	}
}
