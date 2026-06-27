package commitpanel

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/seltonfiuza/gui/internal/git"
)

func sample() []git.Commit {
	return []git.Commit{
		{SHA: "1111111aaa", Short: "1111111", Subject: "add feature", RelTime: "3h", When: time.Now()},
		{SHA: "2222222bbb", Short: "2222222", Subject: "fix bug", RelTime: "1d", When: time.Now()},
	}
}

func TestViewRendersShortShaSubjectAndAge(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetCommits(sample())
	out := m.View()
	if !strings.Contains(out, "1111111") || !strings.Contains(out, "add feature") || !strings.Contains(out, "3h") {
		t.Errorf("row missing fields:\n%s", out)
	}
}

func TestEnterActivatesSelectedCommit(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetCommits(sample())
	intent := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if intent.Kind != IntentActivate || intent.SHA != "1111111aaa" {
		t.Errorf("Enter intent = %+v, want Activate sha 1111111aaa", intent)
	}
}

func TestEmptyShowsPlaceholder(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetPlaceholder("no commits yet")
	if !strings.Contains(m.View(), "no commits yet") {
		t.Errorf("placeholder missing:\n%s", m.View())
	}
}

func TestRenderRowNarrowWidthNoPanic(t *testing.T) {
	m := New()
	m.SetSize(5, 4) // narrower than sha(7)+reltime(2)
	m.SetCommits(sample())
	_ = m.View() // must not panic
}
