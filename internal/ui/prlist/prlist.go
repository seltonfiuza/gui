// Package prlist renders the merge/pull request overlay: a list of the open
// requests for the origin remote (fetched via internal/github) and a detail
// screen laying out one request's title, description, pipeline status, and diff
// in separate panes. Like branchpanel it owns only navigation and rendering; the
// root App performs the I/O and feeds results back via SetPRs / SetError /
// SetDetail / SetDetailError.
package prlist

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/github"
	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// IntentKind is an action the root App should perform.
type IntentKind int

const (
	// IntentNone means the panel handled the key with no action needed.
	IntentNone IntentKind = iota
	// IntentClose asks the root to close the overlay.
	IntentClose
	// IntentOpenDetail asks the root to fetch the detail for Number.
	IntentOpenDetail
)

// Intent is returned from Update for the root App to act on.
type Intent struct {
	Kind   IntentKind
	Number int
}

type mode int

const (
	modeList mode = iota
	modeDetail
)

// detailFocus is the pane the scroll keys act on in the detail screen.
type detailFocus int

const (
	focusDiff detailFocus = iota
	focusDesc
)

// Model is the request overlay.
type Model struct {
	prs     []github.PR
	cursor  int
	loading bool
	errMsg  string
	title   string

	mode mode

	// detail state
	detail        github.PR
	detailDiff    string
	detailLoading bool
	detailErr     string
	focus         detailFocus
	diffLines     []string // colorized diff lines
	detailScroll  int      // first visible diff line
	detailDiffH   int      // visible diff rows at last render (for paging)
	descScroll    int      // first visible description line
	descH         int      // visible description rows at last render
	descTotal     int      // total wrapped description lines at last render
}

// New builds an empty request panel.
func New() Model { return Model{title: "Pull Requests"} }

// Open resets the panel into its loading state with the given title (e.g.
// "Merge Requests" for GitLab, "Pull Requests" for GitHub).
func (m *Model) Open(title string) {
	m.title = title
	m.loading = true
	m.errMsg = ""
	m.prs = nil
	m.cursor = 0
	m.mode = modeList
}

// SetPRs populates the list and clears the loading/error state.
func (m *Model) SetPRs(prs []github.PR) {
	m.prs = prs
	m.loading = false
	m.errMsg = ""
	if m.cursor >= len(m.prs) {
		m.cursor = len(m.prs) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// SetError shows an inline error in place of the list.
func (m *Model) SetError(msg string) {
	m.errMsg = msg
	m.loading = false
}

// SetDetail populates the detail screen with a request and its diff.
func (m *Model) SetDetail(pr github.PR, diff string) {
	m.detail = pr
	m.detailDiff = diff
	m.detailLoading = false
	m.detailErr = ""
	m.focus = focusDiff
	m.detailScroll = 0
	m.descScroll = 0
	m.diffLines = buildDiffLines(diff)
}

// SetDetailError shows an inline error in the detail screen.
func (m *Model) SetDetailError(msg string) {
	m.detailErr = msg
	m.detailLoading = false
}

// selected returns the request under the cursor.
func (m *Model) selected() (github.PR, bool) {
	if m.cursor < 0 || m.cursor >= len(m.prs) {
		return github.PR{}, false
	}
	return m.prs[m.cursor], true
}

// Update handles a key and returns an Intent for the root to execute.
func (m *Model) Update(msg tea.KeyMsg) Intent {
	if m.mode == modeDetail {
		return m.updateDetail(msg)
	}
	return m.updateList(msg)
}

func (m *Model) updateList(msg tea.KeyMsg) Intent {
	switch msg.String() {
	case "esc", "q":
		return Intent{Kind: IntentClose}
	case "j", "down":
		if m.cursor < len(m.prs)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if pr, ok := m.selected(); ok {
			m.mode = modeDetail
			m.detail = pr
			m.detailDiff = ""
			m.detailLoading = true
			m.detailErr = ""
			m.focus = focusDiff
			m.detailScroll = 0
			m.descScroll = 0
			m.diffLines = nil
			return Intent{Kind: IntentOpenDetail, Number: pr.Number}
		}
	}
	return Intent{Kind: IntentNone}
}

func (m *Model) updateDetail(msg tea.KeyMsg) Intent {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeList
		return Intent{Kind: IntentNone}
	case "tab":
		if m.focus == focusDiff {
			m.focus = focusDesc
		} else {
			m.focus = focusDiff
		}
		return Intent{Kind: IntentNone}
	}
	// Scroll keys act on the focused pane.
	if m.focus == focusDesc {
		m.descScroll += scrollDelta(msg.String(), m.descH)
		m.clampDesc()
	} else {
		m.detailScroll += scrollDelta(msg.String(), m.detailDiffH)
		m.clampScroll()
	}
	return Intent{Kind: IntentNone}
}

// scrollDelta maps a scroll key to a line delta for a viewport of viewH rows.
// ctrl+d/ctrl+u move by half a page (vim-style); j/k by one line.
func scrollDelta(key string, viewH int) int {
	page := viewH / 2
	if page < 1 {
		page = 1
	}
	switch key {
	case "j", "down":
		return 1
	case "k", "up":
		return -1
	case "ctrl+d":
		return page
	case "ctrl+u":
		return -page
	}
	return 0
}

// clampScroll keeps the diff scroll offset within range for the last-rendered
// viewport height.
func (m *Model) clampScroll() {
	m.detailScroll = clampScroll(m.detailScroll, len(m.diffLines), m.detailDiffH)
}

// clampDesc keeps the description scroll offset within range.
func (m *Model) clampDesc() {
	m.descScroll = clampScroll(m.descScroll, m.descTotal, m.descH)
}

// clampScroll bounds offset to [0, total-viewH].
func clampScroll(offset, total, viewH int) int {
	max := total - viewH
	if max < 0 {
		max = 0
	}
	if offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// View renders the overlay centered in the given area.
func (m *Model) View(width, height int) string {
	if m.mode == modeDetail {
		return m.viewDetail(width, height)
	}
	return m.viewList(width, height)
}

func (m *Model) viewList(width, height int) string {
	head := []string{styles.OverlayTitle.Render(m.title), ""}

	var mid []string
	cursorLine := 0
	switch {
	case m.loading:
		mid = append(mid, styles.Desc.Render("  loading…"))
	case m.errMsg != "":
		mid = append(mid, styles.Inline.Render("  "+m.errMsg))
	case len(m.prs) == 0:
		mid = append(mid, styles.Desc.Render("  (no open requests)"))
	default:
		for i, pr := range m.prs {
			if i == m.cursor {
				cursorLine = len(mid)
			}
			mid = append(mid, m.renderRow(i, pr))
		}
	}

	tail := []string{"", styles.Desc.Render("j/k move · enter open · esc close")}

	budget := height - 4 - len(head) - len(tail)
	mid = windowLines(mid, cursorLine, budget)

	rows := append(append(append([]string{}, head...), mid...), tail...)
	box := styles.Overlay.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// viewDetail lays out the request as four panes: a full-width title bar, a left
// column with the description (top) and pipeline status (bottom), and a
// full-height diff pane on the right.
func (m *Model) viewDetail(width, height int) string {
	if m.detailLoading {
		return m.centered(width, height, styles.Desc.Render("loading…"))
	}
	if m.detailErr != "" {
		return m.centered(width, height, styles.Inline.Render(m.detailErr))
	}

	hint := styles.Desc.Render("tab switch pane · j/k scroll · ctrl+d/ctrl+u page · esc back")

	const titleH, pipeH = 4, 4
	region := height - titleH - 1 // panes row + 1 hint line
	if region < pipeH+3 {
		region = pipeH + 3
	}

	lw := width * 40 / 100
	if lw < 20 {
		lw = 20
	}
	if lw > width-20 {
		lw = width - 20
	}
	if lw < 1 {
		lw = 1
	}
	rw := width - lw

	descH := region - pipeH
	if descH < 3 {
		descH = 3
	}

	// Title pane: the MR/PR title plus a meta line (branches · author).
	meta := m.detail.HeadRef + " → " + m.detail.BaseRef
	if m.detail.Author != "" {
		meta += "  ·  " + m.detail.Author
	}
	titlePane := renderPane(
		fmt.Sprintf("#%d %s", m.detail.Number, m.detail.Title),
		[]string{styles.Desc.Render(meta)},
		width, titleH, false)

	// Description pane: wrapped message body, scrollable.
	descLines := m.descriptionLines(lw - 4)
	m.descTotal = len(descLines)
	m.descH = descH - 3 // pane chrome (border 2) + pane title 1
	if m.descH < 1 {
		m.descH = 1
	}
	m.clampDesc()
	descPane := renderPane(m.descTitle(), windowSlice(descLines, m.descScroll, m.descH),
		lw, descH, m.focus == focusDesc)

	// Pipeline pane: colorized status.
	status := m.detail.ChecksSummary
	if status == "" {
		status = "none"
	}
	pipePane := renderPane("Pipeline", []string{pipelineStyle(status).Render(status)}, lw, pipeH, false)

	// Diff pane: colorized, scrollable.
	m.detailDiffH = region - 3 // pane chrome (border 2) + pane title 1
	if m.detailDiffH < 1 {
		m.detailDiffH = 1
	}
	m.clampScroll()
	diffPane := renderPane(m.diffTitle(), windowSlice(m.diffLines, m.detailScroll, m.detailDiffH),
		rw, region, m.focus == focusDiff)

	left := lipgloss.JoinVertical(lipgloss.Left, descPane, pipePane)
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, diffPane)
	return lipgloss.JoinVertical(lipgloss.Left, titlePane, row, hint)
}

// diffTitle labels the diff pane with the current scroll position.
func (m *Model) diffTitle() string {
	if len(m.diffLines) == 0 {
		return "Diff"
	}
	end := m.detailScroll + m.detailDiffH
	if end > len(m.diffLines) {
		end = len(m.diffLines)
	}
	return fmt.Sprintf("Diff  (%d-%d/%d)", m.detailScroll+1, end, len(m.diffLines))
}

// descTitle labels the description pane, adding a scroll position when the body
// overflows the pane.
func (m *Model) descTitle() string {
	if m.descTotal <= m.descH || m.descTotal == 0 {
		return "Description"
	}
	end := m.descScroll + m.descH
	if end > m.descTotal {
		end = m.descTotal
	}
	return fmt.Sprintf("Description  (%d-%d/%d)", m.descScroll+1, end, m.descTotal)
}

// descriptionLines wraps the request body to width w.
func (m *Model) descriptionLines(w int) []string {
	body := strings.TrimSpace(m.detail.Body)
	if body == "" {
		return []string{styles.Desc.Render("(no description)")}
	}
	if w < 1 {
		w = 1
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(body)
	return strings.Split(wrapped, "\n")
}

// centered renders content in a single overlay box, centered in the area (used
// for the detail loading / error states).
func (m *Model) centered(width, height int, content string) string {
	box := styles.Overlay.Render(content)
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// renderPane draws a bordered box of outer size w×h: a styled pane title on the
// first line, then content clipped to the inner area. A focused pane gets a
// highlighted border.
func renderPane(title string, content []string, w, h int, focused bool) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	innerW := w - 4 // border (2) + padding (2)
	bodyH := h - 2  // border rows
	lines := make([]string, 0, bodyH)
	lines = append(lines, styles.OverlayTitle.Render(clip(title, innerW)))
	for i := 0; i < bodyH-1; i++ {
		if i < len(content) {
			lines = append(lines, clip(content[i], innerW))
		} else {
			lines = append(lines, "")
		}
	}
	border := styles.Divider.GetForeground()
	if focused {
		border = styles.Branch.GetForeground()
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w - 2).
		Render(strings.Join(lines, "\n"))
}

// windowSlice returns the n lines of lines starting at start (clamped).
func windowSlice(lines []string, start, n int) []string {
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + n
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end]
}

// buildDiffLines splits and colorizes a unified diff.
func buildDiffLines(diff string) []string {
	if strings.TrimSpace(diff) == "" {
		return []string{styles.Desc.Render("(no diff)")}
	}
	raw := strings.Split(diff, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, styleDiffLine(strings.TrimRight(l, "\r")))
	}
	return out
}

// pipelineStyle picks a color for a pipeline/checks status string.
func pipelineStyle(status string) lipgloss.Style {
	switch {
	case strings.Contains(status, "success"), strings.Contains(status, "passed"):
		return styles.Added
	case strings.Contains(status, "failed"), strings.Contains(status, "canceled"), strings.Contains(status, "cancelled"):
		return styles.Removed
	default:
		return styles.Desc
	}
}

// styleDiffLine colorizes one diff line by its leading marker.
func styleDiffLine(l string) string {
	switch {
	case strings.HasPrefix(l, "@@"):
		return styles.Hunk.Render(l)
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		return styles.DiffMeta.Render(l)
	case strings.HasPrefix(l, "+"):
		return styles.Added.Render(l)
	case strings.HasPrefix(l, "-"):
		return styles.Removed.Render(l)
	default:
		return styles.Context.Render(l)
	}
}

func (m *Model) renderRow(idx int, pr github.PR) string {
	title := pr.Title
	if pr.Draft {
		title = "[draft] " + title
	}
	if idx == m.cursor {
		return styles.SelectedRow.Render(fmt.Sprintf("#%d %s", pr.Number, title))
	}
	meta := ""
	if pr.Author != "" {
		meta = "  " + pr.Author
	}
	return styles.Key.Render(fmt.Sprintf("#%d ", pr.Number)) + title + styles.Desc.Render(meta)
}

// clip truncates s (ANSI-aware) to at most w display columns.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}

// windowLines returns at most budget lines that always include the focus line,
// marking truncated ends with a "more" indicator. When everything fits it
// returns lines unchanged.
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

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
