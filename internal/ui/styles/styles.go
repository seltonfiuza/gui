// Package styles centralizes the shared lipgloss styles used across the gui
// UI. Colors use lipgloss adaptive/truecolor values with a graceful 256-color
// fallback handled by lipgloss itself.
package styles

import "github.com/charmbracelet/lipgloss"

// Palette colors. lipgloss downsamples these to the terminal's color profile,
// so truecolor terminals get the exact hue and 256/16-color terminals get the
// nearest match automatically.
var (
	colorBranch  = lipgloss.Color("#7AA2F7") // blue
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6C6F85", Dark: "#9099AB"}
	colorAdded   = lipgloss.Color("#9ECE6A") // green
	colorRemoved = lipgloss.Color("#F7768E") // red
	colorHunk    = lipgloss.Color("#7DCFFF") // cyan
	colorError   = lipgloss.Color("#DB4B4B")
	colorSel     = lipgloss.Color("#283457")
	colorGroup   = lipgloss.Color("#BB9AF7") // purple
	colorBorder  = lipgloss.AdaptiveColor{Light: "#8087A2", Dark: "#3B4261"}
	colorHint    = lipgloss.AdaptiveColor{Light: "#6C6F85", Dark: "#737AA2"}
)

// Header pieces.
var (
	// Header is the top line container.
	Header = lipgloss.NewStyle().Padding(0, 1)
	// Branch styles the branch name in the header.
	Branch = lipgloss.NewStyle().Foreground(colorBranch).Bold(true)
	// HeaderMuted styles secondary header segments (remote, path).
	HeaderMuted = lipgloss.NewStyle().Foreground(colorMuted)
	// HeaderSep styles the " · " separators.
	HeaderSep = lipgloss.NewStyle().Foreground(colorMuted)
)

// Diff list / pane.
var (
	// GroupHeader styles the "Staged"/"Unstaged"/"Untracked" labels.
	GroupHeader = lipgloss.NewStyle().Foreground(colorGroup).Bold(true)
	// Row is a normal (unselected) file row.
	Row = lipgloss.NewStyle()
	// SelectedRow highlights the row under the cursor.
	SelectedRow = lipgloss.NewStyle().Background(colorSel).Bold(true)
	// Glyph styles the leading status letter.
	Glyph = lipgloss.NewStyle().Bold(true)
	// Clean styles the "nothing to commit" message.
	Clean = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	// Added styles a `+` diff line.
	Added = lipgloss.NewStyle().Foreground(colorAdded)
	// Removed styles a `-` diff line.
	Removed = lipgloss.NewStyle().Foreground(colorRemoved)
	// Hunk styles a `@@` diff line.
	Hunk = lipgloss.NewStyle().Foreground(colorHunk)
	// DiffMeta styles diff metadata lines (diff/index/+++/---).
	DiffMeta = lipgloss.NewStyle().Foreground(colorMuted)
)

// Footer / toast.
var (
	// Hint styles the footer help hint line.
	Hint = lipgloss.NewStyle().Foreground(colorHint)
	// Toast styles a transient error/status message.
	Toast = lipgloss.NewStyle().Foreground(colorError).Bold(true)
)

// Overlays.
var (
	// Overlay is the bordered container for modal panels (branch/help).
	Overlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2)
	// OverlayTitle styles an overlay's title line.
	OverlayTitle = lipgloss.NewStyle().Foreground(colorGroup).Bold(true)
	// Confirm is the bordered container for confirmation dialogs.
	Confirm = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorError).
		Padding(1, 2)
	// Inline styles an inline error message inside an overlay.
	Inline = lipgloss.NewStyle().Foreground(colorError)
	// Key styles a key cap in the help overlay.
	Key = lipgloss.NewStyle().Foreground(colorBranch).Bold(true)
	// Desc styles a binding description in the help overlay.
	Desc = lipgloss.NewStyle().Foreground(colorMuted)
)
