// Package commitpanel renders the bottom-left scrollable "Commits" block: recent
// git log entries. Activation (Enter) yields the selected commit's SHA so the app
// can load its diff into the main pane.
package commitpanel

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/git"
	"github.com/seltonfiuza/gui/internal/ui/listpane"
	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// IntentKind is the result of a key press on the panel.
type IntentKind int

const (
	IntentNone IntentKind = iota
	IntentActivate
)

// Intent is returned from Update; SHA is set when Kind == IntentActivate.
type Intent struct {
	Kind IntentKind
	SHA  string
}

// Model is the Commits block.
type Model struct {
	pane    listpane.Model
	commits []git.Commit
	width   int
}

// New builds an empty Commits panel.
func New() Model { return Model{pane: listpane.New("Commits")} }

// SetSize sizes the block; height includes the title row.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.pane.SetSize(width, height)
	if len(m.commits) > 0 {
		m.rebuildRows()
	}
}

// SetFocused marks the block focused.
func (m *Model) SetFocused(b bool) { m.pane.SetFocused(b) }

// SetPlaceholder sets the muted empty-state text.
func (m *Model) SetPlaceholder(s string) { m.pane.SetPlaceholder(s) }

// SetCommits replaces the commit list and rebuilds rows.
func (m *Model) SetCommits(cs []git.Commit) {
	m.commits = cs
	m.rebuildRows()
}

// rebuildRows re-renders the commit rows for the current width and pushes them
// into the underlying list pane. Called on data change and on resize.
func (m *Model) rebuildRows() {
	rows := make([]string, len(m.commits))
	for i, c := range m.commits {
		rows[i] = m.renderRow(c)
	}
	m.pane.SetRows(rows)
}

func (m *Model) renderRow(c git.Commit) string {
	sha := styles.DiffMeta.Render(c.Short)
	age := styles.Desc.Render(c.RelTime)
	subj := c.Subject
	// Reserve space for "sha  subject … age".
	budget := m.width - ansi.StringWidth(c.Short) - ansi.StringWidth(c.RelTime) - 4 // 2 spaces separator + 1 min padding + 1 buffer
	if budget < 1 {
		budget = 1
	}
	subj = ansi.Truncate(subj, budget, "…")
	line := sha + "  " + styles.Row.Render(subj)
	pad := m.width - ansi.StringWidth(c.Short) - 2 - ansi.StringWidth(subj) - ansi.StringWidth(c.RelTime)
	if pad < 1 {
		pad = 1
	}
	return line + strings.Repeat(" ", pad) + age
}

// Update handles a key press and returns an activation intent.
func (m *Model) Update(key tea.KeyMsg) Intent {
	switch key.String() {
	case "j", "down":
		m.pane.MoveSelection(1)
	case "k", "up":
		m.pane.MoveSelection(-1)
	case "enter":
		if sel := m.pane.Selected(); sel >= 0 && sel < len(m.commits) {
			return Intent{Kind: IntentActivate, SHA: m.commits[sel].SHA}
		}
	}
	return Intent{Kind: IntentNone}
}

// Selected returns the currently highlighted commit, or ok=false when empty.
func (m *Model) Selected() (git.Commit, bool) {
	if sel := m.pane.Selected(); sel >= 0 && sel < len(m.commits) {
		return m.commits[sel], true
	}
	return git.Commit{}, false
}

// ScrollBy moves the selection by delta (used for mouse-wheel scroll).
func (m *Model) ScrollBy(delta int) { m.pane.MoveSelection(delta) }

// View renders the block.
func (m *Model) View() string { return m.pane.View() }
