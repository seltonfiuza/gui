// Package diffview renders the FR-1 header and FR-2 diff panel: three labeled
// file groups (Staged / Unstaged / Untracked) plus a scrollable, colorized
// unified-diff viewport for the selected file.
//
// The model is "dumb": it owns selection, focus and scroll state and renders,
// but it does not perform git I/O. The root App owns the git.Service, watches
// the diffview's Selected() file, and feeds diffs in via SetDiff.
//
// Two focus targets exist: the file list and the diff pane. When the list is
// focused, j/k move the file selection; when the diff is focused, j/k move a
// line cursor over the diff text (the SAME 0-based index over
// strings.Split(diffText, "\n") that git.ParseHunks uses), and }/{ jump between
// hunk @@ headers.
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

// Focus identifies which sub-pane currently receives j/k navigation.
type Focus int

const (
	// FocusList: j/k move the file selection.
	FocusList Focus = iota
	// FocusDiff: j/k move the diff line cursor; }/{ jump hunks.
	FocusDiff
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

	focus Focus

	vp          viewport.Model
	vpReady     bool
	diffPath    string   // path the viewport content currently belongs to
	diffRaw     string   // raw (uncolorized) diff text for the loaded path
	diffLines   []string // strings.Split(diffRaw, "\n") — the cursor index space
	styledLines []string // per-line colorized cache (parallel to diffLines)
	hunks       []git.Hunk
	lineCursor  int // 0-based index into diffLines
	prevCursor  int // last cursor position whose highlight is reflected in styledLines

	listWidth   int
	totalWidth  int
	totalHeight int
}

// New builds an empty diff panel.
func New() Model {
	return Model{}
}

// SetStatus replaces the status and rebuilds the flattened row list, reconciling
// the selection by path: the previously selected file stays selected if it is
// still present; if it vanished, a sensible neighbor is chosen (the row that now
// occupies the old index, clamped). This is what keeps a background refresh from
// yanking the user's selection back to row 0.
func (m *Model) SetStatus(s *git.Status) {
	prev := m.SelectedPath()
	prevIdx := m.cursor
	m.status = s
	m.rebuild()
	paths := make([]string, len(m.rows))
	for i, r := range m.rows {
		paths[i] = r.File.Path
	}
	m.cursor = ReconcileSelection(prev, prevIdx, paths)
}

// ReconcileSelection resolves the new cursor index after the file list changed.
// Given the previously selected path, its previous index, and the new ordered
// list of paths, it returns:
//   - the index of oldPath if it is still present (selection follows the file);
//   - otherwise a neighbor: the row now at oldIdx, clamped into range (so when a
//     file is removed the selection lands on what slid into its place);
//   - 0 for an empty list (callers must treat an empty list as "no selection").
//
// Pure and unit-tested.
func ReconcileSelection(oldPath string, oldIdx int, newPaths []string) int {
	if len(newPaths) == 0 {
		return 0
	}
	if oldPath != "" {
		for i, p := range newPaths {
			if p == oldPath {
				return i
			}
		}
	}
	// Path gone (or none before): keep the same slot, clamped.
	idx := oldIdx
	if idx >= len(newPaths) {
		idx = len(newPaths) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
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

// SetSize lays out the panel for the available space. listWidth is the desired
// absolute width of the file list; it is clamped to keep both panes usable.
func (m *Model) SetSize(width, height, listWidth int) {
	m.totalWidth = width
	m.totalHeight = height
	m.listWidth = ClampListWidth(width, listWidth)
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
	// Re-render the diff content for the new width so highlight + wrapping match.
	m.refreshViewport()
}

// MinListWidth / MinDiffWidth are the minimum pane widths enforced by resize.
const (
	MinListWidth = 16
	MinDiffWidth = 20
	dividerWidth = 1
)

// ClampListWidth returns a list width that respects the minimum widths of both
// panes given a total width. Pure function — unit-testable.
func ClampListWidth(total, want int) int {
	if total <= 0 {
		return 0
	}
	// The diff pane needs total - want - divider >= MinDiffWidth.
	maxList := total - dividerWidth - MinDiffWidth
	if maxList < 1 {
		// Terminal too narrow to honor both minimums: give the list whatever is
		// left after the divider, never less than 0.
		ml := total - dividerWidth
		if ml < 0 {
			ml = 0
		}
		return ml
	}
	if want < MinListWidth {
		want = MinListWidth
	}
	if want > maxList {
		want = maxList
	}
	if want < 0 {
		want = 0
	}
	return want
}

// Focus reports the current focus target.
func (m *Model) Focus() Focus { return m.focus }

// FocusDiff focuses the diff pane (j/k move the line cursor).
func (m *Model) FocusDiff() {
	m.focus = FocusDiff
	m.ensureCursorVisible()
}

// FocusList focuses the file list (j/k move the file selection).
func (m *Model) FocusList() { m.focus = FocusList }

// CursorDown moves the file selection (list focus) or the diff line cursor
// (diff focus) down by one.
func (m *Model) CursorDown() {
	if m.focus == FocusDiff {
		m.lineCursorDown()
		return
	}
	if m.cursor < len(m.rows)-1 {
		m.cursor++
	}
}

// CursorUp moves the file selection (list focus) or the diff line cursor (diff
// focus) up by one.
func (m *Model) CursorUp() {
	if m.focus == FocusDiff {
		m.lineCursorUp()
		return
	}
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *Model) lineCursorDown() {
	if m.lineCursor < len(m.diffLines)-1 {
		m.lineCursor++
		m.afterCursorMove()
	}
}

func (m *Model) lineCursorUp() {
	if m.lineCursor > 0 {
		m.lineCursor--
		m.afterCursorMove()
	}
}

// LineCursor returns the current 0-based diff line cursor.
func (m *Model) LineCursor() int { return m.lineCursor }

// Hunks returns the parsed hunks for the loaded diff.
func (m *Model) Hunks() []git.Hunk { return m.hunks }

// DiffRaw returns the raw diff text for the loaded path.
func (m *Model) DiffRaw() string { return m.diffRaw }

// HunkNext jumps the line cursor to the next hunk's @@ header (forward of the
// cursor). No-op when the diff is not focused or there is no next hunk.
func (m *Model) HunkNext() {
	if m.focus != FocusDiff {
		return
	}
	for _, h := range m.hunks {
		if h.StartLine > m.lineCursor {
			m.lineCursor = h.StartLine
			m.afterCursorMove()
			return
		}
	}
}

// HunkPrev jumps the line cursor to the previous hunk's @@ header (before the
// cursor). No-op when the diff is not focused or there is no previous hunk.
func (m *Model) HunkPrev() {
	if m.focus != FocusDiff {
		return
	}
	for i := len(m.hunks) - 1; i >= 0; i-- {
		if m.hunks[i].StartLine < m.lineCursor {
			m.lineCursor = m.hunks[i].StartLine
			m.afterCursorMove()
			return
		}
	}
}

func (m *Model) afterCursorMove() {
	m.restyleCursorLines()
	m.ensureCursorVisible()
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

// SetDiff loads a unified diff (for path) into the viewport, parsing hunks and
// resetting the line cursor to the first hunk (or top).
func (m *Model) SetDiff(path, raw string) {
	m.diffPath = path
	m.diffRaw = raw
	m.diffLines = strings.Split(raw, "\n")
	m.hunks, _ = git.ParseHunks(raw)
	// Start the cursor on the first hunk header when there is one, else at top.
	if len(m.hunks) > 0 {
		m.lineCursor = m.hunks[0].StartLine
	} else {
		m.lineCursor = 0
	}
	if !m.vpReady {
		m.vp = viewport.New(40, 10)
		m.vpReady = true
	}
	m.renderStyledLines()
	m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	m.vp.GotoTop()
	m.ensureCursorVisible()
}

// SetDiffPreserving loads a diff like SetDiff, but when the raw text is
// identical to what is already loaded for path it is a no-op that keeps the line
// cursor and viewport scroll exactly where they are. This is the background-tick
// path: an unchanged diff must never reset the user's position. When the text
// actually changed it falls through to SetDiff (cursor resets to the first hunk).
func (m *Model) SetDiffPreserving(path, raw string) {
	if m.diffPath == path && m.diffRaw == raw {
		return
	}
	m.SetDiff(path, raw)
}

// renderStyledLines (re)builds the per-line colorized cache from diffLines and
// applies the cursor highlight. This is the only full re-colorize; cursor moves
// use restyleCursorLines to avoid re-styling the whole diff.
func (m *Model) renderStyledLines() {
	m.styledLines = make([]string, len(m.diffLines))
	for i, ln := range m.diffLines {
		m.styledLines[i] = m.styleLine(i, ln)
	}
	m.prevCursor = m.lineCursor
}

// styleLine colorizes one diff line, applying the cursor highlight when the
// diff is focused and i is the cursor line.
func (m *Model) styleLine(i int, ln string) string {
	base := colorizeLine(ln)
	if m.focus == FocusDiff && i == m.lineCursor {
		width := m.vp.Width
		if width < 1 {
			width = lipgloss.Width(ln)
		}
		return styles.DiffCursor.Width(width).Render(ln)
	}
	return base
}

// restyleCursorLines updates the highlight after a cursor move. It re-styles
// only the lines whose highlight state changed — the new cursor line and the
// previously-highlighted line — then re-joins the cached styledLines. This
// keeps cursor movement O(1) in lipgloss work even for very large diffs (no
// re-parse, no full re-colorize per keypress).
func (m *Model) restyleCursorLines() {
	if len(m.styledLines) != len(m.diffLines) {
		// Cache out of sync (e.g. width changed); rebuild fully.
		m.renderStyledLines()
	} else {
		for _, i := range []int{m.prevCursor, m.lineCursor} {
			if i >= 0 && i < len(m.diffLines) {
				m.styledLines[i] = m.styleLine(i, m.diffLines[i])
			}
		}
	}
	m.prevCursor = m.lineCursor
	if m.vpReady {
		m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	}
}

// refreshViewport re-renders styled content for the current width (used on
// resize). Safe to call when no diff is loaded.
func (m *Model) refreshViewport() {
	if !m.vpReady {
		return
	}
	if len(m.diffLines) == 0 {
		return
	}
	m.renderStyledLines()
	m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	m.ensureCursorVisible()
}

// ensureCursorVisible scrolls the viewport so the line cursor stays in view.
func (m *Model) ensureCursorVisible() {
	if !m.vpReady || m.focus != FocusDiff {
		return
	}
	top := m.vp.YOffset
	bottom := top + m.vp.Height - 1
	switch {
	case m.lineCursor < top:
		m.vp.SetYOffset(m.lineCursor)
	case m.lineCursor > bottom:
		m.vp.SetYOffset(m.lineCursor - m.vp.Height + 1)
	}
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

// colorizeLine applies diff line styling to a single line.
func colorizeLine(ln string) string {
	switch {
	case strings.HasPrefix(ln, "@@"):
		return styles.Hunk.Render(ln)
	case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---"):
		return styles.DiffMeta.Render(ln)
	case strings.HasPrefix(ln, "diff ") || strings.HasPrefix(ln, "index ") ||
		strings.HasPrefix(ln, "new file") || strings.HasPrefix(ln, "deleted file") ||
		strings.HasPrefix(ln, "rename ") || strings.HasPrefix(ln, "similarity "):
		return styles.DiffMeta.Render(ln)
	case strings.HasPrefix(ln, "+"):
		return styles.Added.Render(ln)
	case strings.HasPrefix(ln, "-"):
		return styles.Removed.Render(ln)
	default:
		return ln
	}
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
			style := styles.SelectedRow.Width(m.listWidth)
			if m.focus == FocusDiff {
				// Dim the selection marker when focus is on the diff so it's clear
				// j/k now move within the diff.
				style = styles.SelectedRowInactive.Width(m.listWidth)
			}
			line = style.Render(line)
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
