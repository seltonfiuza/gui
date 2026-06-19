package config

import (
	"runtime"
	"testing"
)

func TestResolveLeaderChord(t *testing.T) {
	d := NewDispatcher(DefaultKeymap())

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
	d := NewDispatcher(DefaultKeymap())

	d.Resolve("space")
	if got := d.Resolve("z"); got != ActNone {
		t.Fatalf("Resolve(z) after leader = %v, want ActNone", got)
	}
	if d.LeaderPending() {
		t.Fatalf("LeaderPending() = true after unmapped chord key, want false")
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

// setConfigHome points os.UserConfigDir at a temp dir cross-platform.
func setConfigHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", dir)
	} else {
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	setConfigHome(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if c.Leader != "space" {
		t.Fatalf("Load() Leader = %q, want %q", c.Leader, "space")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	setConfigHome(t)
	want := Config{Leader: "tab", GitHubHost: "github.example.com"}
	if err := want.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
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
