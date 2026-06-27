package prpanel

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/seltonfiuza/gui/internal/github"
)

func sample() []github.PR {
	return []github.PR{
		{Number: 13, Title: "git blame popup"},
		{Number: 12, Title: "create pr panel"},
	}
}

func TestViewRendersNumberAndTitle(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetPRs(sample())
	out := m.View()
	if !strings.Contains(out, "#13") || !strings.Contains(out, "git blame popup") {
		t.Errorf("row missing fields:\n%s", out)
	}
}

func TestEnterActivatesSelectedPR(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetPRs(sample())
	intent := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if intent.Kind != IntentActivate || intent.Number != 13 {
		t.Errorf("Enter intent = %+v, want Activate #13", intent)
	}
}

func TestSetErrorShowsMessageAndNoRows(t *testing.T) {
	m := New()
	m.SetSize(40, 4)
	m.SetPRs(sample())
	m.SetError("no remote")
	out := m.View()
	if !strings.Contains(out, "no remote") {
		t.Errorf("error text missing:\n%s", out)
	}
	if i := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); i.Kind != IntentNone {
		t.Errorf("Enter after error should be inert, got %+v", i)
	}
}

func TestRenderRowNarrowWidthNoPanic(t *testing.T) {
	m := New()
	m.SetSize(4, 4)
	m.SetPRs(sample())
	_ = m.View() // must not panic
}
