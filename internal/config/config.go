// Package config holds the keymap, leader chord dispatcher, and persisted
// preferences. See docs/specs/03-config-keymap.md. No UI rendering deps.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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
	// Appended in checklist 03 (UI polish: themes + clean diff). Keep at the end.
	ActThemePicker   // T (shift+t): open the theme picker overlay (live preview)
	ActToggleRawDiff // ctrl+g: toggle showing the raw (unfiltered) diff
	// Appended for the file-tree UX. Keep at the end.
	ActCollapse    // h: collapse the folder under the cursor / go to parent
	ActExpand      // l: expand the folder under the cursor / step in
	ActToggleTree  // .: toggle folder-tree vs flat file list
	ActFocusToggle // tab: move focus between the file tree and the diff contents
	ActHideTree    // E (shift+e): hide/show the file-tree pane (diff full width)
	// Appended for the commit section. Keep at the end.
	ActCommit     // C (shift+c): open the commit-message dialog and commit staged changes
	ActStageAll   // a: stage all unstaged + untracked files (git add -A)
	ActUnstageAll // A (shift+a): unstage all staged files (git restore --staged .)
	ActPush       // p: push the current branch to its upstream (git push)
	// Appended for the command palette. Keep at the end.
	ActCommandPalette // ctrl+p: open the fuzzy command palette (search all commands)
	// Appended for git blame. Keep at the end.
	ActBlameLine // b: show git blame for the line under the diff cursor
)

// actionNames maps each Action to its stable config name (used in config.yaml
// `keys:` values). It is the single source of truth for action ↔ name; the
// reverse map actionByName is derived from it. "none" is reserved (ActNone) to
// let a config clear a default binding.
var actionNames = map[Action]string{
	ActNone:              "none",
	ActQuit:              "quit",
	ActRefresh:           "refresh",
	ActHelp:              "help",
	ActDown:              "down",
	ActUp:                "up",
	ActConfirm:           "confirm",
	ActUndo:              "discard_hunk",
	ActStageToggle:       "stage_toggle",
	ActBranchPanel:       "branch_panel",
	ActPRList:            "pr_list",
	ActCancel:            "cancel",
	ActUndoFile:          "discard_file",
	ActRecover:           "recover",
	ActPaneGrow:          "pane_grow",
	ActPaneShrink:        "pane_shrink",
	ActHunkNext:          "hunk_next",
	ActHunkPrev:          "hunk_prev",
	ActToggleAutoRefresh: "toggle_auto_refresh",
	ActThemePicker:       "theme_picker",
	ActToggleRawDiff:     "toggle_raw_diff",
	ActCollapse:          "collapse",
	ActExpand:            "expand",
	ActToggleTree:        "toggle_tree",
	ActFocusToggle:       "focus_toggle",
	ActHideTree:          "hide_tree",
	ActCommit:            "commit",
	ActStageAll:          "stage_all",
	ActUnstageAll:        "unstage_all",
	ActPush:              "push",
	ActCommandPalette:    "command_palette",
	ActBlameLine:         "blame_line",
}

// actionByName is the reverse of actionNames, built once at package load via an IIFE.
var actionByName = func() map[string]Action {
	m := make(map[string]Action, len(actionNames))
	for a, n := range actionNames {
		m[n] = a
	}
	return m
}()

// Keymap maps keys/chords to actions. Leader is the chord prefix.
type Keymap struct {
	Leader string
	// direct holds non-chord key -> action bindings.
	direct map[string]Action
	// chords holds the second key (after the leader) -> action bindings.
	chords map[string]Action
}

// DefaultKeymap returns the documented default bindings. Every action is a
// direct key; shift+letter (e.g. B/T/P) replaces the former leader chords, so
// there is no leader prefix to remember.
func DefaultKeymap() Keymap {
	return Keymap{
		Leader: "",
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
			"a":      ActStageAll,
			"A":      ActUnstageAll,
			"u":      ActUndo,
			"U":      ActUndoFile,
			"ctrl+r": ActRecover,
			">":      ActPaneGrow,
			"<":      ActPaneShrink,
			"}":      ActHunkNext,
			"{":      ActHunkPrev,
			"ctrl+t": ActToggleAutoRefresh,
			"ctrl+g": ActToggleRawDiff,
			"h":      ActCollapse,
			"left":   ActCollapse,
			"l":      ActExpand,
			"right":  ActExpand,
			".":      ActToggleTree,
			"tab":    ActFocusToggle,
			"E":      ActHideTree,
			"C":      ActCommit,
			"B":      ActBranchPanel,
			"T":      ActThemePicker,
			"P":      ActPRList,
			"p":      ActPush,
			"b":      ActBlameLine,
			"ctrl+p": ActCommandPalette,
			"esc":    ActCancel,
		},
		// No leader chords: every action is a direct key (see DefaultKeymap doc).
		chords: map[string]Action{},
	}
}

// bindingOrder is the static display order and description for each action in
// the help overlay / command palette. The actual keys are filled in from the
// live keymap by Bindings(), so remapped keys show correctly.
var bindingOrder = []struct {
	Action Action
	Desc   string
}{
	{ActDown, "Move down (by line within a diff)"},
	{ActUp, "Move up (by line within a diff)"},
	{ActConfirm, "Open / confirm"},
	{ActStageToggle, "Stage / unstage selected file"},
	{ActStageAll, "Stage all changed + untracked files"},
	{ActUnstageAll, "Unstage all staged files"},
	{ActUndo, "Discard the change under the cursor (hunk)"},
	{ActUndoFile, "Discard the whole file (confirm)"},
	{ActRecover, "Recover the last discarded change"},
	{ActHunkNext, "Next hunk"},
	{ActHunkPrev, "Previous hunk"},
	{ActBlameLine, "Blame the line under the cursor"},
	{ActCollapse, "Collapse folder / go to parent"},
	{ActExpand, "Expand folder / step in"},
	{ActToggleTree, "Toggle folder tree / flat list"},
	{ActFocusToggle, "Move focus: file tree ↔ diff contents"},
	{ActHideTree, "Hide / show the file-tree pane"},
	{ActCommit, "Commit staged changes (message dialog)"},
	{ActPaneGrow, "Grow the diff pane"},
	{ActPaneShrink, "Shrink the diff pane"},
	{ActRefresh, "Refresh status"},
	{ActToggleAutoRefresh, "Toggle auto-refresh on/off"},
	{ActToggleRawDiff, "Toggle raw / cleaned diff view"},
	{ActPush, "Push the current branch (git push)"},
	{ActCommandPalette, "Open the command palette (fuzzy search)"},
	{ActBranchPanel, "Open branch panel"},
	{ActThemePicker, "Open theme picker (live preview)"},
	{ActPRList, "Open merge/pull request list"},
	{ActHelp, "Toggle help overlay"},
	{ActCancel, "Cancel / close overlay"},
	{ActQuit, "Quit"},
}

// Bindings returns the documented keybindings for the help overlay, in display
// order, with each binding's keys taken from this keymap (so custom configs are
// reflected). Chords are rendered as "<leader> <key>".
func (k Keymap) Bindings() []Binding {
	byAction := map[Action][]string{}
	for key, act := range k.direct {
		byAction[act] = append(byAction[act], key)
	}
	for key, act := range k.chords {
		disp := key
		if k.Leader != "" {
			disp = k.Leader + " " + key
		}
		byAction[act] = append(byAction[act], disp)
	}
	for act := range byAction {
		sort.Strings(byAction[act])
	}
	out := make([]Binding, 0, len(bindingOrder))
	for _, bi := range bindingOrder {
		out = append(out, Binding{Keys: byAction[bi.Action], Action: bi.Action, Desc: bi.Desc})
	}
	return out
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

// Config is persisted user preference (YAML-primary; old JSON is read as a
// fallback). Keys overrides bindings on top of DefaultKeymap().
type Config struct {
	Leader     string            `json:"leader" yaml:"leader"`
	GitHubHost string            `json:"github_host" yaml:"github_host"`
	Theme      string            `json:"theme" yaml:"theme"`
	Keys       map[string]string `json:"keys,omitempty" yaml:"keys,omitempty"`
}

// normalizeConfigKey maps a config key string onto the names the keymap uses.
// Only the space key needs translating (" " → "space"); everything else is
// already in keymap form.
func normalizeConfigKey(s string) string {
	if s == " " {
		return "space"
	}
	return s
}

// Keymap builds the active keymap by merging the config's Keys over
// DefaultKeymap(). Each entry's value is an action name (see actionNames) or
// "none" to clear that key. A key written "<leader> k" registers a chord on k.
// The leader is left inert (empty) when no chords end up defined. Unknown
// action names and empty chord keys are skipped and reported in the returned
// warnings (never fatal).
func (c Config) Keymap() (Keymap, []string) {
	base := DefaultKeymap()
	km := Keymap{
		Leader: base.Leader,
		direct: make(map[string]Action, len(base.direct)),
		chords: make(map[string]Action, len(base.chords)),
	}
	for k, v := range base.direct {
		km.direct[k] = v
	}
	for k, v := range base.chords {
		km.chords[k] = v
	}
	if c.Leader != "" {
		km.Leader = normalizeConfigKey(c.Leader)
	}

	var warns []string
	for rawKey, name := range c.Keys {
		act, ok := actionByName[name]
		if !ok {
			warns = append(warns, fmt.Sprintf("config: unknown action %q for key %q (ignored)", name, rawKey))
			continue
		}
		key := normalizeConfigKey(strings.TrimSpace(rawKey))
		if strings.HasPrefix(key, "<leader>") {
			chordKey := strings.TrimSpace(key[len("<leader>"):])
			if chordKey == "" {
				warns = append(warns, fmt.Sprintf("config: empty leader chord for action %q (ignored)", name))
				continue
			}
			if act == ActNone {
				delete(km.chords, chordKey)
			} else {
				km.chords[chordKey] = act
			}
			continue
		}
		if act == ActNone {
			delete(km.direct, key)
		} else {
			km.direct[key] = act
		}
	}

	// The leader only matters if a chord exists; otherwise keep it inert so the
	// leader key behaves as a normal (unbound) key.
	if len(km.chords) == 0 {
		km.Leader = ""
	}
	return km, warns
}

// configPath returns os.UserConfigDir()/gui/config.json.
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gui", "config.json"), nil
}

// yamlConfigPath returns ~/.config/gui/config.yaml (literal ~/.config, the same
// on macOS and Linux). It deliberately does not use os.UserConfigDir.
func yamlConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gui", "config.yaml"), nil
}

// applyDefaults fills empty preference fields from def.
func applyDefaults(c *Config, def Config) {
	if c.Leader == "" {
		c.Leader = def.Leader
	}
	if c.Theme == "" {
		c.Theme = def.Theme
	}
}

// Load reads ~/.config/gui/config.yaml. When that file is absent it falls back
// to the old JSON config (os.UserConfigDir()/gui/config.json), and finally to
// built-in defaults. Recoverable problems (malformed YAML, unreadable file)
// produce warnings rather than errors; the caller surfaces them to the user.
func Load() (Config, []string, error) {
	def := Config{Leader: "space", Theme: "tokyonight"}
	var warns []string

	yp, err := yamlConfigPath()
	if err == nil {
		data, rerr := os.ReadFile(yp)
		switch {
		case rerr == nil:
			var c Config
			if uerr := yaml.Unmarshal(data, &c); uerr != nil {
				warns = append(warns, "config: invalid YAML in "+yp+", using defaults: "+uerr.Error())
				return loadJSONFallback(def, warns)
			}
			applyDefaults(&c, def)
			return c, warns, nil
		case errors.Is(rerr, fs.ErrNotExist):
			// fall through to JSON fallback
		default:
			warns = append(warns, "config: cannot read "+yp+": "+rerr.Error())
		}
	}
	return loadJSONFallback(def, warns)
}

// loadJSONFallback reads the legacy JSON config, returning defaults when it is
// absent. JSON is read-only: we never write it back.
func loadJSONFallback(def Config, warns []string) (Config, []string, error) {
	path, err := configPath()
	if err != nil {
		return def, warns, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return def, warns, nil
		}
		return def, warns, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		warns = append(warns, "config: invalid JSON in "+path+", using defaults: "+err.Error())
		return def, warns, nil
	}
	applyDefaults(&c, def)
	return c, warns, nil
}

// Save writes the config as YAML to ~/.config/gui/config.yaml, creating the
// directory if needed. It round-trips keys and leader so persisting the theme
// never drops the user's key overrides.
func (c Config) Save() error {
	path, err := yamlConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
