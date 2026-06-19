package styles

import "github.com/charmbracelet/lipgloss"

// Palette is the single source of truth for every color the UI draws. A theme is
// a fully-populated Palette; Apply rebuilds the package-level lipgloss style vars
// from it so all views (diff/list/branch/help/app) pick up the active colors by
// referencing styles.X without threading a theme object through render calls.
//
// Every field MUST be set by every preset — TestPresetsAreComplete asserts this.
type Palette struct {
	// Name is the preset's stable identifier (persisted in config).
	Name string

	// Header.
	Branch    string // branch name in the header
	HeaderTxt string // primary header text
	Muted     string // secondary / dimmed text (remote, path, separators)

	// File-list group headers.
	GroupStaged    string // "Staged" header
	GroupUnstaged  string // "Unstaged" header
	GroupUntracked string // "Untracked" header
	// Background tints for the Unstaged / Untracked group headers, rendered as
	// full-width banners so the three groups are unmistakable at a glance.
	GroupUnstagedBg  string
	GroupUntrackedBg string

	// Status glyphs, colorized by change type.
	GlyphAdded     string // A
	GlyphModified  string // M
	GlyphDeleted   string // D
	GlyphRenamed   string // R
	GlyphCopied    string // C
	GlyphUntracked string // ?
	GlyphUnmerged  string // U

	// Selection.
	SelectedBg     string // background of the selected row (list focused)
	SelectedFg     string // foreground/text of the selected row (kept legible)
	SelectedDimBg  string // background of the selected row when the diff is focused
	UntrackedRowFg string // tint for whole untracked rows so they read distinctly

	// Diff pane.
	DiffAdd     string // added line tint
	DiffRemove  string // removed line tint
	DiffContext string // context (unchanged) line — dimmed
	DiffHunk    string // compact hunk header label
	DiffCursor  string // background of the diff line cursor
	DiffMeta    string // any retained plumbing / raw-mode metadata

	// Overlays & chrome.
	OverlayBorder string // rounded overlay border
	OverlayTitle  string // overlay title text
	ConfirmBorder string // destructive confirm dialog border
	Error         string // inline errors + error toast
	KeyCap        string // key caps in the help overlay
	Footer        string // footer hint text
	Divider       string // the vertical bar between list and diff
}

func col(s string) lipgloss.Color { return lipgloss.Color(s) }

// presets holds every curated theme keyed by Name. Order is preserved separately
// in presetOrder so the picker lists them deterministically.
var presets = map[string]Palette{}
var presetOrder []string

func register(p Palette) {
	if _, ok := presets[p.Name]; !ok {
		presetOrder = append(presetOrder, p.Name)
	}
	presets[p.Name] = p
}

// DefaultTheme is applied when config has no stored preference.
const DefaultTheme = "tokyonight"

func init() {
	register(tokyoNight())
	register(catppuccin())
	register(gruvbox())
	register(nord())
	register(solarized())
	register(mono())
	// Apply the default so package vars are non-nil before any SetTheme call.
	Apply(presets[DefaultTheme])
}

// ThemeNames returns the preset names in display order.
func ThemeNames() []string {
	out := make([]string, len(presetOrder))
	copy(out, presetOrder)
	return out
}

// PaletteFor returns the named preset and whether it exists.
func PaletteFor(name string) (Palette, bool) {
	p, ok := presets[name]
	return p, ok
}

// activeTheme tracks the currently applied preset name.
var activeTheme = DefaultTheme

// ActiveTheme returns the name of the currently applied theme.
func ActiveTheme() string { return activeTheme }

// SetTheme applies the named preset (falling back to the default for an unknown
// name) and reports the name actually applied. Rebuilds all package style vars.
func SetTheme(name string) string {
	p, ok := presets[name]
	if !ok {
		p = presets[DefaultTheme]
		name = DefaultTheme
	}
	Apply(p)
	activeTheme = name
	return name
}

// ---- presets ----

func tokyoNight() Palette {
	return Palette{
		Name:           "tokyonight",
		Branch:         "#7AA2F7",
		HeaderTxt:      "#C0CAF5",
		Muted:          "#737AA2",
		GroupStaged:      "#9ECE6A",
		GroupUnstaged:    "#E0AF68",
		GroupUntracked:   "#7DCFFF",
		GroupUnstagedBg:  "#33291A",
		GroupUntrackedBg: "#16293A",
		GlyphAdded:     "#9ECE6A",
		GlyphModified:  "#E0AF68",
		GlyphDeleted:   "#F7768E",
		GlyphRenamed:   "#7DCFFF",
		GlyphCopied:    "#7DCFFF",
		GlyphUntracked: "#7AA2F7",
		GlyphUnmerged:  "#FF9E64",
		SelectedBg:     "#283457",
		SelectedFg:     "#C0CAF5",
		SelectedDimBg:  "#1F2335",
		UntrackedRowFg: "#7DCFFF",
		DiffAdd:        "#9ECE6A",
		DiffRemove:     "#F7768E",
		DiffContext:    "#9099AB",
		DiffHunk:       "#7DCFFF",
		DiffCursor:     "#3D59A1",
		DiffMeta:       "#565F89",
		OverlayBorder:  "#3B4261",
		OverlayTitle:   "#BB9AF7",
		ConfirmBorder:  "#DB4B4B",
		Error:          "#DB4B4B",
		KeyCap:         "#7AA2F7",
		Footer:         "#737AA2",
		Divider:        "#3B4261",
	}
}

func catppuccin() Palette {
	return Palette{
		Name:           "catppuccin",
		Branch:         "#89B4FA",
		HeaderTxt:      "#CDD6F4",
		Muted:          "#7F849C",
		GroupStaged:      "#A6E3A1",
		GroupUnstaged:    "#F9E2AF",
		GroupUntracked:   "#89DCEB",
		GroupUnstagedBg:  "#3A3320",
		GroupUntrackedBg: "#16323A",
		GlyphAdded:     "#A6E3A1",
		GlyphModified:  "#F9E2AF",
		GlyphDeleted:   "#F38BA8",
		GlyphRenamed:   "#89DCEB",
		GlyphCopied:    "#89DCEB",
		GlyphUntracked: "#89B4FA",
		GlyphUnmerged:  "#FAB387",
		SelectedBg:     "#313244",
		SelectedFg:     "#CDD6F4",
		SelectedDimBg:  "#1E1E2E",
		UntrackedRowFg: "#89DCEB",
		DiffAdd:        "#A6E3A1",
		DiffRemove:     "#F38BA8",
		DiffContext:    "#9399B2",
		DiffHunk:       "#89DCEB",
		DiffCursor:     "#45475A",
		DiffMeta:       "#6C7086",
		OverlayBorder:  "#45475A",
		OverlayTitle:   "#CBA6F7",
		ConfirmBorder:  "#F38BA8",
		Error:          "#F38BA8",
		KeyCap:         "#89B4FA",
		Footer:         "#7F849C",
		Divider:        "#45475A",
	}
}

func gruvbox() Palette {
	return Palette{
		Name:           "gruvbox",
		Branch:         "#83A598",
		HeaderTxt:      "#EBDBB2",
		Muted:          "#928374",
		GroupStaged:      "#B8BB26",
		GroupUnstaged:    "#FABD2F",
		GroupUntracked:   "#8EC07C",
		GroupUnstagedBg:  "#3D3018",
		GroupUntrackedBg: "#1F3328",
		GlyphAdded:     "#B8BB26",
		GlyphModified:  "#FABD2F",
		GlyphDeleted:   "#FB4934",
		GlyphRenamed:   "#8EC07C",
		GlyphCopied:    "#8EC07C",
		GlyphUntracked: "#83A598",
		GlyphUnmerged:  "#FE8019",
		SelectedBg:     "#3C3836",
		SelectedFg:     "#FBF1C7",
		SelectedDimBg:  "#282828",
		UntrackedRowFg: "#8EC07C",
		DiffAdd:        "#B8BB26",
		DiffRemove:     "#FB4934",
		DiffContext:    "#A89984",
		DiffHunk:       "#8EC07C",
		DiffCursor:     "#504945",
		DiffMeta:       "#7C6F64",
		OverlayBorder:  "#504945",
		OverlayTitle:   "#D3869B",
		ConfirmBorder:  "#FB4934",
		Error:          "#FB4934",
		KeyCap:         "#83A598",
		Footer:         "#928374",
		Divider:        "#504945",
	}
}

func nord() Palette {
	return Palette{
		Name:           "nord",
		Branch:         "#81A1C1",
		HeaderTxt:      "#ECEFF4",
		Muted:          "#616E88",
		GroupStaged:      "#A3BE8C",
		GroupUnstaged:    "#EBCB8B",
		GroupUntracked:   "#88C0D0",
		GroupUnstagedBg:  "#3A3320",
		GroupUntrackedBg: "#22353D",
		GlyphAdded:     "#A3BE8C",
		GlyphModified:  "#EBCB8B",
		GlyphDeleted:   "#BF616A",
		GlyphRenamed:   "#88C0D0",
		GlyphCopied:    "#88C0D0",
		GlyphUntracked: "#81A1C1",
		GlyphUnmerged:  "#D08770",
		SelectedBg:     "#3B4252",
		SelectedFg:     "#ECEFF4",
		SelectedDimBg:  "#2E3440",
		UntrackedRowFg: "#88C0D0",
		DiffAdd:        "#A3BE8C",
		DiffRemove:     "#BF616A",
		DiffContext:    "#9aa3b5",
		DiffHunk:       "#88C0D0",
		DiffCursor:     "#434C5E",
		DiffMeta:       "#4C566A",
		OverlayBorder:  "#434C5E",
		OverlayTitle:   "#B48EAD",
		ConfirmBorder:  "#BF616A",
		Error:          "#BF616A",
		KeyCap:         "#81A1C1",
		Footer:         "#616E88",
		Divider:        "#434C5E",
	}
}

func solarized() Palette {
	return Palette{
		Name:           "solarized",
		Branch:         "#268BD2",
		HeaderTxt:      "#93A1A1",
		Muted:          "#657B83",
		GroupStaged:      "#859900",
		GroupUnstaged:    "#B58900",
		GroupUntracked:   "#2AA198",
		GroupUnstagedBg:  "#33290A",
		GroupUntrackedBg: "#06332E",
		GlyphAdded:     "#859900",
		GlyphModified:  "#B58900",
		GlyphDeleted:   "#DC322F",
		GlyphRenamed:   "#2AA198",
		GlyphCopied:    "#2AA198",
		GlyphUntracked: "#268BD2",
		GlyphUnmerged:  "#CB4B16",
		SelectedBg:     "#073642",
		SelectedFg:     "#FDF6E3",
		SelectedDimBg:  "#002B36",
		UntrackedRowFg: "#2AA198",
		DiffAdd:        "#859900",
		DiffRemove:     "#DC322F",
		DiffContext:    "#839496",
		DiffHunk:       "#2AA198",
		DiffCursor:     "#094B5A",
		DiffMeta:       "#586E75",
		OverlayBorder:  "#586E75",
		OverlayTitle:   "#6C71C4",
		ConfirmBorder:  "#DC322F",
		Error:          "#DC322F",
		KeyCap:         "#268BD2",
		Footer:         "#657B83",
		Divider:        "#586E75",
	}
}

// mono is the high-contrast fallback: grayscale chrome with a few unmissable
// accents, so it stays legible on 16-color and monochrome-ish terminals.
func mono() Palette {
	return Palette{
		Name:           "mono",
		Branch:         "#FFFFFF",
		HeaderTxt:      "#FFFFFF",
		Muted:          "#AAAAAA",
		GroupStaged:      "#FFFFFF",
		GroupUnstaged:    "#FFFFFF",
		GroupUntracked:   "#FFFFFF",
		GroupUnstagedBg:  "#3A3A3A",
		GroupUntrackedBg: "#2A2A2A",
		GlyphAdded:     "#00FF00",
		GlyphModified:  "#FFFF00",
		GlyphDeleted:   "#FF0000",
		GlyphRenamed:   "#00FFFF",
		GlyphCopied:    "#00FFFF",
		GlyphUntracked: "#00AFFF",
		GlyphUnmerged:  "#FF8700",
		SelectedBg:     "#FFFFFF",
		SelectedFg:     "#000000",
		SelectedDimBg:  "#444444",
		UntrackedRowFg: "#00AFFF",
		DiffAdd:        "#00FF00",
		DiffRemove:     "#FF0000",
		DiffContext:    "#AAAAAA",
		DiffHunk:       "#00FFFF",
		DiffCursor:     "#5F5F5F",
		DiffMeta:       "#888888",
		OverlayBorder:  "#FFFFFF",
		OverlayTitle:   "#FFFFFF",
		ConfirmBorder:  "#FF0000",
		Error:          "#FF0000",
		KeyCap:         "#FFFFFF",
		Footer:         "#AAAAAA",
		Divider:        "#888888",
	}
}
