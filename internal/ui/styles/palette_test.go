package styles

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestPresetsAreComplete asserts every preset fills every Palette field (no
// empty color string left), so a half-defined theme can never ship.
func TestPresetsAreComplete(t *testing.T) {
	names := ThemeNames()
	if len(names) < 6 {
		t.Fatalf("expected at least 6 presets, got %d: %v", len(names), names)
	}
	for _, name := range names {
		p, ok := PaletteFor(name)
		if !ok {
			t.Fatalf("preset %q missing from registry", name)
		}
		v := reflect.ValueOf(p)
		tp := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() != reflect.String {
				continue
			}
			if v.Field(i).String() == "" {
				t.Errorf("preset %q: field %q is empty", name, tp.Field(i).Name)
			}
		}
	}
}

// TestExpectedPresetsExist checks the curated set is registered.
func TestExpectedPresetsExist(t *testing.T) {
	want := []string{"tokyonight", "catppuccin", "gruvbox", "nord", "solarized", "mono"}
	for _, w := range want {
		if _, ok := PaletteFor(w); !ok {
			t.Errorf("expected preset %q to be registered", w)
		}
	}
}

// TestSetThemeSwapsActivePalette asserts SetTheme changes the active theme and
// rebuilds the package style vars (different themes → different rendered output),
// and that an unknown name falls back to the default.
func TestSetThemeSwapsActivePalette(t *testing.T) {
	// Force a truecolor profile so rendered styling is deterministic in CI/non-TTY.
	lipgloss.SetColorProfile(termenv.TrueColor)
	prev := ActiveTheme()
	defer SetTheme(prev)

	got := SetTheme("gruvbox")
	if got != "gruvbox" || ActiveTheme() != "gruvbox" {
		t.Fatalf("SetTheme(gruvbox) active=%q returned=%q", ActiveTheme(), got)
	}
	gruvAdd := Added.Render("x")

	SetTheme("nord")
	nordAdd := Added.Render("x")
	if gruvAdd == nordAdd {
		t.Errorf("switching themes should change rendered styling")
	}

	if fb := SetTheme("does-not-exist"); fb != DefaultTheme {
		t.Errorf("unknown theme should fall back to %q, got %q", DefaultTheme, fb)
	}
}
