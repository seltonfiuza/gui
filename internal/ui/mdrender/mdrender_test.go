package mdrender

import (
	"strings"
	"testing"
)

func TestRenderContainsText(t *testing.T) {
	r := New()
	out := r.Render("# Title\n\nHello **world**", 40)
	if out == "" {
		t.Fatal("expected non-empty render")
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "Hello") {
		t.Fatalf("render missing text: %q", out)
	}
}

func TestRenderMemoizes(t *testing.T) {
	r := New()
	_ = r.Render("# A\n\nbody", 40)
	_ = r.Render("# A\n\nbody", 40) // identical → cache hit
	if r.Renders() != 1 {
		t.Fatalf("Renders() = %d, want 1 (second call should hit cache)", r.Renders())
	}
	_ = r.Render("# A\n\nbody", 60) // different width → miss
	if r.Renders() != 2 {
		t.Fatalf("Renders() = %d, want 2 (width change should miss)", r.Renders())
	}
	_ = r.Render("# B\n\nother", 60) // different markdown → miss
	if r.Renders() != 3 {
		t.Fatalf("Renders() = %d, want 3 (markdown change should miss)", r.Renders())
	}
}

func TestRenderEmpty(t *testing.T) {
	r := New()
	if out := r.Render("", 40); out != "" {
		t.Fatalf("empty markdown should render empty, got %q", out)
	}
}
