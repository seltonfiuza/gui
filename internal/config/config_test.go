package config

import (
	"os"
	"path/filepath"
	"testing"
)

// leaderKeymap builds a keymap that still uses the leader-chord mechanism so the
// dispatcher's chord handling stays covered even though DefaultKeymap no longer
// defines any chords (all actions are now direct shift+letter bindings).
func leaderKeymap() Keymap {
	return Keymap{
		Leader: "space",
		direct: map[string]Action{"q": ActQuit},
		chords: map[string]Action{"b": ActBranchPanel},
	}
}

func TestResolveLeaderChord(t *testing.T) {
	d := NewDispatcher(leaderKeymap())

	if got := d.Resolve("space"); got != ActNone {
		t.Fatalf("Resolve(space) = %v, want ActNone", got)
	}
	if !d.LeaderPending() {
		t.Fatalf("LeaderPending() = false after leader, want true")
	}
	if got := d.Resolve("b"); got != ActBranchPanel {
		t.Fatalf("Resolve(b) = %v, want ActBranchPanel", got)
	}
	if d.LeaderPending() {
		t.Fatalf("LeaderPending() = true after chord, want false")
	}
}

func TestResolveLeaderUnmappedKey(t *testing.T) {
	d := NewDispatcher(leaderKeymap())

	d.Resolve("space")
	if got := d.Resolve("z"); got != ActNone {
		t.Fatalf("Resolve(z) after leader = %v, want ActNone", got)
	}
	if d.LeaderPending() {
		t.Fatalf("LeaderPending() = true after unmapped chord key, want false")
	}
}

// TestDefaultKeymapHasNoLeader verifies the standardized keymap: shift+letter
// direct bindings replace the old space-leader chords, and space is inert.
func TestDefaultKeymapHasNoLeader(t *testing.T) {
	d := NewDispatcher(DefaultKeymap())
	if got := d.Resolve("space"); got != ActNone {
		t.Fatalf("Resolve(space) = %v, want ActNone", got)
	}
	if d.LeaderPending() {
		t.Fatal("space must not arm a leader in the default keymap")
	}
	for key, want := range map[string]Action{
		"B": ActBranchPanel,
		"T": ActThemePicker,
		"P": ActPRList,
	} {
		d := NewDispatcher(DefaultKeymap())
		if got := d.Resolve(key); got != want {
			t.Errorf("Resolve(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestResolveDirectBindings(t *testing.T) {
	cases := map[string]Action{
		"?":      ActHelp,
		"q":      ActQuit,
		"ctrl+c": ActQuit,
		"r":      ActRefresh,
		"j":      ActDown,
		"down":   ActDown,
		"k":      ActUp,
		"up":     ActUp,
		"enter":  ActConfirm,
		"s":      ActStageToggle,
		"p":      ActPush,
		"ctrl+p": ActCommandPalette,
		"u":      ActUndo,
		"U":      ActUndoFile,
		"ctrl+r": ActRecover,
		">":      ActPaneGrow,
		"<":      ActPaneShrink,
		"}":      ActHunkNext,
		"{":      ActHunkPrev,
		"esc":    ActCancel,
	}
	for key, want := range cases {
		d := NewDispatcher(DefaultKeymap())
		if got := d.Resolve(key); got != want {
			t.Errorf("Resolve(%q) = %v, want %v", key, got, want)
		}
		if d.LeaderPending() {
			t.Errorf("Resolve(%q) left LeaderPending true", key)
		}
	}
}

func TestResolveUnmapped(t *testing.T) {
	d := NewDispatcher(DefaultKeymap())
	if got := d.Resolve("x"); got != ActNone {
		t.Fatalf("Resolve(x) = %v, want ActNone", got)
	}
}

// setConfigHome points both the YAML path (os.UserHomeDir) and the JSON
// fallback path (os.UserConfigDir) at a temp dir, cross-platform.
func setConfigHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	setConfigHome(t)
	c, warns, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(warns) != 0 {
		t.Fatalf("Load() warns = %v, want none", warns)
	}
	if c.Leader != "space" {
		t.Fatalf("Load() Leader = %q, want %q", c.Leader, "space")
	}
	if c.Theme != "tokyonight" {
		t.Fatalf("Load() Theme = %q, want %q", c.Theme, "tokyonight")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	setConfigHome(t)
	want := Config{Leader: "tab", GitHubHost: "github.example.com", Theme: "dracula"}
	if err := want.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Leader != want.Leader || got.GitHubHost != want.GitHubHost || got.Theme != want.Theme {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestLoadYAML(t *testing.T) {
	setConfigHome(t)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "gui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "theme: dracula\nleader: tab\nkeys:\n  g: stage_all\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, warns, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("warns = %v, want none", warns)
	}
	if c.Theme != "dracula" || c.Leader != "tab" || c.Keys["g"] != "stage_all" {
		t.Fatalf("Load() = %+v", c)
	}
}

func TestLoadJSONFallback(t *testing.T) {
	setConfigHome(t)
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"theme":"nord","leader":"space"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Theme != "nord" {
		t.Fatalf("Load() Theme = %q, want nord (from JSON fallback)", c.Theme)
	}
}

func TestLoadMalformedYAMLFallsBack(t *testing.T) {
	setConfigHome(t)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "gui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("theme: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, warns, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(warns) == 0 {
		t.Fatal("expected a warning for malformed YAML")
	}
	if c.Theme != "tokyonight" {
		t.Fatalf("Load() Theme = %q, want default after malformed YAML", c.Theme)
	}
}

func TestSaveWritesYAMLWithKeys(t *testing.T) {
	setConfigHome(t)
	c := Config{Leader: "tab", Theme: "dracula", Keys: map[string]string{"g": "stage_all"}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".config", "gui", "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}
	got, _, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Leader != "tab" || got.Theme != "dracula" || got.Keys["g"] != "stage_all" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestBindings(t *testing.T) {
	bs := DefaultKeymap().Bindings()
	if len(bs) == 0 {
		t.Fatal("Bindings() returned empty slice")
	}
	found := false
	for _, b := range bs {
		if b.Action == ActBranchPanel {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Bindings() missing ActBranchPanel entry")
	}

	for _, want := range []Action{ActUndoFile, ActRecover} {
		ok := false
		for _, b := range bs {
			if b.Action == want {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("Bindings() missing entry for action %v", want)
		}
	}
}

func TestActionNameRoundTrip(t *testing.T) {
	if len(actionNames) == 0 {
		t.Fatal("actionNames is empty")
	}
	for act, name := range actionNames {
		if got := actionByName[name]; got != act {
			t.Errorf("actionByName[%q] = %v, want %v", name, got, act)
		}
	}
}

func TestActionByNameUnknown(t *testing.T) {
	if _, ok := actionByName["definitely_not_an_action"]; ok {
		t.Fatal("unknown action name unexpectedly resolved")
	}
	if actionByName["none"] != ActNone {
		t.Fatalf("actionByName[none] = %v, want ActNone", actionByName["none"])
	}
}

func TestKeymapMergeOverDefaults(t *testing.T) {
	c := Config{Keys: map[string]string{"g": "stage_all"}}
	km, warns := c.Keymap()
	if len(warns) != 0 {
		t.Fatalf("warns = %v, want none", warns)
	}
	if got := NewDispatcher(km).Resolve("g"); got != ActStageAll {
		t.Errorf("Resolve(g) = %v, want ActStageAll", got)
	}
	// An unlisted default survives the merge.
	if got := NewDispatcher(km).Resolve("s"); got != ActStageToggle {
		t.Errorf("Resolve(s) = %v, want ActStageToggle", got)
	}
}

func TestKeymapNoneDisables(t *testing.T) {
	c := Config{Keys: map[string]string{"s": "none"}}
	km, _ := c.Keymap()
	if got := NewDispatcher(km).Resolve("s"); got != ActNone {
		t.Errorf("Resolve(s) = %v, want ActNone", got)
	}
}

func TestKeymapLeaderChord(t *testing.T) {
	c := Config{Leader: "space", Keys: map[string]string{"<leader> g": "stage_all"}}
	km, _ := c.Keymap()
	d := NewDispatcher(km)
	if got := d.Resolve("space"); got != ActNone {
		t.Fatalf("Resolve(space) = %v, want ActNone", got)
	}
	if !d.LeaderPending() {
		t.Fatal("space did not arm leader despite a chord binding")
	}
	if got := d.Resolve("g"); got != ActStageAll {
		t.Errorf("chord Resolve(g) = %v, want ActStageAll", got)
	}
}

func TestKeymapUnknownActionWarns(t *testing.T) {
	c := Config{Keys: map[string]string{"s": "frobnicate"}}
	km, warns := c.Keymap()
	if len(warns) == 0 {
		t.Fatal("expected a warning for an unknown action name")
	}
	if got := NewDispatcher(km).Resolve("s"); got != ActStageToggle {
		t.Errorf("Resolve(s) = %v, want ActStageToggle (default survives bad entry)", got)
	}
}

func TestDefaultConfigLeaderInertWithoutChords(t *testing.T) {
	c := Config{Leader: "space"} // leader set, but no chords
	km, _ := c.Keymap()
	d := NewDispatcher(km)
	if got := d.Resolve("space"); got != ActNone {
		t.Fatalf("Resolve(space) = %v, want ActNone", got)
	}
	if d.LeaderPending() {
		t.Fatal("space armed leader despite no chord bindings")
	}
}

func TestBindingsReflectCustomKeys(t *testing.T) {
	c := Config{Keys: map[string]string{"g": "stage_all"}}
	km, _ := c.Keymap()
	var keys []string
	for _, b := range km.Bindings() {
		if b.Action == ActStageAll {
			keys = b.Keys
		}
	}
	found := false
	for _, k := range keys {
		if k == "g" {
			found = true
		}
	}
	if !found {
		t.Errorf("ActStageAll keys = %v, want to include the remapped \"g\"", keys)
	}
}

func TestBlameLineBinding(t *testing.T) {
	d := NewDispatcher(DefaultKeymap())
	if got := d.Resolve("b"); got != ActBlameLine {
		t.Fatalf("key b resolves to %v, want ActBlameLine", got)
	}
	if name := actionNames[ActBlameLine]; name != "blame_line" {
		t.Fatalf("actionNames[ActBlameLine] = %q, want blame_line", name)
	}
	if got := actionByName["blame_line"]; got != ActBlameLine {
		t.Fatalf("actionByName[blame_line] = %v, want ActBlameLine", got)
	}
}

func TestResolveEditFileKey(t *testing.T) {
	d := NewDispatcher(DefaultKeymap())
	if got := d.Resolve("e"); got != ActEditFile {
		t.Fatalf("Resolve(e) = %v, want ActEditFile", got)
	}
	if name := actionNames[ActEditFile]; name != "edit_file" {
		t.Fatalf("actionNames[ActEditFile] = %q, want edit_file", name)
	}
	if got := actionByName["edit_file"]; got != ActEditFile {
		t.Fatalf("actionByName[edit_file] = %v, want ActEditFile", got)
	}
}
