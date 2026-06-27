// Package prpanel renders the bottom-left scrollable "Pull Requests" block. It
// shows open PRs for the repo; activation (Enter) yields the selected PR number
// so the app can open the full-screen PR view.
package prpanel

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/github"
	"github.com/seltonfiuza/gui/internal/ui/listpane"
	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// IntentKind is the result of a key press on the panel.
type IntentKind int

const (
	IntentNone IntentKind = iota
	IntentActivate
)

// Intent is returned from Update; Number is set when Kind == IntentActivate.
type Intent struct {
	Kind   IntentKind
	Number int
}

// Model is the Pull Requests block.
type Model struct {
	pane  listpane.Model
	prs   []github.PR
	width int
}

// New builds an empty PR panel.
func New() Model { return Model{pane: listpane.New("Pull Requests")} }

// SetSize sizes the block; height includes the title row.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.pane.SetSize(width, height)
	if len(m.prs) > 0 {
		m.rebuildRows()
	}
}

// SetFocused marks the block focused.
func (m *Model) SetFocused(b bool) { m.pane.SetFocused(b) }

// SetPlaceholder sets the muted empty-state text (rows preserved).
func (m *Model) SetPlaceholder(s string) { m.pane.SetPlaceholder(s) }

// SetError clears rows and shows msg as the block body (e.g. "no remote").
func (m *Model) SetError(msg string) {
	m.prs = nil
	m.pane.SetRows(nil)
	m.pane.SetPlaceholder(msg)
}

// SetPRs replaces the PR list and rebuilds rows.
func (m *Model) SetPRs(prs []github.PR) {
	m.prs = prs
	m.rebuildRows()
	if len(prs) == 0 {
		m.pane.SetPlaceholder("no open PRs")
	}
}

// rebuildRows re-renders PR rows at the current width and pushes them to the pane.
func (m *Model) rebuildRows() {
	rows := make([]string, len(m.prs))
	for i, pr := range m.prs {
		rows[i] = m.renderRow(pr)
	}
	m.pane.SetRows(rows)
}

func (m *Model) renderRow(pr github.PR) string {
	num := styles.DiffMeta.Render("#" + strconv.Itoa(pr.Number))
	title := pr.Title
	if pr.Draft {
		title = "(draft) " + title
	}
	budget := m.width - ansi.StringWidth("#"+strconv.Itoa(pr.Number)) - 2
	if budget < 1 {
		budget = 1
	}
	title = ansi.Truncate(title, budget, "…")
	return num + "  " + styles.Row.Render(title)
}

// Update handles a key press and returns an activation intent.
func (m *Model) Update(key tea.KeyMsg) Intent {
	switch key.String() {
	case "j", "down":
		m.pane.MoveSelection(1)
	case "k", "up":
		m.pane.MoveSelection(-1)
	case "enter":
		if sel := m.pane.Selected(); sel >= 0 && sel < len(m.prs) {
			return Intent{Kind: IntentActivate, Number: m.prs[sel].Number}
		}
	}
	return Intent{Kind: IntentNone}
}

// ScrollBy moves the selection by delta (used for mouse-wheel scroll).
func (m *Model) ScrollBy(delta int) { m.pane.MoveSelection(delta) }

// View renders the block.
func (m *Model) View() string { return m.pane.View() }
