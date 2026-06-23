// Package palette renders a fuzzy command palette: a searchable, centered
// overlay listing every command the app exposes. It owns its query input,
// filtering and selection but performs no actions — when the user picks a
// command it returns the chosen config.Action for the root App to dispatch.
package palette

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/config"
	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// Command is one searchable entry in the palette.
type Command struct {
	Action config.Action
	Title  string // human description, e.g. "Commit staged changes"
	Keys   string // display string of the bound key(s), e.g. "Shift+C"
}

// ResultKind is the outcome of an Update.
type ResultKind int

const (
	// ResultNone: the key was handled internally (typing / navigation).
	ResultNone ResultKind = iota
	// ResultRun: the user picked a command (Action is set).
	ResultRun
	// ResultCancel: the user dismissed the palette.
	ResultCancel
)

// Result is returned from Update for the root App to act on.
type Result struct {
	Kind   ResultKind
	Action config.Action
}

// Model is the command-palette overlay.
type Model struct {
	input    textinput.Model
	all      []Command
	filtered []Command
	cursor   int
}

// New builds an empty palette.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "search commands…"
	ti.CharLimit = 100
	return Model{input: ti}
}

// SetCommands sets the full command list shown when the query is empty.
func (m *Model) SetCommands(cmds []Command) {
	m.all = cmds
	m.refilter()
}

// Open clears the query, selects the top entry, and focuses the input. The
// returned cmd starts the input's cursor blink (and, crucially, focuses the
// stored input so it receives keys).
func (m *Model) Open() tea.Cmd {
	m.input.Reset()
	m.cursor = 0
	m.refilter()
	return m.input.Focus()
}

// refilter recomputes the filtered list for the current query, ranked by fuzzy
// score, and clamps the cursor into range.
func (m *Model) refilter() {
	q := strings.TrimSpace(m.input.Value())
	type scored struct {
		cmd   Command
		score int
	}
	var matches []scored
	for _, c := range m.all {
		if s, ok := fuzzyMatch(q, c.Title+" "+c.Keys); ok {
			matches = append(matches, scored{c, s})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	m.filtered = m.filtered[:0]
	for _, s := range matches {
		m.filtered = append(m.filtered, s.cmd)
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Update handles a key. Arrows / ctrl+n / ctrl+p move the selection; enter runs
// the highlighted command; esc cancels; anything else edits the query (so j/k
// are typed, not navigation).
func (m *Model) Update(msg tea.KeyMsg) (Result, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return Result{Kind: ResultCancel}, nil
	case "enter":
		if m.cursor >= 0 && m.cursor < len(m.filtered) {
			return Result{Kind: ResultRun, Action: m.filtered[m.cursor].Action}, nil
		}
		return Result{Kind: ResultNone}, nil
	case "down", "ctrl+n":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return Result{Kind: ResultNone}, nil
	case "up", "ctrl+p":
		if m.cursor > 0 {
			m.cursor--
		}
		return Result{Kind: ResultNone}, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// A query edit re-ranks the list; keep the top match selected.
	m.cursor = 0
	m.refilter()
	return Result{Kind: ResultNone}, cmd
}

// View renders the palette centered in the given area.
func (m *Model) View(width, height int) string {
	innerWidth := width - 8
	if innerWidth < 24 {
		innerWidth = 24
	}
	if innerWidth > 64 {
		innerWidth = 64
	}

	header := []string{styles.OverlayTitle.Render("Command palette"), "", m.input.View(), ""}
	hint := styles.Desc.Render("↑/↓ move · enter run · esc close")
	tail := []string{"", hint}

	var rows []string
	cursorLine := 0
	for i, c := range m.filtered {
		if i == m.cursor {
			cursorLine = len(rows)
		}
		rows = append(rows, m.renderRow(i, c, innerWidth))
	}
	if len(m.filtered) == 0 {
		rows = append(rows, styles.Desc.Render("  (no matching commands)"))
	}

	// Window the list so header + rows + tail fit within the box chrome (rounded
	// border + vertical padding = 4 rows).
	budget := height - 4 - len(header) - len(tail)
	rows = windowLines(rows, cursorLine, budget)

	body := append(append(append([]string{}, header...), rows...), tail...)
	box := styles.Overlay.Width(innerWidth).Render(strings.Join(body, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// keyColWidth is the column reserved for the key hint at the left of each row.
const keyColWidth = 12

func (m *Model) renderRow(i int, c Command, w int) string {
	keys := c.Keys
	gap := keyColWidth - ansi.StringWidth(keys)
	if gap < 1 {
		gap = 1
	}
	if i == m.cursor {
		text := keys + strings.Repeat(" ", gap) + c.Title
		text = ansi.Truncate(text, w, "…")
		return styles.SelectedRow.Width(w).Render(text)
	}
	title := ansi.Truncate(c.Title, w-keyColWidth-1, "…")
	return styles.Key.Render(keys) + strings.Repeat(" ", gap) + title
}

// windowLines returns at most budget lines that always include the focus line,
// marking truncated ends with a "more" indicator.
func windowLines(lines []string, focus, budget int) []string {
	if budget < 1 {
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

// fuzzyMatch reports whether query is a subsequence of target (case-insensitive)
// and returns a score: higher is a better match. Consecutive and word-start
// matches are favored, and shorter targets get a small edge. An empty query
// matches everything with a neutral score.
func fuzzyMatch(query, target string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q := strings.ToLower(query)
	t := strings.ToLower(target)
	score := 0
	ti := 0
	streak := 0
	for qi := 0; qi < len(q); qi++ {
		c := q[qi]
		matched := false
		for ti < len(t) {
			if t[ti] == c {
				score++
				if streak > 0 {
					score += 2 // consecutive run
				}
				if ti == 0 || t[ti-1] == ' ' || t[ti-1] == '/' {
					score += 3 // start of a word
				}
				ti++
				streak++
				matched = true
				break
			}
			ti++
			streak = 0
		}
		if !matched {
			return 0, false
		}
	}
	score -= len(t) / 20
	return score, true
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
