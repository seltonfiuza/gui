// Package themepicker renders the theme-switcher overlay. Moving the selection
// applies the theme immediately (live preview); enter confirms, esc reverts to
// the theme that was active when the picker opened. The picker does not persist
// anything itself — the root App writes config on confirm.
package themepicker

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/selton/gui/internal/ui/styles"
)

// ResultKind is the outcome of a key handled by the picker.
type ResultKind int

const (
	// ResultNone: the picker handled the key (e.g. moved selection / live preview).
	ResultNone ResultKind = iota
	// ResultConfirm: the user pressed enter — persist Theme.
	ResultConfirm
	// ResultCancel: the user pressed esc — the original theme was restored.
	ResultCancel
)

// Result is returned from Update so the root can react (persist / close).
type Result struct {
	Kind  ResultKind
	Theme string // the chosen theme name (on confirm) or restored name (on cancel)
}

// Model is the theme picker overlay.
type Model struct {
	names    []string
	cursor   int
	original string // theme active when the picker opened (restored on esc)
}

// New builds a picker. Call Open before showing it.
func New() Model {
	return Model{names: styles.ThemeNames()}
}

// Open (re)initializes the picker for display, recording the currently active
// theme so esc can revert to it, and placing the cursor on it.
func (m *Model) Open() {
	m.names = styles.ThemeNames()
	m.original = styles.ActiveTheme()
	m.cursor = 0
	for i, n := range m.names {
		if n == m.original {
			m.cursor = i
			break
		}
	}
}

// Update handles a key. Moving the cursor applies the theme live; enter confirms
// and esc reverts. The root must re-render the whole UI after any ResultNone too
// (the active theme may have changed).
func (m *Model) Update(msg tea.KeyMsg) (Result, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.names)-1 {
			m.cursor++
		}
		m.applyCurrent()
		return Result{Kind: ResultNone}, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		m.applyCurrent()
		return Result{Kind: ResultNone}, nil
	case "enter":
		name := m.applyCurrent()
		return Result{Kind: ResultConfirm, Theme: name}, nil
	case "esc", "q":
		styles.SetTheme(m.original)
		return Result{Kind: ResultCancel, Theme: m.original}, nil
	}
	return Result{Kind: ResultNone}, nil
}

// applyCurrent applies the currently highlighted theme and returns its name.
func (m *Model) applyCurrent() string {
	if m.cursor < 0 || m.cursor >= len(m.names) {
		return styles.ActiveTheme()
	}
	return styles.SetTheme(m.names[m.cursor])
}

// View renders the centered picker overlay, windowing the theme list to fit the
// available height so it never renders off-screen on a short terminal.
func (m Model) View(width, height int) string {
	title := []string{styles.OverlayTitle.Render("Theme"), ""}
	var mid []string
	cursorLine := 0
	for i, n := range m.names {
		if i == m.cursor {
			cursorLine = len(mid)
			mid = append(mid, styles.SelectedRow.Render("> "+n))
		} else {
			mid = append(mid, "  "+n)
		}
	}
	tail := []string{"", styles.Desc.Render("j/k preview · enter apply · esc revert")}

	budget := height - 4 - len(title) - len(tail)
	mid = windowLines(mid, cursorLine, budget)
	rows := append(append(append([]string{}, title...), mid...), tail...)
	box := styles.Overlay.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// windowLines returns at most budget lines, always including the focus line and
// marking truncated ends. Returns lines unchanged when they already fit.
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

// Names returns the preset names in display order (for tests).
func (m Model) Names() []string { return m.names }

// Cursor returns the current selection index (for tests).
func (m Model) Cursor() int { return m.cursor }

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
