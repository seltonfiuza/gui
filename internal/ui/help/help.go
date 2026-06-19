// Package help renders the FR-4 keymap overlay built from
// config.DefaultKeymap().Bindings().
package help

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/selton/gui/internal/config"
	"github.com/selton/gui/internal/ui/styles"
)

// Model is the help overlay.
type Model struct {
	bindings []config.Binding
}

// New builds the help overlay from the default keymap.
func New() Model {
	return Model{bindings: config.DefaultKeymap().Bindings()}
}

// View renders the bordered keymap listing centered in the given area.
func (m Model) View(width, height int) string {
	var rows []string
	rows = append(rows, styles.OverlayTitle.Render("Keybindings"), "")

	// Compute key-column width for alignment.
	keyWidth := 0
	keyCols := make([]string, len(m.bindings))
	for i, b := range m.bindings {
		keyCols[i] = strings.Join(b.Keys, " / ")
		if w := lipgloss.Width(keyCols[i]); w > keyWidth {
			keyWidth = w
		}
	}
	for i, b := range m.bindings {
		key := styles.Key.Render(padRight(keyCols[i], keyWidth))
		rows = append(rows, key+"  "+styles.Desc.Render(b.Desc))
	}
	rows = append(rows, "", styles.Desc.Render("press ? or esc to close"))

	// Cap the body so the bordered box (border+padding = 4 rows of chrome) never
	// exceeds the available height; drop overflow with a "… more" marker rather
	// than letting lipgloss.Place render the box off-screen.
	rows = capRows(rows, height-4)
	box := styles.Overlay.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// capRows truncates rows to at most max lines, replacing the last kept line with
// a "… more" marker when content was dropped. A non-positive max keeps one line.
func capRows(rows []string, max int) []string {
	if max < 1 {
		max = 1
	}
	if len(rows) <= max {
		return rows
	}
	out := make([]string, max)
	copy(out, rows[:max])
	out[max-1] = styles.Desc.Render("… more (resize to see all)")
	return out
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
