// Package ui implements the Bubble Tea UI layer for gui. The root App model
// owns the git.Service and config.Dispatcher, routes keys through the leader
// chord dispatcher, and renders the active view (diff / branch / help). All
// blocking git calls run inside tea.Cmds; Update never blocks.
package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/selton/gui/internal/config"
	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui/branchpanel"
	"github.com/selton/gui/internal/ui/diffview"
	"github.com/selton/gui/internal/ui/help"
	"github.com/selton/gui/internal/ui/styles"
)

// view enumerates the active top-level view.
type view int

const (
	viewDiff view = iota
	viewBranch
	viewHelp
)

// ---- messages returned by git commands ----

type statusMsg struct {
	status *git.Status
	err    error
}

type remoteMsg struct {
	remote *git.Remote
	err    error
}

type diffMsg struct {
	path string
	raw  string
	err  error
}

// bgDiffMsg is a tick-driven diff fetch; it is applied with SetDiffPreserving so
// an unchanged diff keeps the user's cursor/scroll.
type bgDiffMsg struct {
	path string
	raw  string
	err  error
}

type branchesMsg struct {
	branches []git.Branch
	err      error
}

// quitCheckMsg is the result of probing for an in-progress git operation when
// the user asks to quit (FR-4: warn before quitting mid-operation).
type quitCheckMsg struct {
	inProgress bool
}

// mutationDoneMsg is emitted after a stage/unstage/discard/checkout/etc.
type mutationDoneMsg struct {
	err error
	// origin describes what produced this, so the handler can react (e.g. a
	// delete that failed because the branch is unmerged).
	origin mutationOrigin
	name   string // branch name for branch mutations
}

type mutationOrigin int

const (
	originFile mutationOrigin = iota
	originBranch
	originDelete
)

// App is the root Bubble Tea model.
type App struct {
	repo       *git.Service
	dispatcher *config.Dispatcher

	status *git.Status
	remote *git.Remote

	active view
	diff   diffview.Model
	branch branchpanel.Model
	help   help.Model

	// pending confirmation for a destructive file op (discard).
	confirm *confirmState

	// undo is a LIFO stack of discarded changes that ctrl+r can recover. Capped
	// at undoStackCap; oldest entries are dropped when the cap is exceeded.
	undo []undoEntry

	// listWidth is the desired absolute width of the file list pane; >/< adjust
	// it and it is clamped to sane minimums on every layout.
	listWidth int

	// forceDiffReload makes the next status-driven diff refresh reload the
	// current file's diff even when the selected path is unchanged (needed after
	// a mutation that altered the file's content in place, e.g. a hunk discard).
	forceDiffReload bool

	// autoRefresh toggles the background polling tick (ctrl+t). Default on.
	autoRefresh bool
	// statusFP is the fingerprint of the last applied status; a tick whose
	// fingerprint matches causes no re-render and no diff re-fetch.
	statusFP string
	// bgErrShown is set once a background-refresh error has been surfaced as a
	// toast, so the same failure is not re-toasted on every subsequent tick. It
	// is cleared on the next successful background status.
	bgErrShown bool

	toast  string
	width  int
	height int
}

// autoRefreshInterval is the polling cadence for the background refresh tick.
// Kept under 1s for low perceived latency while staying cheap for ≤500-file
// repos (per FR-2). The next tick is chained off the previous tick's completion,
// so a slow Status() never overlaps or queues itself.
const autoRefreshInterval = 750 * time.Millisecond

// undoStackCap bounds the in-memory recover stack. Older entries beyond this are
// dropped (recovery is best-effort and lost on quit).
const undoStackCap = 50

// undoEntry is one recoverable discarded change. restore re-applies it.
type undoEntry struct {
	label   string
	restore func(*git.Service) error
}

type confirmState struct {
	prompt string
	// onYes is the action to perform when accepted.
	onYes func(*App) tea.Cmd
}

// New constructs the root model. It satisfies the contract expected by main.go:
// New(repo *git.Service) returns a tea.Model.
func New(repo *git.Service) tea.Model {
	return &App{
		repo:       repo,
		dispatcher: config.NewDispatcher(config.DefaultKeymap()),
		active:     viewDiff,
		diff:       diffview.New(),
		branch:      branchpanel.New(),
		help:        help.New(),
		listWidth:   defaultListWidth,
		autoRefresh: true,
	}
}

// Init fires the initial status + remote loads and starts the auto-refresh tick.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.loadStatusCmd(), a.loadRemoteCmd(), scheduleTick())
}

// ---- commands ----

func (a *App) loadStatusCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		s, err := repo.Status()
		return statusMsg{status: s, err: err}
	}
}

// ---- auto-refresh tick ----

// tickMsg fires on every poll interval. It carries no payload; handling it
// either kicks off a background Status() fetch (when auto-refresh is on) or just
// reschedules itself (when off).
type tickMsg struct{}

// bgStatusMsg is the result of a background (tick-driven) Status() fetch. It is
// distinct from statusMsg so the handler can apply change-detection and
// overlay-safe reconciliation, and so it can chain the next tick off completion
// — guaranteeing a slow Status() never overlaps or queues another.
type bgStatusMsg struct {
	status *git.Status
	err    error
}

// scheduleTick returns a command that emits a tickMsg after one interval. The
// tick chain is: tickMsg -> bgStatusCmd -> bgStatusMsg -> scheduleTick. Because
// the next tick is only scheduled when the previous fetch's result is handled,
// there is never more than one Status() in flight.
func scheduleTick() tea.Cmd {
	return tea.Tick(autoRefreshInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) bgStatusCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		s, err := repo.Status()
		return bgStatusMsg{status: s, err: err}
	}
}

// handleTick handles one poll tick. When auto-refresh is on it kicks off a
// background Status() fetch (whose result reschedules the next tick). When off,
// it simply reschedules itself so the chain survives a later toggle-on.
func (a *App) handleTick() tea.Cmd {
	if !a.autoRefresh {
		return scheduleTick()
	}
	return a.bgStatusCmd()
}

// handleBgStatus applies a tick-driven status, then always reschedules the next
// tick (chaining off completion → no overlap). It is careful not to disrupt the
// user: nothing changes when the fingerprint is unchanged, and disruptive view
// updates are skipped while an overlay/input is active.
func (a *App) handleBgStatus(msg bgStatusMsg) (tea.Model, tea.Cmd) {
	next := scheduleTick()
	if msg.err != nil {
		// Surface a background failure at most once until the next success.
		if !a.bgErrShown {
			a.toast = "auto-refresh: " + msg.err.Error()
			a.bgErrShown = true
		}
		return a, next
	}
	a.bgErrShown = false

	fp := statusFingerprint(msg.status)
	if fp == a.statusFP {
		// No change: no re-render, no diff re-fetch.
		return a, next
	}

	// While an overlay or text input is open, never disturb the view: keep the
	// raw status data current underneath, but defer reconciling the diff panel
	// (selection/cursor/diff) until the overlay closes. We intentionally do NOT
	// update statusFP here, so the change is re-applied on the first tick after
	// the overlay closes.
	if a.overlayActive() {
		a.status = msg.status
		return a, next
	}

	a.status = msg.status
	a.statusFP = fp
	a.diff.SetStatus(msg.status)
	return a, tea.Batch(next, a.bgRefreshDiffCmd())
}

// bgRefreshDiffCmd re-fetches the selected file's diff for a background refresh.
// Unlike refreshDiffCmd it always re-fetches the selected path (so changed-in-
// place content is picked up) but the result is applied via SetDiffPreserving,
// which keeps the line cursor and scroll when the diff text is unchanged.
func (a *App) bgRefreshDiffCmd() tea.Cmd {
	row, ok := a.diff.Selected()
	if !ok {
		return nil
	}
	repo := a.repo
	path := row.File.Path
	staged := row.Group == diffview.GroupStaged
	return func() tea.Msg {
		raw, err := repo.Diff(path, staged)
		return bgDiffMsg{path: path, raw: raw, err: err}
	}
}

// statusFingerprint computes a cheap, order-sensitive signature of a Status:
// branch + upstream + ahead/behind, plus each group's (path, status code). Two
// snapshots that produce the same string are treated as "no change" — no
// re-render, no diff re-fetch. A nil status yields a stable empty marker.
func statusFingerprint(s *git.Status) string {
	if s == nil {
		return "<nil>"
	}
	var b strings.Builder
	b.WriteString(s.Branch)
	b.WriteByte('\x1f')
	b.WriteString(s.Upstream)
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(s.Ahead))
	b.WriteByte('\x1f')
	b.WriteString(strconv.Itoa(s.Behind))
	writeGroup := func(tag byte, files []git.ChangedFile) {
		b.WriteByte('\x1e')
		b.WriteByte(tag)
		for _, f := range files {
			b.WriteByte('\x1f')
			b.WriteString(f.Path)
			b.WriteByte(':')
			b.WriteString(f.OrigPath)
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(int(f.Staged)))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(int(f.Worktree)))
		}
	}
	writeGroup('S', s.Staged)
	writeGroup('U', s.Unstaged)
	writeGroup('?', s.Untracked)
	return b.String()
}

// overlayActive reports whether an overlay or text input is open such that a
// background refresh must not disturb the view (steal focus, close it, or
// interrupt typing). The underlying status data may still be refreshed.
func (a *App) overlayActive() bool {
	if a.confirm != nil {
		return true
	}
	if a.active == viewHelp {
		return true
	}
	if a.active == viewBranch {
		return true
	}
	return false
}

func (a *App) loadRemoteCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		r, err := repo.OriginRemote()
		return remoteMsg{remote: r, err: err}
	}
}

func (a *App) loadDiffCmd(path string, staged bool) tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		raw, err := repo.Diff(path, staged)
		return diffMsg{path: path, raw: raw, err: err}
	}
}

// quitCheckCmd probes for an in-progress rebase off the UI goroutine so the
// quit handler can warn before exiting mid-operation.
func (a *App) quitCheckCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		inProg, _ := repo.RebaseInProgress()
		return quitCheckMsg{inProgress: inProg}
	}
}

func (a *App) loadBranchesCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		bs, err := repo.Branches()
		return branchesMsg{branches: bs, err: err}
	}
}

func fileMutationCmd(fn func() error) tea.Cmd {
	return func() tea.Msg {
		return mutationDoneMsg{err: fn(), origin: originFile}
	}
}

func branchMutationCmd(name string, origin mutationOrigin, fn func() error) tea.Cmd {
	return func() tea.Msg {
		return mutationDoneMsg{err: fn(), origin: origin, name: name}
	}
}

// ---- Update ----

// Update is the single root reducer.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.applyLayout()
		return a, nil

	case statusMsg:
		if msg.err != nil {
			a.toast = "status: " + msg.err.Error()
			return a, nil
		}
		a.status = msg.status
		a.statusFP = statusFingerprint(msg.status)
		a.diff.SetStatus(msg.status)
		return a, a.refreshDiffCmd()

	case tickMsg:
		return a, a.handleTick()

	case bgStatusMsg:
		return a.handleBgStatus(msg)

	case remoteMsg:
		if msg.err != nil {
			if !errors.Is(msg.err, git.ErrNoRemote) {
				a.toast = "remote: " + msg.err.Error()
			}
			a.remote = nil
			return a, nil
		}
		a.remote = msg.remote
		return a, nil

	case diffMsg:
		if msg.err != nil {
			a.toast = "diff: " + msg.err.Error()
			return a, nil
		}
		a.diff.SetDiff(msg.path, msg.raw)
		return a, nil

	case bgDiffMsg:
		if msg.err != nil {
			// Background diff errors are non-fatal and not toasted (the path may
			// have just vanished); the next status tick reconciles.
			return a, nil
		}
		// Only apply if the selection still points at this path (it may have
		// moved between the fetch firing and completing).
		if a.diff.SelectedPath() == msg.path {
			a.diff.SetDiffPreserving(msg.path, msg.raw)
		}
		return a, nil

	case branchesMsg:
		if msg.err != nil {
			a.branch.SetError("branches: " + msg.err.Error())
			return a, nil
		}
		a.branch.SetBranches(msg.branches)
		return a, nil

	case mutationDoneMsg:
		return a.handleMutationDone(msg)

	case needsForceDeleteMsg:
		a.branch.EscalateToForceDelete(msg.name)
		return a, nil

	case quitCheckMsg:
		if msg.inProgress {
			a.confirm = &confirmState{
				prompt: "A git operation (rebase) is in progress. Quit anyway?",
				onYes:  func(*App) tea.Cmd { return tea.Quit },
			}
			return a, nil
		}
		return a, tea.Quit

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

func (a *App) handleMutationDone(msg mutationDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		switch msg.origin {
		case originBranch, originDelete:
			a.branch.SetError(msg.err.Error())
		default:
			a.toast = msg.err.Error()
		}
		return a, nil
	}
	// Success: refresh everything relevant. File mutations can change the
	// selected file's diff in place, so force the next diff refresh to reload.
	if msg.origin == originFile {
		a.forceDiffReload = true
	}
	cmds := []tea.Cmd{a.loadStatusCmd()}
	if a.active == viewBranch {
		cmds = append(cmds, a.loadBranchesCmd())
	}
	a.toast = ""
	return a, tea.Batch(cmds...)
}

// handleKey routes a key. Overlays (confirm/help/branch) capture keys first.
func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global hard quit.
	if key == "ctrl+c" {
		return a, tea.Quit
	}

	// Confirm dialog captures everything.
	if a.confirm != nil {
		switch key {
		case "y", "Y", "enter":
			fn := a.confirm.onYes
			a.confirm = nil
			if fn != nil {
				return a, fn(a)
			}
		case "n", "N", "esc":
			a.confirm = nil
		}
		return a, nil
	}

	switch a.active {
	case viewHelp:
		if key == "?" || key == "esc" || key == "q" {
			a.active = viewDiff
		}
		return a, nil
	case viewBranch:
		return a.handleBranchKey(msg)
	}

	// viewDiff: route through the dispatcher.
	return a.handleDiffKey(msg)
}

// normalizeKey maps Bubble Tea's key strings onto the names the config keymap
// uses. Notably Bubble Tea reports the space key as " ", while the default
// keymap's leader is "space".
func normalizeKey(s string) string {
	if s == " " {
		return "space"
	}
	return s
}

func (a *App) handleDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	action := a.dispatcher.Resolve(normalizeKey(msg.String()))
	switch action {
	case config.ActQuit:
		// FR-4: warn before quitting if a git operation is in progress.
		return a, a.quitCheckCmd()
	case config.ActHelp:
		a.active = viewHelp
		return a, nil
	case config.ActBranchPanel:
		a.active = viewBranch
		a.branch.Reset()
		return a, a.loadBranchesCmd()
	case config.ActRefresh:
		a.toast = ""
		return a, a.loadStatusCmd()
	case config.ActToggleAutoRefresh:
		a.autoRefresh = !a.autoRefresh
		if a.autoRefresh {
			a.toast = "auto-refresh: on"
		} else {
			a.toast = "auto-refresh: off"
		}
		return a, nil
	case config.ActDown:
		a.diff.CursorDown()
		return a, a.refreshDiffCmd()
	case config.ActUp:
		a.diff.CursorUp()
		return a, a.refreshDiffCmd()
	case config.ActConfirm:
		// Focus the diff pane so j/k move the line cursor, ensuring it's loaded.
		a.diff.FocusDiff()
		return a, a.refreshDiffCmd()
	case config.ActCancel:
		// Return focus to the file list.
		a.diff.FocusList()
		return a, nil
	case config.ActHunkNext:
		a.diff.HunkNext()
		return a, nil
	case config.ActHunkPrev:
		a.diff.HunkPrev()
		return a, nil
	case config.ActPaneGrow:
		a.growDiff()
		return a, nil
	case config.ActPaneShrink:
		a.shrinkDiff()
		return a, nil
	case config.ActStageToggle:
		return a, a.stageToggleCmd()
	case config.ActUndo:
		return a.discardHunk()
	case config.ActUndoFile:
		return a.discardWholeFile()
	case config.ActRecover:
		return a.recover()
	}
	// Unhandled key: let the diff viewport scroll (pgup/pgdn/etc).
	return a, a.diff.ForwardViewport(msg)
}

// refreshDiffCmd loads the diff for the currently selected file if it differs
// from what's shown.
func (a *App) refreshDiffCmd() tea.Cmd {
	row, ok := a.diff.Selected()
	if !ok {
		return nil
	}
	if a.diff.DiffPath() == row.File.Path && !a.forceDiffReload {
		return nil
	}
	a.forceDiffReload = false
	staged := row.Group == diffview.GroupStaged
	return a.loadDiffCmd(row.File.Path, staged)
}

func (a *App) stageToggleCmd() tea.Cmd {
	row, ok := a.diff.Selected()
	if !ok {
		return nil
	}
	repo := a.repo
	path := row.File.Path
	if row.Group == diffview.GroupStaged {
		return fileMutationCmd(func() error { return repo.Unstage(path) })
	}
	return fileMutationCmd(func() error { return repo.Stage(path) })
}

// pushUndo records a recoverable discarded change on the LIFO stack, dropping
// the oldest entry when the cap is exceeded.
func (a *App) pushUndo(e undoEntry) {
	a.undo = append(a.undo, e)
	if len(a.undo) > undoStackCap {
		a.undo = a.undo[len(a.undo)-undoStackCap:]
	}
}

// discardHunk (u) discards only the hunk under the diff line cursor. Untracked
// files have no real hunks, so they fall through to the whole-file path.
func (a *App) discardHunk() (tea.Model, tea.Cmd) {
	row, ok := a.diff.Selected()
	if !ok {
		return a, nil
	}
	// Untracked files: no hunks to isolate — defer to the whole-file discard.
	if row.Group == diffview.GroupUntracked {
		return a.discardWholeFile()
	}

	hunks := a.diff.Hunks()
	idx := git.HunkAtLine(hunks, a.diff.LineCursor())
	if idx < 0 {
		// Cursor not inside a hunk (header line, or no hunks at all).
		a.toast = "no hunk under cursor — move into a hunk or press U to discard the file"
		return a, nil
	}
	hunk := hunks[idx]
	cached := row.Group == diffview.GroupStaged
	repo := a.repo
	patch := hunk.Patch

	// Record recovery (forward-apply) before discarding.
	a.pushUndo(undoEntry{
		label:   "hunk in " + row.File.Path,
		restore: func(s *git.Service) error { return s.ApplyPatch(patch, false, cached) },
	})

	a.toast = ""
	return a, fileMutationCmd(func() error { return repo.ApplyPatch(patch, true, cached) })
}

// discardWholeFile (U) discards every worktree change for the selected file,
// behind a confirmation dialog, capturing recovery state first.
func (a *App) discardWholeFile() (tea.Model, tea.Cmd) {
	row, ok := a.diff.Selected()
	if !ok {
		return a, nil
	}
	path := row.File.Path
	untracked := row.Group == diffview.GroupUntracked
	a.confirm = &confirmState{
		prompt: fmt.Sprintf("Discard all changes in '%s'? (y/n)", path),
		onYes: func(app *App) tea.Cmd {
			return app.discardFileCmd(path, untracked)
		},
	}
	return a, nil
}

// discardFileCmd builds the command that discards the whole file and records a
// recovery entry. Recovery state (full diff or file bytes) is captured inside
// the command so it reflects the on-disk state at discard time.
func (a *App) discardFileCmd(path string, untracked bool) tea.Cmd {
	repo := a.repo
	if untracked {
		// Capture the file bytes so recover can rewrite them.
		data, _ := os.ReadFile(filepath.Join(repo.Root(), path))
		a.pushUndo(undoEntry{
			label: "untracked " + path,
			restore: func(s *git.Service) error {
				return os.WriteFile(filepath.Join(s.Root(), path), data, 0o644)
			},
		})
		return fileMutationCmd(func() error { return repo.DiscardUntracked(path) })
	}
	// Tracked: capture the full unstaged diff so recover can forward-apply it.
	fullDiff, _ := repo.Diff(path, false)
	if strings.TrimSpace(fullDiff) != "" {
		a.pushUndo(undoEntry{
			label:   path,
			restore: func(s *git.Service) error { return s.ApplyPatch(fullDiff, false, false) },
		})
	}
	return fileMutationCmd(func() error { return repo.Discard(path) })
}

// recover (ctrl+r) pops the most recent discarded change and re-applies it.
func (a *App) recover() (tea.Model, tea.Cmd) {
	if len(a.undo) == 0 {
		a.toast = "nothing to recover"
		return a, nil
	}
	entry := a.undo[len(a.undo)-1]
	a.undo = a.undo[:len(a.undo)-1]
	repo := a.repo
	a.toast = ""
	return a, fileMutationCmd(func() error {
		if err := entry.restore(repo); err != nil {
			return fmt.Errorf("recover %s: %w", entry.label, err)
		}
		return nil
	})
}

func (a *App) handleBranchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	intent, cmd := a.branch.Update(msg)
	switch intent.Kind {
	case branchpanel.IntentClose:
		a.active = viewDiff
		return a, nil
	case branchpanel.IntentCheckout:
		repo := a.repo
		name := intent.Name
		return a, tea.Batch(cmd, branchMutationCmd(name, originBranch, func() error { return repo.Checkout(name) }))
	case branchpanel.IntentCreate:
		repo := a.repo
		name, start := intent.Name, intent.Start
		return a, tea.Batch(cmd, branchMutationCmd(name, originBranch, func() error { return repo.CreateBranch(name, start) }))
	case branchpanel.IntentDelete:
		repo := a.repo
		name, force := intent.Name, intent.Force
		return a, tea.Batch(cmd, a.deleteBranchCmd(repo, name, force))
	case branchpanel.IntentRebase:
		repo := a.repo
		onto := intent.Name
		return a, tea.Batch(cmd, branchMutationCmd(onto, originBranch, func() error { return repo.Rebase(onto) }))
	}
	return a, cmd
}

// deleteBranchCmd deletes a branch; on a non-force delete it first checks
// IsMerged so the panel can escalate to a force-confirm.
func (a *App) deleteBranchCmd(repo *git.Service, name string, force bool) tea.Cmd {
	if force {
		return branchMutationCmd(name, originBranch, func() error { return repo.DeleteBranch(name, true) })
	}
	return func() tea.Msg {
		merged, err := repo.IsMerged(name)
		if err != nil {
			return mutationDoneMsg{err: err, origin: originDelete, name: name}
		}
		if !merged {
			// Signal escalation via a typed message handled below.
			return needsForceDeleteMsg{name: name}
		}
		return mutationDoneMsg{err: repo.DeleteBranch(name, false), origin: originBranch, name: name}
	}
}

// needsForceDeleteMsg asks the branch panel to confirm a force delete.
type needsForceDeleteMsg struct{ name string }

// View renders the active view with header + body + footer.
func (a *App) View() string {
	if a.width == 0 {
		return "loading…"
	}
	header := a.renderHeader()
	footer := a.renderFooter()

	var body string
	switch a.active {
	case viewHelp:
		body = a.help.View(a.width, a.bodyHeight())
	case viewBranch:
		body = a.branch.View(a.width, a.bodyHeight())
	default:
		body = a.diff.View()
	}
	body = lipgloss.NewStyle().Height(a.bodyHeight()).MaxHeight(a.bodyHeight()).Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (a *App) bodyHeight() int {
	h := a.height - 2 // header + footer
	if h < 1 {
		h = 1
	}
	return h
}

// Layout / resize constants.
const (
	// defaultListWidth is the initial file-list pane width before resizing.
	defaultListWidth = 32
	// paneResizeStep is how many columns >/< move the split per press.
	paneResizeStep = 4
)

// applyLayout recomputes the diff panel layout for the current size + split.
// It clamps the desired list width to sane minimums (delegated to diffview).
func (a *App) applyLayout() {
	a.listWidth = diffview.ClampListWidth(a.width, a.listWidth)
	a.diff.SetSize(a.width, a.bodyHeight(), a.listWidth)
}

// growDiff shrinks the file list (grows the diff pane) by one step, clamped.
func (a *App) growDiff() {
	a.listWidth = diffview.ClampListWidth(a.width, a.listWidth-paneResizeStep)
	a.applyLayout()
}

// shrinkDiff grows the file list (shrinks the diff pane) by one step, clamped.
func (a *App) shrinkDiff() {
	a.listWidth = diffview.ClampListWidth(a.width, a.listWidth+paneResizeStep)
	a.applyLayout()
}

func (a *App) renderHeader() string {
	var segs []string
	branch := "(detached)"
	ahead, behind := 0, 0
	if a.status != nil {
		if a.status.Branch != "" {
			branch = a.status.Branch
		}
		ahead, behind = a.status.Ahead, a.status.Behind
	}
	b := styles.Branch.Render(branch)
	if a.status != nil && a.status.Upstream != "" {
		b += " " + styles.HeaderMuted.Render(fmt.Sprintf("↑%d↓%d", ahead, behind))
	}
	segs = append(segs, b)

	if a.remote != nil {
		var ident string
		if a.remote.Owner != "" && a.remote.Repo != "" {
			ident = a.remote.Owner + "/" + a.remote.Repo
		} else {
			ident = a.remote.Host
		}
		if ident != "" {
			segs = append(segs, styles.HeaderMuted.Render(ident))
		}
	}

	if root := a.repo.Root(); root != "" {
		segs = append(segs, styles.HeaderMuted.Render(root))
	}

	sep := styles.HeaderSep.Render(" · ")
	return styles.Header.Render(strings.Join(segs, sep))
}

func (a *App) renderFooter() string {
	auto := "on"
	if !a.autoRefresh {
		auto = "off"
	}
	hint := "j/k move · enter focus diff · u hunk · U file · ctrl+r recover · } { hunk · < > resize · s stage · r refresh · ctrl+t auto:" + auto + " · space b branches · ? help · q quit"
	if a.confirm != nil {
		return styles.Confirm.Render(a.confirm.prompt)
	}
	line := styles.Hint.Render(hint)
	if a.toast != "" {
		line = styles.Toast.Render(a.toast)
	}
	return styles.Header.Render(line)
}
