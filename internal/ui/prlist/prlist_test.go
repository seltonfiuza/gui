package prlist

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/github"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestCreateFormOpensOnN(t *testing.T) {
	m := New()
	m.SetPRs([]github.PR{{Number: 1, Title: "x"}})
	intent, _ := m.Update(runeKey('n'))
	if intent.Kind != IntentStartCreate {
		t.Fatalf("Update(n) kind = %v, want IntentStartCreate", intent.Kind)
	}
}

func TestCreateSubmitEmptyTitle(t *testing.T) {
	m := New()
	m.OpenCreate("feature", "main", "Pull Request")
	intent, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if intent.Kind != IntentNone {
		t.Fatalf("empty-title submit kind = %v, want IntentNone", intent.Kind)
	}
	if m.createNote == "" {
		t.Error("expected a validation note for empty title")
	}
}

func TestCreateSubmitValid(t *testing.T) {
	m := New()
	m.OpenCreate("feature", "main", "Pull Request")
	m.Update(runeKey('H'))
	m.Update(runeKey('i'))
	intent, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if intent.Kind != IntentCreate {
		t.Fatalf("valid submit kind = %v, want IntentCreate", intent.Kind)
	}
	if intent.Opts.Title != "Hi" {
		t.Errorf("Opts.Title = %q, want Hi", intent.Opts.Title)
	}
	if intent.Opts.Head != "feature" {
		t.Errorf("Opts.Head = %q, want feature", intent.Opts.Head)
	}
	if intent.Opts.Base != "main" {
		t.Errorf("Opts.Base = %q, want main", intent.Opts.Base)
	}
}

func TestCreateEscCancels(t *testing.T) {
	m := New()
	m.OpenCreate("feature", "main", "Pull Request")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("after esc mode = %v, want modeList", m.mode)
	}
}

func TestScrollDescriptionAt(t *testing.T) {
	m := New()
	m.Open("Pull Requests")
	body := ""
	for i := 0; i < 100; i++ {
		body += fmt.Sprintf("line %d\n\n", i)
	}
	m.SetDetail(github.PR{Number: 1, Title: "t", Body: body}, "")
	m.mode = modeDetail
	_ = m.View(120, 40) // sizes the viewport and records the desc rect

	// A point inside the description rect scrolls; outside does not.
	inX, inY := m.descRectX+1, m.descRectY+1
	if !m.ScrollDescriptionAt(inX, inY, 3) {
		t.Fatalf("expected scroll inside desc rect")
	}
	if m.descVP.YOffset != 3 {
		t.Fatalf("YOffset = %d, want 3", m.descVP.YOffset)
	}
	if m.ScrollDescriptionAt(m.descRectX+m.descRectW+5, inY, 3) {
		t.Fatalf("did not expect scroll outside desc rect")
	}
}

func TestDescriptionRendersMarkdown(t *testing.T) {
	m := New()
	m.Open("Pull Requests")
	m.SetDetail(github.PR{Number: 1, Title: "t", Body: "# Heading\n\nSome body text."}, "")
	m.mode = modeDetail
	raw := m.View(120, 40)
	// Glamour styling must survive into the rendered pane (guards against a
	// regression that strips ANSI in production).
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected ANSI styling in rendered description:\n%q", raw)
	}
	// Strip ANSI so substring checks aren't broken by glamour's per-span colour
	// codes (which can split words like "body text" across escape sequences).
	out := ansi.Strip(raw)
	// The heading text survives markdown rendering (styling aside).
	if !strings.Contains(out, "Heading") || !strings.Contains(out, "body text") {
		t.Fatalf("description did not render markdown content:\n%s", out)
	}
	// The literal '#' markdown marker should be gone in the rendered heading.
	if strings.Contains(out, "# Heading") {
		t.Fatalf("expected markdown '#' to be rendered away:\n%s", out)
	}
}

func TestDescriptionViewportScrolls(t *testing.T) {
	m := New()
	m.Open("Pull Requests")
	body := ""
	for i := 0; i < 100; i++ {
		body += fmt.Sprintf("line %d\n\n", i)
	}
	m.SetDetail(github.PR{Number: 1, Title: "t", Body: body}, "")
	m.mode = modeDetail
	m.focus = focusDesc
	// Render once so the viewport gets sized + content.
	_ = m.View(120, 40)
	if got := m.descVP.YOffset; got != 0 {
		t.Fatalf("initial YOffset = %d, want 0", got)
	}
	// A 'j' keypress while the description is focused scrolls it down.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	_ = m.View(120, 40)
	if got := m.descVP.YOffset; got != 1 {
		t.Fatalf("after j YOffset = %d, want 1", got)
	}
}

func TestOpenDetailShowsDetailLoadingNotList(t *testing.T) {
	m := New()
	m.OpenDetail("Pull Requests")
	out := m.View(80, 24)
	if strings.Contains(out, "n new") || strings.Contains(out, "enter open") {
		t.Errorf("OpenDetail should show the detail view, not the list footer:\n%s", out)
	}
	if !strings.Contains(out, "loading…") {
		t.Errorf("OpenDetail should show the detail loading state:\n%s", out)
	}
}

func TestSetDetailAfterOpenDetailRendersDiff(t *testing.T) {
	m := New()
	m.OpenDetail("Pull Requests")
	m.SetDetail(github.PR{Number: 7, Title: "My PR"}, "diff --git a/x b/x\n@@ -1 +1 @@\n+DIFF_MARKER\n")
	out := m.View(80, 24)
	if strings.Contains(out, "loading…") {
		t.Errorf("loading state should clear after SetDetail:\n%s", out)
	}
	if !strings.Contains(out, "DIFF_MARKER") {
		t.Errorf("detail diff should render after SetDetail:\n%s", out)
	}
}
