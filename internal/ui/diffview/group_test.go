package diffview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/selton/gui/internal/git"
)

// TestUnstagedUntrackedHeadersHaveBackground asserts the Unstaged and Untracked
// group headers render with a background tint (full-width banner) while Staged
// stays a plain colored label without a background.
func TestUnstagedUntrackedHeadersHaveBackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor) // deterministic in non-TTY
	const bgSGR = "48;2;"                        // truecolor background introducer
	staged := renderGroupHeader(GroupStaged, 1, 30)
	unstaged := renderGroupHeader(GroupUnstaged, 1, 30)
	untracked := renderGroupHeader(GroupUntracked, 1, 30)
	if strings.Contains(staged, bgSGR) {
		t.Errorf("Staged header should NOT have a background:\n%q", staged)
	}
	if !strings.Contains(unstaged, bgSGR) {
		t.Errorf("Unstaged header should have a background banner:\n%q", unstaged)
	}
	if !strings.Contains(untracked, bgSGR) {
		t.Errorf("Untracked header should have a background banner:\n%q", untracked)
	}
}

// TestRenderListOmitsEmptyGroupsAndBadgesCounts asserts only non-empty groups
// produce headers and each carries a count badge.
func TestRenderListOmitsEmptyGroupsAndBadgesCounts(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 40)
	m.SetStatus(&git.Status{
		Unstaged: []git.ChangedFile{
			{Path: "a.go", Worktree: git.Modified},
			{Path: "b.go", Worktree: git.Modified},
		},
		Untracked: []git.ChangedFile{
			{Path: "c.txt", Worktree: git.Untracked},
		},
		// No staged files: the Staged header must NOT appear.
	})
	out := m.renderList()

	if strings.Contains(out, "Staged") {
		t.Errorf("empty Staged group should be omitted:\n%s", out)
	}
	if !strings.Contains(out, "Unstaged") || !strings.Contains(out, "(2)") {
		t.Errorf("Unstaged header with (2) badge expected:\n%s", out)
	}
	if !strings.Contains(out, "Untracked") || !strings.Contains(out, "(1)") {
		t.Errorf("Untracked header with (1) badge expected:\n%s", out)
	}
}

// TestGroupHeadersAreVisuallyDistinct asserts the three group headers render
// with different ANSI styling (distinct colors), not one shared style.
func TestGroupHeadersAreVisuallyDistinct(t *testing.T) {
	staged := renderGroupHeader(GroupStaged, 1, 30)
	unstaged := renderGroupHeader(GroupUnstaged, 1, 30)
	untracked := renderGroupHeader(GroupUntracked, 1, 30)
	if staged == unstaged || unstaged == untracked || staged == untracked {
		t.Errorf("group headers should be visually distinct:\n%q\n%q\n%q",
			staged, unstaged, untracked)
	}
}

// TestListLineToRowMapsClicks asserts the list-line→row mapper accounts for
// group-header lines and blank separators (used by the mouse hit-test).
func TestListLineToRowMapsClicks(t *testing.T) {
	m := New()
	m.SetSize(80, 20, 40)
	m.SetStatus(&git.Status{
		Unstaged: []git.ChangedFile{
			{Path: "a.go", Worktree: git.Modified},
			{Path: "b.go", Worktree: git.Modified},
		},
		Untracked: []git.ChangedFile{
			{Path: "c.txt", Worktree: git.Untracked},
		},
	})
	// Body layout:
	// 0: "Unstaged (2)"  (header)
	// 1: a.go            (row 0)
	// 2: b.go            (row 1)
	// 3: (blank sep)
	// 4: "Untracked (1)" (header)
	// 5: c.txt           (row 2)
	cases := []struct {
		line    int
		wantRow int
		wantOK  bool
	}{
		{0, -1, false}, // header
		{1, 0, true},
		{2, 1, true},
		{3, -1, false}, // blank
		{4, -1, false}, // header
		{5, 2, true},
		{99, -1, false},
	}
	for _, c := range cases {
		row, ok := m.ListLineToRow(c.line)
		if ok != c.wantOK || (ok && row != c.wantRow) {
			t.Errorf("ListLineToRow(%d)=(%d,%v) want (%d,%v)", c.line, row, ok, c.wantRow, c.wantOK)
		}
	}
}
