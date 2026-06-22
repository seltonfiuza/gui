// Package diffview renders the FR-1 header and FR-2 diff panel: three labeled
// file groups (Staged / Unstaged / Untracked) plus a scrollable, colorized
// unified-diff viewport for the selected file.
//
// The model is "dumb": it owns selection, focus and scroll state and renders,
// but it does not perform git I/O. The root App owns the git.Service, watches
// the diffview's Selected() file, and feeds diffs in via SetDiff.
//
// Two focus targets exist: the file list and the diff pane. When the list is
// focused, j/k move the file selection; when the diff is focused, j/k move a
// line cursor over the diff text (the SAME 0-based index over
// strings.Split(diffText, "\n") that git.ParseHunks uses), and }/{ jump between
// hunk @@ headers.
package diffview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/git"
	"github.com/seltonfiuza/gui/internal/ui/styles"
)

// Group identifies which of the three sections a file row belongs to.
type Group int

const (
	GroupStaged Group = iota
	GroupUnstaged
	GroupUntracked
)

// Focus identifies which sub-pane currently receives j/k navigation.
type Focus int

const (
	// FocusList: j/k move the file selection.
	FocusList Focus = iota
	// FocusDiff: j/k move the diff line cursor; }/{ jump hunks.
	FocusDiff
)

// Row is one selectable file row, flattened across all groups.
type Row struct {
	Group Group
	File  git.ChangedFile
}

// Model is the diff panel: file list + diff viewport.
type Model struct {
	status *git.Status
	rows   []Row
	// cursor indexes the current visible NODE list (files AND folders), not rows.
	cursor int
	// selKey is the key (file path or folder path) of the selected node, used to
	// re-anchor the cursor after the node list changes (refresh / collapse / mode
	// toggle) so the selection follows the same item rather than an index.
	selKey string
	// treeMode renders changed files as a folder tree (true) or a flat list of
	// full paths (false). Toggleable at runtime.
	treeMode bool
	// collapsed records folders (by full path) the user has folded shut.
	collapsed map[string]bool
	// listHidden hides the file-list pane entirely (the diff fills the width).
	listHidden bool
	// commitEnabled shows the commit affordance pinned to the bottom of the file
	// list. On by default; the ⇧C key (handled by the root App) opens the dialog.
	commitEnabled bool
	// hoverRow is the visible-node index currently under the mouse pointer, or -1
	// when nothing is hovered. Purely a visual affordance.
	hoverRow int
	// listOffset is the number of list lines scrolled past the top of the file
	// pane, so a list taller than the pane scrolls to keep the selection visible.
	listOffset int

	focus Focus

	vp          viewport.Model
	vpReady     bool
	diffPath    string   // path the viewport content currently belongs to
	diffRaw     string   // raw (uncolorized) diff text for the loaded path
	diffLines   []string // strings.Split(diffRaw, "\n") — the RAW cursor index space
	cleaned     cleanedDiff
	styledLines []string // per-rendered-row colorized cache (parallel to cleaned.lines)
	hunks       []git.Hunk
	lineCursor  int  // 0-based index into diffLines (RAW space; matches git.ParseHunks)
	prevCursor  int  // last RAW cursor whose highlight is reflected in styledLines
	rawMode     bool // when true, show the unfiltered raw diff (toggle)

	listWidth   int
	totalWidth  int
	totalHeight int
}

// New builds an empty diff panel.
func New() Model {
	return Model{hoverRow: -1, treeMode: true, collapsed: map[string]bool{}, commitEnabled: true}
}

// SetCommitEnabled toggles the commit affordance at the bottom of the file list.
func (m *Model) SetCommitEnabled(b bool) { m.commitEnabled = b }

// CommitEnabled reports whether the commit affordance is shown.
func (m *Model) CommitEnabled() bool { return m.commitEnabled }

// StagedCount returns the number of staged files (what a commit would include).
func (m *Model) StagedCount() int { return m.groupFileCount(GroupStaged) }

// CommitBarHeight returns the rows the commit affordance occupies at the bottom
// of the file list (0 when it is not shown). Used by the mouse hit-test.
func (m *Model) CommitBarHeight() int {
	if m.commitBarVisible() {
		return commitBarLines
	}
	return 0
}

// SetHoverRow sets the visible-node index under the mouse pointer (-1 for none).
// Returns true when the hover target changed, so the caller can avoid a redundant
// re-render on every pixel of mouse motion. Hover is cosmetic only.
func (m *Model) SetHoverRow(i int) bool {
	if i < -1 {
		i = -1
	}
	if i == m.hoverRow {
		return false
	}
	m.hoverRow = i
	return true
}

// ClearHover removes any hover highlight. Returns true if state changed.
func (m *Model) ClearHover() bool { return m.SetHoverRow(-1) }

// HoverRow returns the flattened row index under the pointer, or -1 for none.
func (m *Model) HoverRow() int { return m.hoverRow }

// SetStatus replaces the status and rebuilds the row list, then re-anchors the
// selection by key (the previously selected file/folder stays selected if still
// present; otherwise the cursor clamps into range). This keeps a background
// refresh from yanking the user's selection back to the top.
func (m *Model) SetStatus(s *git.Status) {
	m.status = s
	m.rebuild()
	m.pruneCollapsed()
	m.syncCursor()
}

// ReconcileSelection resolves the new cursor index after the file list changed.
// Given the previously selected path, its previous index, and the new ordered
// list of paths, it returns:
//   - the index of oldPath if it is still present (selection follows the file);
//   - otherwise a neighbor: the row now at oldIdx, clamped into range (so when a
//     file is removed the selection lands on what slid into its place);
//   - 0 for an empty list (callers must treat an empty list as "no selection").
//
// Pure and unit-tested.
func ReconcileSelection(oldPath string, oldIdx int, newPaths []string) int {
	if len(newPaths) == 0 {
		return 0
	}
	if oldPath != "" {
		for i, p := range newPaths {
			if p == oldPath {
				return i
			}
		}
	}
	// Path gone (or none before): keep the same slot, clamped.
	idx := oldIdx
	if idx >= len(newPaths) {
		idx = len(newPaths) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}

func (m *Model) rebuild() {
	m.rows = m.rows[:0]
	if m.status == nil {
		return
	}
	// Sort each group by path so the flattened row order matches the depth-first
	// order the folder tree renders in (lexicographic full-path order == tree
	// order), keeping j/k navigation aligned with what's on screen.
	add := func(g Group, files []git.ChangedFile) {
		sorted := append([]git.ChangedFile(nil), files...)
		sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Path < sorted[b].Path })
		for _, f := range sorted {
			m.rows = append(m.rows, Row{Group: g, File: f})
		}
	}
	add(GroupStaged, m.status.Staged)
	add(GroupUnstaged, m.status.Unstaged)
	add(GroupUntracked, m.status.Untracked)
}

// ---- visible-node model (folder tree / flat list) ----

type nodeKind int

const (
	fileNode nodeKind = iota
	folderNode
)

// node is one navigable item in the file list: a file leaf or a folder. Folders
// are selectable so they can be collapsed/expanded. Group headers are NOT nodes
// (they are non-navigable chrome inserted at render time).
type node struct {
	kind      nodeKind
	depth     int
	group     Group
	label     string // folder: compacted dir label (no trailing slash); file: leaf name
	key       string // folder: full dir path; file: file path — stable selection id
	fileRow   int    // file: index into m.rows; folder: -1
	count     int    // folder: number of files beneath it
	collapsed bool   // folder only
}

// nodes computes the current visible node list from the rows, the tree/flat mode
// and the collapsed set. It is the single source of truth that rendering, the
// mouse hit-test and the scroll math all derive from.
func (m *Model) nodes() []node {
	var out []node
	for i := 0; i < len(m.rows); {
		g := m.rows[i].Group
		start := i
		for i < len(m.rows) && m.rows[i].Group == g {
			i++
		}
		if m.treeMode {
			t := buildDirTree(m.rows[start:i], start)
			m.flatten(t, 0, g, &out)
		} else {
			for k := start; k < i; k++ {
				out = append(out, node{
					kind: fileNode, depth: 0, group: g,
					label: m.rows[k].File.Path, key: m.rows[k].File.Path,
					fileRow: k, count: -1,
				})
			}
		}
	}
	return out
}

// dirTree is an intermediate directory tree built from a group's sorted files.
type dirTree struct {
	name     string
	path     string
	children map[string]*dirTree
	order    []string // child dir names, in insertion (sorted) order
	files    []int    // file row indices directly in this dir
}

func buildDirTree(rows []Row, baseIdx int) *dirTree {
	root := &dirTree{children: map[string]*dirTree{}}
	for k, r := range rows {
		dirs, _ := splitPath(r.File.Path)
		cur := root
		acc := ""
		for _, d := range dirs {
			if acc == "" {
				acc = d
			} else {
				acc += "/" + d
			}
			child, ok := cur.children[d]
			if !ok {
				child = &dirTree{name: d, path: acc, children: map[string]*dirTree{}}
				cur.children[d] = child
				cur.order = append(cur.order, d)
			}
			cur = child
		}
		cur.files = append(cur.files, baseIdx+k)
	}
	return root
}

func countFiles(t *dirTree) int {
	n := len(t.files)
	for _, name := range t.order {
		n += countFiles(t.children[name])
	}
	return n
}

// flatten emits nodes for a directory: child folders first (each compacted across
// single-child chains and recursed into unless collapsed), then this dir's files.
func (m *Model) flatten(t *dirTree, depth int, g Group, out *[]node) {
	for _, name := range t.order {
		child := t.children[name]
		// Compact single-child chains: while the node holds no files and exactly
		// one subdirectory, fold that subdirectory into the label.
		label := name
		nd := child
		for len(nd.files) == 0 && len(nd.order) == 1 {
			only := nd.children[nd.order[0]]
			label += "/" + only.name
			nd = only
		}
		collapsed := m.collapsed[nd.path]
		*out = append(*out, node{
			kind: folderNode, depth: depth, group: g,
			label: label, key: nd.path, fileRow: -1,
			count: countFiles(nd), collapsed: collapsed,
		})
		if !collapsed {
			m.flatten(nd, depth+1, g, out)
		}
	}
	for _, fr := range t.files {
		f := m.rows[fr].File
		_, base := splitPath(f.Path)
		leaf := base
		if f.OrigPath != "" {
			_, ob := splitPath(f.OrigPath)
			leaf = ob + " → " + base
		}
		*out = append(*out, node{
			kind: fileNode, depth: depth, group: g,
			label: leaf, key: f.Path, fileRow: fr, count: -1,
		})
	}
}

// syncCursor re-anchors the cursor to the node whose key matches selKey after the
// node list changed; if that node is gone it clamps the index. Keeps selKey and
// the scroll window consistent.
func (m *Model) syncCursor() {
	nodes := m.nodes()
	if len(nodes) == 0 {
		m.cursor, m.selKey = 0, ""
		m.listOffset = 0
		return
	}
	if m.selKey != "" {
		for i, n := range nodes {
			if n.key == m.selKey {
				m.cursor = i
				m.ensureSelectedVisible()
				return
			}
		}
	}
	if m.cursor >= len(nodes) {
		m.cursor = len(nodes) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.selKey = nodes[m.cursor].key
	m.ensureSelectedVisible()
}

// pruneCollapsed drops collapsed entries for folders that no longer exist, so the
// set doesn't grow without bound across refreshes.
func (m *Model) pruneCollapsed() {
	if len(m.collapsed) == 0 {
		return
	}
	live := map[string]bool{}
	for _, n := range m.nodes() {
		if n.kind == folderNode {
			live[n.key] = true
		}
	}
	for k := range m.collapsed {
		if !live[k] {
			delete(m.collapsed, k)
		}
	}
}

// selectedNode returns the node under the cursor, if any.
func (m *Model) selectedNode() (node, bool) {
	nodes := m.nodes()
	if m.cursor < 0 || m.cursor >= len(nodes) {
		return node{}, false
	}
	return nodes[m.cursor], true
}

// Activate acts on the node under the cursor: a folder toggles collapse and
// returns true; a file (or empty) returns false so the caller opens the diff.
func (m *Model) Activate() bool {
	n, ok := m.selectedNode()
	if !ok || n.kind != folderNode {
		return false
	}
	m.toggleFolder(n.key)
	return true
}

func (m *Model) toggleFolder(key string) {
	if m.collapsed[key] {
		delete(m.collapsed, key)
	} else {
		m.collapsed[key] = true
	}
	m.selKey = key // keep the folder selected across the relayout
	m.syncCursor()
}

// Collapse (h): collapse an expanded folder under the cursor; otherwise move the
// selection to the nearest ancestor folder.
func (m *Model) Collapse() {
	nodes := m.nodes()
	if m.cursor < 0 || m.cursor >= len(nodes) {
		return
	}
	n := nodes[m.cursor]
	if n.kind == folderNode && !m.collapsed[n.key] {
		m.toggleFolder(n.key)
		return
	}
	// Move to the nearest ancestor folder above the cursor.
	for i := m.cursor - 1; i >= 0; i-- {
		if nodes[i].kind == folderNode && isAncestorPath(nodes[i].key, n.key) {
			m.cursor = i
			m.selKey = nodes[i].key
			m.ensureSelectedVisible()
			return
		}
	}
}

// Expand (l): expand a collapsed folder under the cursor; if already expanded,
// step into its first child.
func (m *Model) Expand() {
	nodes := m.nodes()
	if m.cursor < 0 || m.cursor >= len(nodes) {
		return
	}
	n := nodes[m.cursor]
	if n.kind != folderNode {
		return
	}
	if m.collapsed[n.key] {
		m.toggleFolder(n.key)
		return
	}
	if m.cursor < len(nodes)-1 {
		m.cursor++
		m.selKey = nodes[m.cursor].key
		m.ensureSelectedVisible()
	}
}

// ToggleTreeMode flips between the folder tree and the flat full-path list,
// keeping the selected file selected.
func (m *Model) ToggleTreeMode() {
	m.treeMode = !m.treeMode
	m.syncCursor()
}

// TreeMode reports whether the folder tree (vs flat list) is active.
func (m *Model) TreeMode() bool { return m.treeMode }

// isAncestorPath reports whether dir is an ancestor directory of path.
func isAncestorPath(dir, path string) bool {
	return strings.HasPrefix(path, dir+"/")
}

// SetListHidden hides or shows the file-list pane. When hidden the diff takes the
// full width.
func (m *Model) SetListHidden(b bool) { m.listHidden = b }

// ListHidden reports whether the file-list pane is hidden.
func (m *Model) ListHidden() bool { return m.listHidden }

// SetSize lays out the panel for the available space. listWidth is the desired
// absolute width of the file list; it is clamped to keep both panes usable. When
// the list is hidden the diff fills the whole width (minus the scrollbar).
func (m *Model) SetSize(width, height, listWidth int) {
	m.totalWidth = width
	m.totalHeight = height
	if height < 1 {
		height = 1
	}
	var vpWidth int
	if m.listHidden {
		m.listWidth = 0
		vpWidth = width - ScrollbarWidth
	} else {
		m.listWidth = ClampListWidth(width, listWidth)
		// Reserve one column for the divider and one for the diff scrollbar.
		vpWidth = width - m.listWidth - dividerWidth - ScrollbarWidth
	}
	if vpWidth < 1 {
		vpWidth = 1
	}
	if !m.vpReady {
		m.vp = viewport.New(vpWidth, height)
		m.vpReady = true
	} else {
		m.vp.Width = vpWidth
		m.vp.Height = height
	}
	// Re-render the diff content for the new width so highlight + wrapping match.
	m.refreshViewport()
	// A height change can move the selection out of the visible window.
	m.ensureSelectedVisible()
}

// MinListWidth / MinDiffWidth are the minimum pane widths enforced by resize.
const (
	MinListWidth = 16
	MinDiffWidth = 20
	dividerWidth = 1
	// ScrollbarWidth is the single column reserved at the right edge of the diff
	// pane for the vertical scrollbar.
	ScrollbarWidth = 1
)

// ClampListWidth returns a list width that respects the minimum widths of both
// panes given a total width. Pure function — unit-testable.
func ClampListWidth(total, want int) int {
	if total <= 0 {
		return 0
	}
	// The diff pane needs total - want - divider >= MinDiffWidth.
	maxList := total - dividerWidth - MinDiffWidth
	if maxList < 1 {
		// Terminal too narrow to honor both minimums: give the list whatever is
		// left after the divider, never less than 0.
		ml := total - dividerWidth
		if ml < 0 {
			ml = 0
		}
		return ml
	}
	if want < MinListWidth {
		want = MinListWidth
	}
	if want > maxList {
		want = maxList
	}
	if want < 0 {
		want = 0
	}
	return want
}

// Focus reports the current focus target.
func (m *Model) Focus() Focus { return m.focus }

// FocusDiff focuses the diff pane (j/k move the line cursor). It restyles the
// cursor line so the highlight appears immediately on focus (not only after the
// first cursor move).
func (m *Model) FocusDiff() {
	m.focus = FocusDiff
	m.restyleCursorLines()
	m.ensureCursorVisible()
}

// FocusList focuses the file list (j/k move the file selection). It restyles the
// cursor line so the diff highlight is cleared immediately when focus leaves.
func (m *Model) FocusList() {
	m.focus = FocusList
	m.restyleCursorLines()
}

// CursorDown moves the node selection (list focus) or the diff line cursor (diff
// focus) down by one.
func (m *Model) CursorDown() {
	if m.focus == FocusDiff {
		m.lineCursorDown()
		return
	}
	m.moveCursor(1)
}

// CursorUp moves the node selection (list focus) or the diff line cursor (diff
// focus) up by one.
func (m *Model) CursorUp() {
	if m.focus == FocusDiff {
		m.lineCursorUp()
		return
	}
	m.moveCursor(-1)
}

// moveCursor steps the node selection by delta, clamped, and re-anchors selKey.
func (m *Model) moveCursor(delta int) {
	nodes := m.nodes()
	if len(nodes) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(nodes) {
		m.cursor = len(nodes) - 1
	}
	m.selKey = nodes[m.cursor].key
	m.ensureSelectedVisible()
}

// SelectNext / SelectPrev move the node selection by one irrespective of focus.
// Used by the mouse wheel over the file list so scrolling there always moves the
// selection (never the diff line cursor).
func (m *Model) SelectNext() { m.moveCursor(1) }

func (m *Model) SelectPrev() { m.moveCursor(-1) }

// renderedRows is the number of rows shown in the cleaned view.
func (m *Model) renderedRows() int { return len(m.cleaned.lines) }

// renderedCursor returns the rendered row the raw line cursor maps to, or -1
// when there is no valid rendered row (e.g. an all-plumbing diff renders to
// nothing in cleaned mode). Callers must treat -1 as "no cursor row".
func (m *Model) renderedCursor() int {
	if len(m.cleaned.lines) == 0 {
		return -1
	}
	if m.lineCursor >= 0 && m.lineCursor < len(m.cleaned.rawToRendered) {
		if r := m.cleaned.rawToRendered[m.lineCursor]; r >= 0 {
			return r
		}
	}
	return 0
}

// rawForRendered maps a rendered row back to its raw diff line index.
func (m *Model) rawForRendered(row int) int {
	if row >= 0 && row < len(m.cleaned.renderedToRaw) {
		return m.cleaned.renderedToRaw[row]
	}
	return 0
}

// lineCursorDown/Up step the cursor through *rendered* rows (so visually skipped
// plumbing lines are never landed on), then translate the new rendered row back
// into raw-line space — keeping LineCursor() consistent with git.ParseHunks.
func (m *Model) lineCursorDown() {
	r := m.renderedCursor()
	if r < m.renderedRows()-1 {
		m.lineCursor = m.rawForRendered(r + 1)
		m.afterCursorMove()
	}
}

func (m *Model) lineCursorUp() {
	r := m.renderedCursor()
	if r > 0 {
		m.lineCursor = m.rawForRendered(r - 1)
		m.afterCursorMove()
	}
}

// MoveCursorToRendered moves the line cursor to a rendered row (used by the
// mouse: click in the diff pane → cursor on that row). Clamped to range.
func (m *Model) MoveCursorToRendered(row int) {
	if m.renderedRows() == 0 {
		return
	}
	if row < 0 {
		row = 0
	}
	if row >= m.renderedRows() {
		row = m.renderedRows() - 1
	}
	m.lineCursor = m.rawForRendered(row)
	m.afterCursorMove()
}

// ViewportYOffset returns the diff viewport's current scroll offset (rendered
// rows scrolled past the top). Used by the mouse hit-test.
func (m *Model) ViewportYOffset() int {
	if !m.vpReady {
		return 0
	}
	return m.vp.YOffset
}

// ToggleRawDiff flips between the cleaned view and the unfiltered raw diff,
// re-rendering and keeping the cursor on the same raw line.
func (m *Model) ToggleRawDiff() {
	m.rawMode = !m.rawMode
	m.renderStyledLines()
	if m.vpReady {
		m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	}
	m.ensureCursorVisible()
}

// RawMode reports whether the raw (unfiltered) diff is being shown.
func (m *Model) RawMode() bool { return m.rawMode }

// LineCursor returns the current 0-based diff line cursor.
func (m *Model) LineCursor() int { return m.lineCursor }

// Hunks returns the parsed hunks for the loaded diff.
func (m *Model) Hunks() []git.Hunk { return m.hunks }

// DiffRaw returns the raw diff text for the loaded path.
func (m *Model) DiffRaw() string { return m.diffRaw }

// HunkNext jumps the line cursor to the next hunk's @@ header (forward of the
// cursor). No-op when the diff is not focused or there is no next hunk.
func (m *Model) HunkNext() {
	if m.focus != FocusDiff {
		return
	}
	for _, h := range m.hunks {
		if h.StartLine > m.lineCursor {
			m.lineCursor = h.StartLine
			m.afterCursorMove()
			return
		}
	}
}

// HunkPrev jumps the line cursor to the previous hunk's @@ header (before the
// cursor). No-op when the diff is not focused or there is no previous hunk.
func (m *Model) HunkPrev() {
	if m.focus != FocusDiff {
		return
	}
	for i := len(m.hunks) - 1; i >= 0; i-- {
		if m.hunks[i].StartLine < m.lineCursor {
			m.lineCursor = m.hunks[i].StartLine
			m.afterCursorMove()
			return
		}
	}
}

func (m *Model) afterCursorMove() {
	m.restyleCursorLines()
	m.ensureCursorVisible()
}

// SelectRow sets the selection to visible-node index i (clamped). Returns true
// if a valid node was selected. Used by the mouse: click a list line → select
// that node (a file or a folder).
func (m *Model) SelectRow(i int) bool {
	nodes := m.nodes()
	if i < 0 || i >= len(nodes) {
		return false
	}
	m.cursor = i
	m.selKey = nodes[i].key
	m.ensureSelectedVisible()
	return true
}

// RowCount returns the number of file rows.
func (m *Model) RowCount() int { return len(m.rows) }

// ScrollDiff scrolls the diff viewport by delta rendered rows (positive = down).
func (m *Model) ScrollDiff(delta int) {
	if !m.vpReady {
		return
	}
	m.vp.SetYOffset(m.vp.YOffset + delta)
}

// Selected returns the file row under the cursor, if the cursor is on a file (not
// a folder) and one exists.
func (m *Model) Selected() (Row, bool) {
	n, ok := m.selectedNode()
	if !ok || n.kind != fileNode {
		return Row{}, false
	}
	if n.fileRow < 0 || n.fileRow >= len(m.rows) {
		return Row{}, false
	}
	return m.rows[n.fileRow], true
}

// SelectedPath returns the selected file path, or "".
func (m *Model) SelectedPath() string {
	if r, ok := m.Selected(); ok {
		return r.File.Path
	}
	return ""
}

// DiffPath reports the path whose diff is currently loaded in the viewport.
func (m *Model) DiffPath() string { return m.diffPath }

// SetDiff loads a unified diff (for path) into the viewport, parsing hunks and
// resetting the line cursor to the first hunk (or top).
func (m *Model) SetDiff(path, raw string) {
	m.diffPath = path
	m.diffRaw = raw
	m.diffLines = strings.Split(raw, "\n")
	m.hunks, _ = git.ParseHunks(raw)
	// Start the cursor on the first hunk header when there is one, else at top.
	if len(m.hunks) > 0 {
		m.lineCursor = m.hunks[0].StartLine
	} else {
		m.lineCursor = 0
	}
	if !m.vpReady {
		m.vp = viewport.New(40, 10)
		m.vpReady = true
	}
	m.renderStyledLines()
	m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	m.vp.GotoTop()
	m.ensureCursorVisible()
}

// rebuildCleaned recomputes the cleaned render + index maps for the current raw
// diff and rawMode. Must be called before (re)styling rendered rows.
func (m *Model) rebuildCleaned() {
	m.cleaned = cleanDiff(m.diffRaw, m.rawMode)
}

// SetDiffPreserving loads a diff like SetDiff, but when the raw text is
// identical to what is already loaded for path it is a no-op that keeps the line
// cursor and viewport scroll exactly where they are. This is the background-tick
// path: an unchanged diff must never reset the user's position. When the text
// actually changed it falls through to SetDiff (cursor resets to the first hunk).
func (m *Model) SetDiffPreserving(path, raw string) {
	if m.diffPath == path && m.diffRaw == raw {
		return
	}
	m.SetDiff(path, raw)
}

// renderStyledLines (re)builds the cleaned render + the per-rendered-row
// colorized cache, applying the cursor highlight. This is the only full
// re-colorize; cursor moves use restyleCursorLines to avoid re-styling the whole
// diff.
func (m *Model) renderStyledLines() {
	m.rebuildCleaned()
	m.styledLines = make([]string, len(m.cleaned.lines))
	cur := m.renderedCursor()
	for i, cl := range m.cleaned.lines {
		m.styledLines[i] = m.styleRow(i, cl, cur)
	}
	m.prevCursor = m.lineCursor
}

// styleRow colorizes one rendered row, applying the cursor highlight when the
// diff is focused and row is the rendered cursor row.
func (m *Model) styleRow(row int, cl cleanLine, cursorRow int) string {
	if m.focus == FocusDiff && row == cursorRow {
		width := m.vp.Width
		if width < 1 {
			return styles.DiffCursor.Render(cl.text)
		}
		// Truncate to the pane width first: a line longer than the pane would
		// otherwise wrap into multiple visual rows under .Width(), desyncing the
		// rendered-row↔raw-line mapping the cursor relies on.
		text := ansi.Truncate(cl.text, width, "…")
		return styles.DiffCursor.Width(width).Render(text)
	}
	return styleClean(cl)
}

// restyleCursorLines updates the highlight after a cursor move. It re-styles
// only the rendered rows whose highlight state changed — the new cursor row and
// the previously-highlighted row — then re-joins the cached styledLines. This
// keeps cursor movement O(1) in lipgloss work even for very large diffs.
func (m *Model) restyleCursorLines() {
	if len(m.styledLines) != len(m.cleaned.lines) {
		// Cache out of sync (e.g. width changed); rebuild fully.
		m.renderStyledLines()
	} else {
		prevRow := -1
		if m.prevCursor >= 0 && m.prevCursor < len(m.cleaned.rawToRendered) {
			prevRow = m.cleaned.rawToRendered[m.prevCursor]
		}
		curRow := m.renderedCursor()
		for _, row := range []int{prevRow, curRow} {
			if row >= 0 && row < len(m.cleaned.lines) {
				m.styledLines[row] = m.styleRow(row, m.cleaned.lines[row], curRow)
			}
		}
	}
	m.prevCursor = m.lineCursor
	if m.vpReady {
		m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	}
}

// refreshViewport re-renders styled content for the current width (used on
// resize). Safe to call when no diff is loaded.
func (m *Model) refreshViewport() {
	if !m.vpReady {
		return
	}
	if len(m.diffLines) == 0 {
		return
	}
	m.renderStyledLines()
	m.vp.SetContent(strings.Join(m.styledLines, "\n"))
	m.ensureCursorVisible()
}

// ensureCursorVisible scrolls the viewport so the (rendered) cursor row stays in
// view.
func (m *Model) ensureCursorVisible() {
	if !m.vpReady || m.focus != FocusDiff {
		return
	}
	row := m.renderedCursor()
	if row < 0 {
		return
	}
	top := m.vp.YOffset
	bottom := top + m.vp.Height - 1
	switch {
	case row < top:
		m.vp.SetYOffset(row)
	case row > bottom:
		m.vp.SetYOffset(row - m.vp.Height + 1)
	}
}

// ForwardViewport passes a key/message to the diff viewport (scrolling).
func (m *Model) ForwardViewport(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return cmd
}

// IsClean reports whether the working tree has no changes.
func (m *Model) IsClean() bool {
	return len(m.rows) == 0
}

// glyph maps a ChangedFile to its single-letter status glyph.
func glyph(g Group, f git.ChangedFile) string {
	if g == GroupUntracked {
		return "?"
	}
	code := f.Worktree
	if g == GroupStaged {
		code = f.Staged
	}
	switch code {
	case git.Added:
		return "A"
	case git.Modified, git.TypeChanged:
		return "M"
	case git.Deleted:
		return "D"
	case git.Renamed:
		return "R"
	case git.Copied:
		return "C"
	case git.Untracked:
		return "?"
	case git.Unmerged:
		return "U"
	default:
		return " "
	}
}

// View renders the file list beside the diff viewport plus a scrollbar. When the
// list is hidden, the diff (plus scrollbar) fills the whole width.
func (m *Model) View() string {
	if m.IsClean() {
		return m.renderCleanState()
	}

	diff := m.renderDiff()
	sb := m.renderScrollbar()
	if m.listHidden {
		return lipgloss.JoinHorizontal(lipgloss.Top, diff, sb)
	}

	list := lipgloss.NewStyle().Width(m.listWidth).Height(m.listHeight()).Render(m.renderList())
	leftCol := list
	if m.commitBarVisible() {
		leftCol = lipgloss.JoinVertical(lipgloss.Left, list, m.renderCommitBar())
	}
	gap := styles.Divider.Render(verticalBar(m.totalHeight))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, gap, diff, sb)
}

// commitBarLines is the number of screen rows the commit affordance occupies at
// the bottom of the file list: a separator rule plus the label/keycap line.
const commitBarLines = 2

// commitBarVisible reports whether the commit affordance is rendered: enabled,
// the list pane visible, and the pane tall enough to spare the rows.
func (m *Model) commitBarVisible() bool {
	return m.commitEnabled && !m.listHidden && m.totalHeight > commitBarLines
}

// listHeight is the height available to the scrollable file list, reserving the
// bottom rows for the commit affordance when it is shown.
func (m *Model) listHeight() int {
	h := m.totalHeight
	if m.commitBarVisible() {
		h -= commitBarLines
	}
	return h
}

// renderCommitBar renders the commit affordance pinned to the bottom of the file
// list: a separator rule then a label (staged count) with the ⇧C keycap
// right-aligned. The label brightens when there is something staged to commit.
func (m *Model) renderCommitBar() string {
	w := m.listWidth
	if w < 1 {
		return ""
	}
	rule := styles.Divider.Render(truncCells(strings.Repeat("─", w), w))

	staged := m.StagedCount()
	keycap := "⇧C"
	var labelText, label string
	if staged > 0 {
		labelText = fmt.Sprintf("Commit %d staged", staged)
		label = styles.GroupStaged.Render(labelText)
	} else {
		labelText = "Commit"
		label = styles.Folder.Render(labelText)
	}

	gap := w - ansi.StringWidth(labelText) - ansi.StringWidth(keycap)
	if gap < 1 {
		// Too narrow for the keycap: show just the (possibly truncated) label.
		return rule + "\n" + truncCells(label, w)
	}
	line := label + strings.Repeat(" ", gap) + styles.Key.Render(keycap)
	return rule + "\n" + line
}

// renderCleanState renders the friendly "working tree clean" empty state.
func (m *Model) renderCleanState() string {
	title := styles.GroupStaged.Render("✓ working tree clean")
	hint := styles.Clean.Render("nothing to commit — you're all caught up")
	msg := lipgloss.JoinVertical(lipgloss.Center, title, "", hint)
	return lipgloss.Place(maxi(m.totalWidth, 1), maxi(m.totalHeight, 1),
		lipgloss.Center, lipgloss.Center, msg)
}

// renderScrollbar draws a one-column vertical scrollbar for the diff viewport: a
// faint full-height track with a brighter thumb sized/positioned to the visible
// window. When the diff fits entirely it renders just the track.
func (m *Model) renderScrollbar() string {
	h := m.totalHeight
	if h < 1 {
		h = 1
	}
	total := len(m.styledLines)
	visible := m.vp.Height
	lines := make([]string, h)
	if total <= visible || total == 0 || visible < 1 {
		for i := range lines {
			lines[i] = styles.ScrollTrack.Render("│")
		}
		return strings.Join(lines, "\n")
	}
	thumb := h * visible / total
	if thumb < 1 {
		thumb = 1
	}
	if thumb > h {
		thumb = h
	}
	maxPos := h - thumb
	pos := 0
	if denom := total - visible; denom > 0 {
		pos = m.vp.YOffset * maxPos / denom
	}
	if pos < 0 {
		pos = 0
	}
	if pos > maxPos {
		pos = maxPos
	}
	for i := range lines {
		if i >= pos && i < pos+thumb {
			lines[i] = styles.ScrollThumb.Render("█")
		} else {
			lines[i] = styles.ScrollTrack.Render("│")
		}
	}
	return strings.Join(lines, "\n")
}

// listCell is one screen line of the file list: its rendered text plus the file
// row it belongs to (-1 for group headers and blank separators). A file whose
// path wraps contributes several cells, all tagged with the same row — so a click
// on any wrapped line selects that file. This is the single source of truth that
// renderList, ListLineToRow, and the scroll math all derive from, which is what
// keeps mouse hit-testing exact regardless of wrapping.
type listCell struct {
	text string
	row  int
}

func (m *Model) renderList() string {
	cells := m.listCells()
	h := m.listHeight()
	start := m.listOffset
	if h < 1 {
		h = len(cells)
		start = 0
	}
	if start > len(cells)-h {
		start = len(cells) - h
	}
	if start < 0 {
		start = 0
	}
	end := start + h
	if end > len(cells) {
		end = len(cells)
	}
	lines := make([]string, 0, end-start)
	for _, c := range cells[start:end] {
		lines = append(lines, c.text)
	}
	return strings.Join(lines, "\n")
}

// listCells builds every screen line of the list, in order: per non-empty group
// a blank separator (except before the first) + a header, then the group's nodes
// (folders + files) rendered with indentation. Each cell is tagged with its
// visible-node index (-1 for headers/blanks) so the mouse hit-test maps exactly.
func (m *Model) listCells() []listCell {
	bodyWidth := m.listWidth - selectBarWidth
	if bodyWidth < 1 {
		bodyWidth = m.listWidth
	}
	nodes := m.nodes()
	var cells []listCell
	lastGroup := Group(-1)
	for ni, n := range nodes {
		if n.group != lastGroup {
			if lastGroup != -1 {
				cells = append(cells, listCell{text: "", row: -1})
			}
			cells = append(cells, listCell{text: renderGroupHeader(n.group, m.groupFileCount(n.group), m.listWidth), row: -1})
			lastGroup = n.group
		}
		for _, ln := range m.renderNodeLines(ni, n, bodyWidth) {
			cells = append(cells, listCell{text: ln, row: ni})
		}
	}
	return cells
}

// groupFileCount returns the number of files in group g.
func (m *Model) groupFileCount(g Group) int {
	n := 0
	for _, r := range m.rows {
		if r.Group == g {
			n++
		}
	}
	return n
}

// selectBarWidth is the width of the left state-bar column on each row.
const selectBarWidth = 1

// indentStep is the per-tree-level indentation in columns.
const indentStep = 2

// splitPath splits a slash path into its directory components and base name.
func splitPath(p string) (dirs []string, base string) {
	if p == "" {
		return nil, ""
	}
	parts := strings.Split(p, "/")
	return parts[:len(parts)-1], parts[len(parts)-1]
}

// clampIndent caps an indent so a deep tree on a narrow pane still leaves room
// for a few label columns (preventing 1-column-at-a-time wrapping).
func clampIndent(indent, bodyWidth int) int {
	maxIndent := bodyWidth - 6
	if maxIndent < 0 {
		maxIndent = 0
	}
	if indent > maxIndent {
		indent = maxIndent
	}
	if indent < 0 {
		indent = 0
	}
	return indent
}

// indentPad builds the left indentation: a dim guide rail (│) per level on normal
// rows, or plain spaces on selected/hovered rows (where the background already
// signals the row). Always exactly `indent` display columns wide.
func (m *Model) indentPad(depth, indent int, highlighted bool) string {
	if indent <= 0 {
		return ""
	}
	if highlighted {
		return strings.Repeat(" ", indent)
	}
	rail := ansi.Truncate(strings.Repeat("│ ", depth), indent, "")
	if w := ansi.StringWidth(rail); w < indent {
		rail += strings.Repeat(" ", indent-w)
	}
	return styles.Divider.Render(rail)
}

// renderNodeLines renders one visible node (folder or file) as one or more screen
// lines: a left state bar (▌ selected / ▎ diff-focused / blank), the indent rail,
// then a folder (caret + name/ + count) or a file (glyph + label, wrapping onto
// aligned continuation lines). The state bar repeats down wrapped lines.
func (m *Model) renderNodeLines(ni int, n node, bodyWidth int) []string {
	selected := ni == m.cursor
	hovered := ni == m.hoverRow
	var style lipgloss.Style
	var bar string
	plainText := false // selected rows render plain text for legible contrast
	switch {
	case selected && m.focus != FocusDiff:
		style, bar, plainText = styles.SelectedRow, styles.Branch.Render("▌"), true
	case selected:
		style, bar, plainText = styles.SelectedRowInactive, styles.HeaderMuted.Render("▎"), true
	case hovered:
		style, bar = styles.HoverRow, " "
	default:
		style, bar = lipgloss.NewStyle(), " "
	}

	indent := clampIndent(n.depth*indentStep, bodyWidth)
	rail := m.indentPad(n.depth, indent, selected || hovered)

	if n.kind == folderNode {
		caret := "▾ "
		label := n.label + "/"
		if n.collapsed {
			caret = "▸ "
			if n.count > 0 {
				label += fmt.Sprintf(" (%d)", n.count)
			}
		}
		content := caret + label
		if !plainText {
			content = styles.Folder.Render(content)
		}
		avail := bodyWidth - indent
		if avail < 1 {
			avail = 1
		}
		content = truncCells(content, avail)
		return []string{bar + style.Width(bodyWidth).Render(rail+content)}
	}

	// File node. Build the styled label once so wrapping (via ansi.Cut) preserves
	// styling across continuation lines.
	r := m.rows[n.fileRow]
	gl := glyph(r.Group, r.File)
	var labelStyled string
	switch {
	case plainText:
		labelStyled = n.label
	case !m.treeMode:
		// Flat list: dim the directory portion, keep the basename bright.
		dirs, base := splitPath(n.key)
		if len(dirs) > 0 {
			labelStyled = styles.Folder.Render(strings.Join(dirs, "/")+"/") + base
		} else {
			labelStyled = base
		}
	case r.Group == GroupUntracked:
		labelStyled = styles.UntrackedRow.Render(n.label)
	default:
		labelStyled = n.label
	}

	chunkW := bodyWidth - indent - 2
	if chunkW < 1 {
		chunkW = 1
	}
	chunks := chunkByWidth(labelStyled, chunkW)
	out := make([]string, 0, len(chunks))
	for j, ch := range chunks {
		var glyphPart string
		switch {
		case j > 0:
			glyphPart = "  " // continuation aligns under the label
		case plainText:
			glyphPart = gl + " "
		default:
			glyphPart = styles.GlyphStyle(gl).Render(gl) + " "
		}
		out = append(out, bar+style.Width(bodyWidth).Render(rail+glyphPart+ch))
	}
	return out
}

// chunkByWidth splits s into substrings each at most w display columns wide
// (cell-accurate, so it matches what the renderer/hit-test see). Always returns
// at least one chunk.
func chunkByWidth(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	total := ansi.StringWidth(s)
	if total == 0 {
		return []string{""}
	}
	var chunks []string
	for off := 0; off < total; off += w {
		chunks = append(chunks, ansi.Cut(s, off, off+w))
	}
	return chunks
}

// cursorCellRange returns the first and last screen-line indices spanned by the
// selected row (or -1,-1) and the total cell count. Used by the scroll math.
func (m *Model) cursorCellRange() (first, last, total int) {
	cells := m.listCells()
	first, last = -1, -1
	for idx, c := range cells {
		if c.row == m.cursor {
			if first < 0 {
				first = idx
			}
			last = idx
		}
	}
	return first, last, len(cells)
}

// ensureSelectedVisible adjusts listOffset so the selected file row stays within
// the visible list window (the file pane scrolls to follow the selection). When
// the selected row is taller than the pane it pins to the row's first line.
func (m *Model) ensureSelectedVisible() {
	h := m.listHeight()
	if h < 1 {
		return
	}
	first, last, total := m.cursorCellRange()
	if first < 0 {
		m.listOffset = 0
		return
	}
	if first < m.listOffset {
		m.listOffset = first
	} else if last >= m.listOffset+h {
		m.listOffset = last - h + 1
		if m.listOffset > first {
			m.listOffset = first // row taller than the pane: show its start
		}
	}
	if maxOffset := total - h; m.listOffset > maxOffset {
		m.listOffset = maxOffset
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// truncCells truncates s (ANSI-aware) to at most w display columns, appending an
// ellipsis when it cuts. Used for group-header banners (which never wrap).
func truncCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// renderGroupHeader renders a distinct, per-group header with a count badge,
// e.g. "Unstaged (3)". The Unstaged and Untracked headers render as full-width
// background banners (padded to width) so the three groups are unmistakable;
// Staged stays a plain colored label.
func renderGroupHeader(g Group, count int, width int) string {
	badge := fmt.Sprintf("(%d)", count)
	banner := func(style lipgloss.Style) string {
		text := groupName(g) + " " + badge
		if width > 0 {
			// Truncate before padding so a narrow pane can't make the banner wrap.
			text = truncCells(text, width)
			style = style.Width(width)
		}
		return style.Render(text)
	}
	switch g {
	case GroupUnstaged:
		return banner(styles.GroupUnstaged)
	case GroupUntracked:
		return banner(styles.GroupUntracked)
	default: // GroupStaged
		return styles.GroupStaged.Render(groupName(g)) + " " + styles.GroupBadge.Render(badge)
	}
}

// ListLineToRow maps an on-screen body line (0 at the top of the list pane) to a
// file row index, accounting for the scroll offset and wrapped rows. Returns
// false for header / blank / out-of-range lines. It derives from the same
// listCells the list is rendered from, so the mapping is always exact.
func (m *Model) ListLineToRow(line int) (int, bool) {
	cells := m.listCells()
	idx := line + m.listOffset
	if idx < 0 || idx >= len(cells) {
		return -1, false
	}
	if cells[idx].row < 0 {
		return -1, false
	}
	return cells[idx].row, true
}

func (m *Model) renderDiff() string {
	if !m.vpReady {
		return ""
	}
	return m.vp.View()
}

func groupName(g Group) string {
	switch g {
	case GroupStaged:
		return "Staged"
	case GroupUnstaged:
		return "Unstaged"
	default:
		return "Untracked"
	}
}

func verticalBar(h int) string {
	if h < 1 {
		h = 1
	}
	return strings.TrimRight(strings.Repeat("│\n", h), "\n")
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
