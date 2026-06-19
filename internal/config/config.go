// Package config holds the keymap, leader chord dispatcher, and persisted
// preferences. See docs/specs/03-config-keymap.md. No UI rendering deps.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Action is a resolved high-level user intent.
type Action int

const (
	ActNone Action = iota
	ActQuit
	ActRefresh
	ActHelp
	ActDown
	ActUp
	ActConfirm
	ActUndo
	ActStageToggle
	ActBranchPanel
	ActPRList
	ActCancel
	// Appended in checklist 01 (granular undo + resizable layout). Keep these at
	// the end so existing iota values are unchanged.
	ActUndoFile   // U: discard the whole file (with confirmation)
	ActRecover    // ctrl+r: restore the most recently discarded change
	ActPaneGrow   // >: grow the diff pane / shrink the file list
	ActPaneShrink // <: shrink the diff pane / grow the file list
	ActHunkNext   // }: jump to next hunk in the diff
	ActHunkPrev   // {: jump to previous hunk in the diff
	// Appended in checklist 02 (near real-time refresh). Keep at the end.
	ActToggleAutoRefresh // ctrl+t: turn the background auto-refresh tick on/off
)

// Keymap maps keys/chords to actions. Leader is the chord prefix.
type Keymap struct {
	Leader string
	// direct holds non-chord key -> action bindings.
	direct map[string]Action
	// chords holds the second key (after the leader) -> action bindings.
	chords map[string]Action
}

// DefaultKeymap returns the documented default bindings (leader = space).
func DefaultKeymap() Keymap {
	return Keymap{
		Leader: "space",
		direct: map[string]Action{
			"q":      ActQuit,
			"ctrl+c": ActQuit,
			"r":      ActRefresh,
			"?":      ActHelp,
			"j":      ActDown,
			"down":   ActDown,
			"k":      ActUp,
			"up":     ActUp,
			"enter":  ActConfirm,
			"s":      ActStageToggle,
			"u":      ActUndo,
			"U":      ActUndoFile,
			"ctrl+r": ActRecover,
			">":      ActPaneGrow,
			"<":      ActPaneShrink,
			"}":      ActHunkNext,
			"{":      ActHunkPrev,
			"ctrl+t": ActToggleAutoRefresh,
			"esc":    ActCancel,
		},
		chords: map[string]Action{
			"b": ActBranchPanel,
		},
	}
}

// Bindings returns the documented keybindings for the help overlay, in display
// order.
func (k Keymap) Bindings() []Binding {
	return []Binding{
		{Keys: []string{"j", "down"}, Action: ActDown, Desc: "Move down (by line within a diff)"},
		{Keys: []string{"k", "up"}, Action: ActUp, Desc: "Move up (by line within a diff)"},
		{Keys: []string{"enter"}, Action: ActConfirm, Desc: "Open / confirm"},
		{Keys: []string{"s"}, Action: ActStageToggle, Desc: "Stage / unstage selected file"},
		{Keys: []string{"u"}, Action: ActUndo, Desc: "Discard the change under the cursor (hunk)"},
		{Keys: []string{"U"}, Action: ActUndoFile, Desc: "Discard the whole file (confirm)"},
		{Keys: []string{"ctrl+r"}, Action: ActRecover, Desc: "Recover the last discarded change"},
		{Keys: []string{"}"}, Action: ActHunkNext, Desc: "Next hunk"},
		{Keys: []string{"{"}, Action: ActHunkPrev, Desc: "Previous hunk"},
		{Keys: []string{">"}, Action: ActPaneGrow, Desc: "Grow the diff pane"},
		{Keys: []string{"<"}, Action: ActPaneShrink, Desc: "Shrink the diff pane"},
		{Keys: []string{"r"}, Action: ActRefresh, Desc: "Refresh status"},
		{Keys: []string{"ctrl+t"}, Action: ActToggleAutoRefresh, Desc: "Toggle auto-refresh on/off"},
		{Keys: []string{"<leader> b"}, Action: ActBranchPanel, Desc: "Open branch panel"},
		{Keys: []string{"?"}, Action: ActHelp, Desc: "Toggle help overlay"},
		{Keys: []string{"esc"}, Action: ActCancel, Desc: "Cancel / close overlay"},
		{Keys: []string{"q", "ctrl+c"}, Action: ActQuit, Desc: "Quit"},
	}
}

// Binding is one documented keybinding for the help overlay.
type Binding struct {
	Keys   []string // e.g. {"j", "down"} or {"<leader> b"}
	Action Action
	Desc   string // human-readable, e.g. "Open branch panel"
}

// Dispatcher resolves keys to Actions, tracking leader-chord state.
type Dispatcher struct {
	keymap        Keymap
	leaderPending bool
}

// NewDispatcher builds a Dispatcher for keymap k.
func NewDispatcher(k Keymap) *Dispatcher { return &Dispatcher{keymap: k} }

// Resolve consumes one key (tea.KeyMsg.String()) and returns the Action.
func (d *Dispatcher) Resolve(key string) Action {
	if d.leaderPending {
		d.leaderPending = false
		if act, ok := d.keymap.chords[key]; ok {
			return act
		}
		return ActNone
	}
	if key == d.keymap.Leader {
		d.leaderPending = true
		return ActNone
	}
	if act, ok := d.keymap.direct[key]; ok {
		return act
	}
	return ActNone
}

// LeaderPending reports whether the leader was pressed and awaits a chord key.
func (d *Dispatcher) LeaderPending() bool { return d.leaderPending }

// Config is persisted user preference.
type Config struct {
	Leader     string `json:"leader"`
	GitHubHost string `json:"github_host"`
}

// configPath returns os.UserConfigDir()/gui/config.json.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gui", "config.json"), nil
}

// Load reads the config file, returning defaults when absent.
func Load() (Config, error) {
	def := Config{Leader: "space"}
	path, err := configPath()
	if err != nil {
		return def, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return def, nil
		}
		return def, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return def, err
	}
	return c, nil
}

// Save writes the config file.
func (c Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
