package listpane

import (
	"strings"
	"testing"
)

func rows(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "row" + string(rune('A'+i))
	}
	return out
}

func TestSelectionClampsToBounds(t *testing.T) {
	m := New("Title")
	m.SetSize(20, 4) // title + 3 visible rows
	m.SetRows(rows(5))
	m.MoveSelection(-1)
	if got := m.Selected(); got != 0 {
		t.Errorf("MoveSelection(-1) from 0 = %d, want 0", got)
	}
	m.MoveSelection(100)
	if got := m.Selected(); got != 4 {
		t.Errorf("MoveSelection(100) = %d, want 4 (last)", got)
	}
}

func TestSelectedIsMinusOneWhenEmpty(t *testing.T) {
	m := New("Title")
	m.SetSize(20, 4)
	if got := m.Selected(); got != -1 {
		t.Errorf("empty Selected() = %d, want -1", got)
	}
}

func TestViewIsExactlyHeightLinesAndWindowsSelection(t *testing.T) {
	m := New("Title")
	m.SetSize(20, 4) // 1 title + 3 rows
	m.SetRows(rows(6))
	m.MoveSelection(5) // select last; window must scroll to keep it visible
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("View lines = %d, want 4\n%s", len(lines), out)
	}
	if !strings.Contains(out, "rowF") {
		t.Errorf("selected last row 'rowF' not visible:\n%s", out)
	}
}

func TestPlaceholderShownWhenEmpty(t *testing.T) {
	m := New("Title")
	m.SetSize(20, 4)
	m.SetPlaceholder("no items")
	if !strings.Contains(m.View(), "no items") {
		t.Errorf("placeholder not shown:\n%s", m.View())
	}
	if got := len(strings.Split(m.View(), "\n")); got != 4 {
		t.Errorf("empty View() lines = %d, want 4", got)
	}
}
