// Package listpane is a fixed-height, scrollable, single-select list block used
// for the bottom-left panels (pull requests, commits). It owns selection/scroll
// mechanics and rendering; callers supply pre-rendered row strings.
package listpane

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// Model is a titled list block. Construct with New; size it with SetSize.
type Model struct {
	title       string
	rows        []string
	placeholder string
	sel         int
	offset      int
	width       int
	height      int // includes the 1-row title
	focused     bool
}

// New builds a list block with a header title.
func New(title string) Model { return Model{title: title, sel: 0} }

// SetSize sets the block geometry; height includes the title row.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.clamp()
}

// SetFocused marks the block focused (brighter title, active selection style).
func (m *Model) SetFocused(b bool) { m.focused = b }

// SetPlaceholder sets the muted text shown when there are no rows.
func (m *Model) SetPlaceholder(s string) { m.placeholder = s }

// SetRows replaces the row strings and clamps the selection.
func (m *Model) SetRows(rows []string) {
	m.rows = rows
	m.clamp()
}

// RowCount returns the number of rows.
func (m *Model) RowCount() int { return len(m.rows) }

// Selected returns the selected index, or -1 when empty.
func (m *Model) Selected() int {
	if len(m.rows) == 0 {
		return -1
	}
	return m.sel
}

// visibleRows is the number of row lines (height minus the title row).
func (m *Model) visibleRows() int {
	if m.height <= 1 {
		return 0
	}
	return m.height - 1
}

// MoveSelection moves the selection by delta and keeps it within the window.
func (m *Model) MoveSelection(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.sel += delta
	m.clamp()
}

func (m *Model) clamp() {
	if len(m.rows) == 0 {
		m.sel, m.offset = 0, 0
		return
	}
	if m.sel < 0 {
		m.sel = 0
	}
	if m.sel > len(m.rows)-1 {
		m.sel = len(m.rows) - 1
	}
	vis := m.visibleRows()
	if vis <= 0 {
		m.offset = 0
		return
	}
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+vis {
		m.offset = m.sel - vis + 1
	}
	maxOffset := len(m.rows) - vis
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

// View renders exactly height lines: a title row then windowed rows, padded.
func (m *Model) View() string {
	titleStyle := styles.GroupHeader
	if !m.focused {
		titleStyle = styles.GroupBadge
	}
	lines := []string{clip(titleStyle.Render(m.title), m.width)}

	vis := m.visibleRows()
	if len(m.rows) == 0 {
		if m.placeholder != "" && vis > 0 {
			lines = append(lines, clip(styles.Desc.Render(m.placeholder), m.width))
		}
	} else {
		end := m.offset + vis
		if end > len(m.rows) {
			end = len(m.rows)
		}
		for i := m.offset; i < end; i++ {
			row := clip(m.rows[i], m.width)
			if i == m.sel {
				sel := styles.SelectedRow
				if !m.focused {
					sel = styles.SelectedRowInactive
				}
				row = sel.Width(m.width).Render(ansi.Strip(m.rows[i]))
				row = clip(row, m.width)
			}
			lines = append(lines, row)
		}
	}
	// Pad to exactly height lines so the column layout stays stable.
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return strings.Join(lines, "\n")
}

// clip truncates s to w display cells (ANSI-aware).
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "")
}
