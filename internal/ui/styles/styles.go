// Package styles centralizes the shared lipgloss styles used across the gui
// UI. The styles are NOT hardcoded: they are rebuilt from the active Palette by
// Apply (see palette.go). Views reference the package-level style vars below and
// automatically pick up the active theme's colors. lipgloss downsamples truecolor
// palette values to the terminal's color profile (256/16-color) automatically.
package styles

import "github.com/charmbracelet/lipgloss"

// Header pieces.
var (
	// Header is the top line container.
	Header lipgloss.Style
	// Branch styles the branch name in the header.
	Branch lipgloss.Style
	// HeaderMuted styles secondary header segments (remote, path).
	HeaderMuted lipgloss.Style
	// HeaderSep styles the " · " separators.
	HeaderSep lipgloss.Style
)

// Diff list / pane.
var (
	// GroupHeader styles a generic group label (legacy; per-group variants below
	// are preferred for the file list).
	GroupHeader lipgloss.Style
	// GroupStaged / GroupUnstaged / GroupUntracked style their respective file
	// group headers with distinct colors so they're unmistakable at a glance.
	GroupStaged    lipgloss.Style
	GroupUnstaged  lipgloss.Style
	GroupUntracked lipgloss.Style
	// GroupBadge styles the "(N)" count badge next to a group header.
	GroupBadge lipgloss.Style

	// Row is a normal (unselected) file row.
	Row lipgloss.Style
	// UntrackedRow tints whole untracked rows so they read distinctly from
	// modified files even before the glyph is noticed.
	UntrackedRow lipgloss.Style
	// Folder styles directory nodes in the file tree (subordinate to file rows).
	Folder lipgloss.Style
	// SelectedRow highlights the row under the cursor.
	SelectedRow lipgloss.Style
	// SelectedRowInactive highlights the file row when focus is on the diff pane
	// (dimmer than SelectedRow so it's clear j/k move within the diff).
	SelectedRowInactive lipgloss.Style
	// HoverRow highlights the file row currently under the mouse pointer.
	HoverRow lipgloss.Style
	// DiffCursor highlights the current line under the diff line cursor.
	DiffCursor lipgloss.Style
	// Glyph styles the leading status letter (color applied per change type).
	Glyph lipgloss.Style
	// Clean styles the "nothing to commit" message.
	Clean lipgloss.Style

	// Added styles a `+` diff line.
	Added lipgloss.Style
	// Removed styles a `-` diff line.
	Removed lipgloss.Style
	// Context styles an unchanged diff line (dimmed so changes pop).
	Context lipgloss.Style
	// Hunk styles a `@@`/compact hunk header line.
	Hunk lipgloss.Style
	// DiffMeta styles diff metadata lines (diff/index/+++/--- in raw mode).
	DiffMeta lipgloss.Style

	// Divider styles the vertical bar between the list and diff panes.
	Divider lipgloss.Style
	// ScrollThumb is the filled part of the diff scrollbar (the current window).
	ScrollThumb lipgloss.Style
	// ScrollTrack is the unfilled part of the diff scrollbar.
	ScrollTrack lipgloss.Style
)

// Footer / toast.
var (
	// Hint styles the footer help hint line.
	Hint lipgloss.Style
	// Toast styles a transient error/status message.
	Toast lipgloss.Style
	// Version styles the build-version badge in the footer's bottom-left.
	Version lipgloss.Style
)

// Overlays.
var (
	// Overlay is the bordered container for modal panels (branch/help/theme).
	Overlay lipgloss.Style
	// OverlayTitle styles an overlay's title line.
	OverlayTitle lipgloss.Style
	// Confirm is the bordered container for confirmation dialogs.
	Confirm lipgloss.Style
	// Inline styles an inline error message inside an overlay.
	Inline lipgloss.Style
	// Key styles a key cap in the help overlay.
	Key lipgloss.Style
	// Desc styles a binding description in the help overlay.
	Desc lipgloss.Style
)

// glyphStyles maps a status glyph letter to its colorized style. Rebuilt by
// Apply. Use GlyphStyle to look one up.
var glyphStyles map[string]lipgloss.Style

// GlyphStyle returns the colorized style for a single status glyph letter
// ("A"/"M"/"D"/"R"/"C"/"?"/"U"), falling back to a plain bold style.
func GlyphStyle(g string) lipgloss.Style {
	if s, ok := glyphStyles[g]; ok {
		return s
	}
	return Glyph
}

// Apply rebuilds every package-level style var from p. Calling it swaps the
// whole UI's color scheme; views need no changes because they reference these
// vars by name.
func Apply(p Palette) {
	Header = lipgloss.NewStyle().Padding(0, 1)
	Branch = lipgloss.NewStyle().Foreground(col(p.Branch)).Bold(true)
	HeaderMuted = lipgloss.NewStyle().Foreground(col(p.Muted))
	HeaderSep = lipgloss.NewStyle().Foreground(col(p.Muted))

	GroupHeader = lipgloss.NewStyle().Foreground(col(p.OverlayTitle)).Bold(true)
	GroupStaged = lipgloss.NewStyle().Foreground(col(p.GroupStaged)).Bold(true)
	GroupUnstaged = lipgloss.NewStyle().Foreground(col(p.GroupUnstaged)).Background(col(p.GroupUnstagedBg)).Bold(true)
	GroupUntracked = lipgloss.NewStyle().Foreground(col(p.GroupUntracked)).Background(col(p.GroupUntrackedBg)).Bold(true)
	GroupBadge = lipgloss.NewStyle().Foreground(col(p.Muted))

	Row = lipgloss.NewStyle()
	UntrackedRow = lipgloss.NewStyle().Foreground(col(p.UntrackedRowFg))
	Folder = lipgloss.NewStyle().Foreground(col(p.Muted)).Bold(true)
	SelectedRow = lipgloss.NewStyle().Background(col(p.SelectedBg)).Foreground(col(p.SelectedFg)).Bold(true)
	SelectedRowInactive = lipgloss.NewStyle().Background(col(p.SelectedDimBg))
	HoverRow = lipgloss.NewStyle().Background(col(p.HoverBg))
	DiffCursor = lipgloss.NewStyle().Background(col(p.DiffCursor))
	Glyph = lipgloss.NewStyle().Bold(true)
	Clean = lipgloss.NewStyle().Foreground(col(p.Muted)).Italic(true)

	Added = lipgloss.NewStyle().Foreground(col(p.DiffAdd))
	Removed = lipgloss.NewStyle().Foreground(col(p.DiffRemove))
	Context = lipgloss.NewStyle().Foreground(col(p.DiffContext))
	Hunk = lipgloss.NewStyle().Foreground(col(p.DiffHunk)).Bold(true)
	DiffMeta = lipgloss.NewStyle().Foreground(col(p.DiffMeta))

	Divider = lipgloss.NewStyle().Foreground(col(p.Divider))
	ScrollThumb = lipgloss.NewStyle().Foreground(col(p.OverlayTitle))
	ScrollTrack = lipgloss.NewStyle().Foreground(col(p.Divider))

	Hint = lipgloss.NewStyle().Foreground(col(p.Footer))
	Toast = lipgloss.NewStyle().Foreground(col(p.Error)).Bold(true)
	Version = lipgloss.NewStyle().Foreground(col(p.Branch)).Bold(true)

	Overlay = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(col(p.OverlayBorder)).
		Padding(1, 2)
	OverlayTitle = lipgloss.NewStyle().Foreground(col(p.OverlayTitle)).Bold(true)
	Confirm = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(col(p.ConfirmBorder)).
		Padding(1, 2)
	Inline = lipgloss.NewStyle().Foreground(col(p.Error))
	Key = lipgloss.NewStyle().Foreground(col(p.KeyCap)).Bold(true)
	Desc = lipgloss.NewStyle().Foreground(col(p.Muted))

	glyphStyles = map[string]lipgloss.Style{
		"A": lipgloss.NewStyle().Foreground(col(p.GlyphAdded)).Bold(true),
		"M": lipgloss.NewStyle().Foreground(col(p.GlyphModified)).Bold(true),
		"D": lipgloss.NewStyle().Foreground(col(p.GlyphDeleted)).Bold(true),
		"R": lipgloss.NewStyle().Foreground(col(p.GlyphRenamed)).Bold(true),
		"C": lipgloss.NewStyle().Foreground(col(p.GlyphCopied)).Bold(true),
		"?": lipgloss.NewStyle().Foreground(col(p.GlyphUntracked)).Bold(true),
		"U": lipgloss.NewStyle().Foreground(col(p.GlyphUnmerged)).Bold(true),
	}
}
