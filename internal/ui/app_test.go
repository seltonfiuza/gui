package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/selton/gui/internal/git"
)

func newTestApp() *App {
	a := New(&git.Service{}).(*App)
	// Give it a size and a stub status so the diff view has rows.
	a.width, a.height = 80, 24
	a.diff.SetSize(80, a.bodyHeight())
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
