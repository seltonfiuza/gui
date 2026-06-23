// Package mdrender renders markdown to ANSI-styled, word-wrapped text for the
// TUI, caching the result so glamour runs only when the (markdown, width) input
// changes. Rendering never fails: a glamour error falls back to a plain-text
// wrap so the UI always shows something.
package mdrender

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Renderer caches the last render keyed by (markdown, width).
type Renderer struct {
	lastMD    string
	lastWidth int
	lastOut   string
	hasCache  bool
	renders   int // count of actual glamour invocations (cache misses)
}

// New returns an empty Renderer.
func New() *Renderer { return &Renderer{} }

// Renders reports how many times glamour was actually invoked (cache misses).
func (r *Renderer) Renders() int { return r.renders }

// Render returns markdown rendered to the given width. Empty input yields "".
func (r *Renderer) Render(markdown string, width int) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}
	if width < 1 {
		width = 1
	}
	if r.hasCache && r.lastMD == markdown && r.lastWidth == width {
		return r.lastOut
	}
	out := r.render(markdown, width)
	r.lastMD, r.lastWidth, r.lastOut, r.hasCache = markdown, width, out, true
	return out
}

func (r *Renderer) render(markdown string, width int) string {
	r.renders++
	tr, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err == nil {
		if out, rerr := tr.Render(markdown); rerr == nil {
			// glamour adds leading/trailing blank lines; trim outer whitespace.
			return strings.Trim(out, "\n")
		}
	}
	// Fallback: plain-text wrap.
	return lipgloss.NewStyle().Width(width).Render(markdown)
}
