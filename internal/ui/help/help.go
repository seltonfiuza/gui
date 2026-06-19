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

	box := styles.Overlay.Render(strings.Join(rows, "\n"))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
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
