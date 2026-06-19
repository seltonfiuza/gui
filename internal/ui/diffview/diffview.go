// Package diffview renders the FR-1 header and FR-2 diff panel: three labeled
// file groups (Staged / Unstaged / Untracked) plus a scrollable, colorized
// unified-diff viewport for the selected file.
//
// The model is "dumb": it owns selection and scroll state and renders, but it
// does not perform git I/O. The root App owns the git.Service, watches the
// diffview's Selected() file, and feeds diffs in via SetDiff.
package diffview

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui/styles"
)

// Group identifies which of the three sections a file row belongs to.
type Group int

const (
	GroupStaged Group = iota
	GroupUnstaged
	GroupUntracked
)

// Row is one selectable file row, flattened across all groups.
type Row struct {
	Group Group
	File  git.ChangedFile
}

// Model is the diff panel: file list + diff viewport.
type Model struct {
	status *git.Status
	rows   []Row
	cursor int

	vp          viewport.Model
	vpReady     bool
	diffPath    string // path the viewport content currently belongs to
	listWidth   int
	totalWidth  int
	totalHeight int
}

// New builds an empty diff panel.
func New() Model {
	return Model{}
}

// SetStatus replaces the status and rebuilds the flattened row list, keeping the
// cursor in range (clamped) and on the same path when possible.
func (m *Model) SetStatus(s *git.Status) {
	prev := m.SelectedPath()
	m.status = s
	m.rebuild()
	// Try to keep selection on the same path.
	if prev != "" {
		for i, r := range m.rows {
			if r.File.Path == prev {
				m.cursor = i
				break
			}
		}
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) rebuild() {
	m.rows = m.rows[:0]
	if m.status == nil {
		return
	}
	for _, f := range m.status.Staged {
		m.rows = append(m.rows, Row{Group: GroupStaged, File: f})
	}
	for _, f := range m.status.Unstaged {
		m.rows = append(m.rows, Row{Group: GroupUnstaged, File: f})
	}
	for _, f := range m.status.Untracked {
		m.rows = append(m.rows, Row{Group: GroupUntracked, File: f})
	}
}

// SetSize lays out the panel for the available space.
func (m *Model) SetSize(width, height int) {
	m.totalWidth = width
	m.totalHeight = height
	m.listWidth = width / 3
	if m.listWidth < 24 {
		m.listWidth = min(24, width)
	}
	vpWidth := width - m.listWidth - 1
	if vpWidth < 1 {
		vpWidth = 1
	}
	if height < 1 {
		height = 1
	}
	if !m.vpReady {
		m.vp = viewport.New(vpWidth, height)
		m.vpReady = true
	} else {
		m.vp.Width = vpWidth
		m.vp.Height = height
	}
	// Re-colorize against the new width is not needed; content is plain wrapped.
}

// CursorDown moves selection to the next row.
func (m *Model) CursorDown() {
	if m.cursor < len(m.rows)-1 {
		m.cursor++
	}
}

// CursorUp moves selection to the previous row.
func (m *Model) CursorUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

// Selected returns the currently selected row and whether one exists.
func (m *Model) Selected() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return Row{}, false
	}
	return m.rows[m.cursor], true
}

// SelectedPath returns the selected file path, or "".
func (m *Model) SelectedPath() string {
	if r, ok := m.Selected(); ok {
		return r.File.Path
	}
	return ""
}

// DiffPath reports the path whose diff is currently loaded in the viewport.
func (m *Model) DiffPath() string { return m.diffPath }

// SetDiff loads a unified diff (for path) into the viewport, colorizing it.
func (m *Model) SetDiff(path, raw string) {
	m.diffPath = path
	if !m.vpReady {
		m.vp = viewport.New(40, 10)
		m.vpReady = true
	}
	m.vp.SetContent(colorize(raw, m.vp.Width))
	m.vp.GotoTop()
}

// ForwardViewport passes a key/message to the diff viewport (scrolling).
func (m *Model) ForwardViewport(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

// IsClean reports whether the working tree has no changes.
func (m *Model) IsClean() bool {
	return len(m.rows) == 0
}

// colorize applies diff line styling.
func colorize(raw string, width int) string {
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	var b strings.Builder
	for i, ln := range lines {
		var styled string
		switch {
		case strings.HasPrefix(ln, "@@"):
			styled = styles.Hunk.Render(ln)
		case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---"):
			styled = styles.DiffMeta.Render(ln)
		case strings.HasPrefix(ln, "diff ") || strings.HasPrefix(ln, "index ") ||
			strings.HasPrefix(ln, "new file") || strings.HasPrefix(ln, "deleted file") ||
			strings.HasPrefix(ln, "rename ") || strings.HasPrefix(ln, "similarity "):
			styled = styles.DiffMeta.Render(ln)
		case strings.HasPrefix(ln, "+"):
			styled = styles.Added.Render(ln)
		case strings.HasPrefix(ln, "-"):
			styled = styles.Removed.Render(ln)
		default:
			styled = ln
		}
		b.WriteString(styled)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// glyph maps a ChangedFile to its single-letter status glyph.
func glyph(g Group, f git.ChangedFile) string {
	if g == GroupUntracked {
		return "?"
	}
	code := f.Worktree
	if g == GroupStaged {
		code = f.Staged
	}
	switch code {
	case git.Added:
		return "A"
	case git.Modified, git.TypeChanged:
		return "M"
	case git.Deleted:
		return "D"
	case git.Renamed:
		return "R"
	case git.Copied:
		return "C"
	case git.Untracked:
		return "?"
	case git.Unmerged:
		return "U"
	default:
		return " "
	}
}

func label(f git.ChangedFile) string {
	if f.OrigPath != "" {
		return f.OrigPath + " → " + f.Path
	}
	return f.Path
}

// View renders the file list beside the diff viewport.
func (m *Model) View() string {
	if m.IsClean() {
		msg := styles.Clean.Render("nothing to commit, working tree clean")
		return lipgloss.Place(maxi(m.totalWidth, 1), maxi(m.totalHeight, 1),
			lipgloss.Center, lipgloss.Center, msg)
	}

	list := m.renderList()
	diff := m.renderDiff()

	list = lipgloss.NewStyle().Width(m.listWidth).Height(m.totalHeight).Render(list)
	gap := lipgloss.NewStyle().Foreground(lipgloss.Color("#3B4261")).Render(verticalBar(m.totalHeight))
	return lipgloss.JoinHorizontal(lipgloss.Top, list, gap, diff)
}

func (m *Model) renderList() string {
	var b strings.Builder
	lastGroup := Group(-1)
	for i, r := range m.rows {
		if r.Group != lastGroup {
			if lastGroup != -1 {
				b.WriteByte('\n')
			}
			b.WriteString(styles.GroupHeader.Render(groupName(r.Group)))
			b.WriteByte('\n')
			lastGroup = r.Group
		}
		g := styles.Glyph.Render(glyph(r.Group, r.File))
		line := g + " " + label(r.File)
		if i == m.cursor {
			line = styles.SelectedRow.Width(m.listWidth).Render(line)
		} else {
			line = styles.Row.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) renderDiff() string {
	if !m.vpReady {
		return ""
	}
	return m.vp.View()
}

func groupName(g Group) string {
	switch g {
	case GroupStaged:
		return "Staged"
	case GroupUnstaged:
		return "Unstaged"
	default:
		return "Untracked"
	}
}

func verticalBar(h int) string {
	if h < 1 {
		h = 1
	}
	return strings.TrimRight(strings.Repeat("│\n", h), "\n")
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
