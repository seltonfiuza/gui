// Package branchpanel renders the FR-3 branch overlay: Local and Remote-tracking
// sections with checkout / create / delete / rebase actions.
//
// The panel owns its navigation, textinput prompt, and confirm state, but does
// NOT perform git I/O. When the user commits an action it returns an Intent; the
// root App turns the Intent into a tea.Cmd against the git.Service and feeds the
// result back via SetError / Reload.
package branchpanel

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui/styles"
)

// IntentKind is the type of git action the user requested from the panel.
type IntentKind int

const (
	// IntentNone means the panel handled the key with no git action needed.
	IntentNone IntentKind = iota
	IntentCheckout
	IntentCreate
	IntentDelete
	IntentRebase
	IntentClose
)

// Intent is an action the root App should perform against the git.Service.
type Intent struct {
	Kind  IntentKind
	Name  string // branch to act on (checkout/delete/rebase target, create start point's name handled separately)
	Start string // start point for create
	Force bool   // force delete
}

type mode int

const (
	modeList mode = iota
	modeCreate
	modeConfirm
)

// confirmAction is the action a confirm dialog will fire if accepted.
type confirmAction int

const (
	confirmDelete confirmAction = iota
	confirmForceDelete
	confirmRebase
)

// Model is the branch overlay.
type Model struct {
	local  []git.Branch
	remote []git.Branch
	rows   []git.Branch // flattened: local then remote
	cursor int

	mode    mode
	input   textinput.Model
	confirm confirmAction
	prompt  string

	current string
	errMsg  string
}

// New builds an empty branch panel.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "new-branch-name"
	ti.CharLimit = 200
	return Model{input: ti}
}

// SetBranches populates the panel sections and resets transient state.
func (m *Model) SetBranches(branches []git.Branch) {
	m.local = m.local[:0]
	m.remote = m.remote[:0]
	m.rows = m.rows[:0]
	for _, b := range branches {
		if b.IsCurrent {
			m.current = b.Name
		}
		if b.IsRemote {
			m.remote = append(m.remote, b)
		} else {
			m.local = append(m.local, b)
		}
	}
	m.rows = append(m.rows, m.local...)
	m.rows = append(m.rows, m.remote...)
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Inputting reports whether the panel is mid-interaction in a way a background
// refresh must not disturb: a text-entry prompt (create) or a confirm dialog.
func (m *Model) Inputting() bool {
	return m.mode == modeCreate || m.mode == modeConfirm
}

// SetError shows an inline error message in the overlay and returns to the list.
func (m *Model) SetError(msg string) {
	m.errMsg = msg
	m.mode = modeList
}

// ClearError clears any inline error.
func (m *Model) ClearError() { m.errMsg = "" }

// Reset returns the panel to its initial list mode (called when opened).
func (m *Model) Reset() {
	m.mode = modeList
	m.errMsg = ""
	m.input.Reset()
	m.input.Blur()
}

func (m *Model) selected() (git.Branch, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return git.Branch{}, false
	}
	return m.rows[m.cursor], true
}

// Update handles a key message and returns an Intent for the root to execute.
func (m *Model) Update(msg tea.KeyMsg) (Intent, tea.Cmd) {
	m.errMsg = ""
	switch m.mode {
	case modeCreate:
		return m.updateCreate(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	default:
		return m.updateList(msg)
	}
}

func (m *Model) updateList(msg tea.KeyMsg) (Intent, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return Intent{Kind: IntentClose}, nil
	case "j", "down":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter", "c":
		if b, ok := m.selected(); ok {
			return Intent{Kind: IntentCheckout, Name: b.Name}, nil
		}
	case "n":
		m.mode = modeCreate
		m.input.Reset()
		return IntentNoneIntent(), m.input.Focus()
	case "d":
		if b, ok := m.selected(); ok && !b.IsRemote {
			m.mode = modeConfirm
			m.confirm = confirmDelete
			m.prompt = "Delete branch '" + b.Name + "'? (y/n)"
		}
	case "R":
		if b, ok := m.selected(); ok {
			m.mode = modeConfirm
			m.confirm = confirmRebase
			m.prompt = "Rebase '" + m.current + "' onto '" + b.Name + "'? (y/n)"
		}
	}
	return IntentNoneIntent(), nil
}

func (m *Model) updateCreate(msg tea.KeyMsg) (Intent, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return IntentNoneIntent(), nil
	case "enter":
		name := strings.TrimSpace(m.input.Value())
		m.mode = modeList
		m.input.Blur()
		if name == "" {
			return IntentNoneIntent(), nil
		}
		start := ""
		if b, ok := m.selected(); ok {
			start = b.Name
		}
		return Intent{Kind: IntentCreate, Name: name, Start: start}, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return IntentNoneIntent(), cmd
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (Intent, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		b, ok := m.selected()
		m.mode = modeList
		if !ok {
			return IntentNoneIntent(), nil
		}
		switch m.confirm {
		case confirmDelete:
			return Intent{Kind: IntentDelete, Name: b.Name, Force: false}, nil
		case confirmForceDelete:
			return Intent{Kind: IntentDelete, Name: b.Name, Force: true}, nil
		case confirmRebase:
			return Intent{Kind: IntentRebase, Name: b.Name}, nil
		}
	case "n", "N", "esc":
		m.mode = modeList
	}
	return IntentNoneIntent(), nil
}

// EscalateToForceDelete switches a pending delete confirm into a force-delete
// confirm (used by the root when DeleteBranch reports the branch is unmerged).
func (m *Model) EscalateToForceDelete(name string) {
	m.mode = modeConfirm
	m.confirm = confirmForceDelete
	m.prompt = "Branch '" + name + "' is not fully merged. Force delete? (y/n)"
}

// IntentNoneIntent is a tiny helper to keep call sites terse.
func IntentNoneIntent() Intent { return Intent{Kind: IntentNone} }

// View renders the overlay centered in the given area. The branch list is
// windowed to fit the available height (keeping the selected branch visible), so
// a repo with many branches never overflows the screen.
func (m *Model) View(width, height int) string {
	title := []string{styles.OverlayTitle.Render("Branches"), ""}

	// Scrollable middle: section headers + branch rows. Record the rendered line
	// holding the cursor so the window can keep it in view.
	var mid []string
	cursorLine := 0
	mid = append(mid, styles.GroupHeader.Render("Local"))
	idx := 0
	for _, br := range m.local {
		if idx == m.cursor {
			cursorLine = len(mid)
		}
		mid = append(mid, m.renderRow(idx, br))
		idx++
	}
	if len(m.local) == 0 {
		mid = append(mid, styles.Desc.Render("  (none)"))
	}
	mid = append(mid, "", styles.GroupHeader.Render("Remote-tracking"))
	for _, br := range m.remote {
		if idx == m.cursor {
			cursorLine = len(mid)
		}
		mid = append(mid, m.renderRow(idx, br))
		idx++
	}
	if len(m.remote) == 0 {
		mid = append(mid, styles.Desc.Render("  (none)"))
	}

	// Trailing controls / prompt / inline error (always shown).
	var tail []string
	switch m.mode {
	case modeCreate:
		tail = append(tail, "", m.input.View())
	case modeConfirm:
		tail = append(tail, "", styles.Inline.Render(m.prompt))
	default:
		tail = append(tail, "", styles.Desc.Render("enter/c checkout · n new · d delete · R rebase · esc close"))
	}
	if m.errMsg != "" {
		tail = append(tail, styles.Inline.Render(m.errMsg))
	}

	// Window the middle so title + middle + tail fit within the box chrome
	// (rounded border + vertical padding = 4 rows).
	budget := height - 4 - len(title) - len(tail)
	mid = windowLines(mid, cursorLine, budget)

	rows := append(append(append([]string{}, title...), mid...), tail...)
	box := styles.Overlay.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// windowLines returns a slice of at most budget lines that always includes the
// focus line, marking truncated ends with a "more" indicator. When everything
// fits it returns lines unchanged.
func windowLines(lines []string, focus, budget int) []string {
	if budget < 1 {
		// No room for any list rows: keep the always-shown title/tail rather than
		// forcing a row that would push the box past the available height.
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	start := focus - budget/2
	if start < 0 {
		start = 0
	}
	if start+budget > len(lines) {
		start = len(lines) - budget
	}
	out := make([]string, budget)
	copy(out, lines[start:start+budget])
	if start > 0 {
		out[0] = styles.Desc.Render("  ↑ more")
	}
	if start+budget < len(lines) {
		out[budget-1] = styles.Desc.Render("  ↓ more")
	}
	return out
}

func (m *Model) renderRow(idx int, br git.Branch) string {
	marker := "  "
	if br.IsCurrent {
		marker = styles.Branch.Render("* ")
	}
	line := marker + br.Name
	if idx == m.cursor {
		return styles.SelectedRow.Render(line)
	}
	return line
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
