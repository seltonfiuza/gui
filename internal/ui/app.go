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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/seltonfiuza/gui/internal/config"
	"github.com/seltonfiuza/gui/internal/git"
	"github.com/seltonfiuza/gui/internal/github"
	"github.com/seltonfiuza/gui/internal/ui/branchpanel"
	"github.com/seltonfiuza/gui/internal/ui/diffview"
	"github.com/seltonfiuza/gui/internal/ui/help"
	"github.com/seltonfiuza/gui/internal/ui/prlist"
	"github.com/seltonfiuza/gui/internal/ui/styles"
	"github.com/seltonfiuza/gui/internal/ui/themepicker"
)

// view enumerates the active top-level view.
type view int

const (
	viewDiff view = iota
	viewBranch
	viewHelp
	viewTheme
	viewPR
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

type prsMsg struct {
	prs []github.PR
	err error
}

type prDetailMsg struct {
	pr   github.PR
	diff string
	err  error
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
	cfg        config.Config

	status *git.Status
	remote *git.Remote

	active view
	diff   diffview.Model
	branch branchpanel.Model
	help   help.Model
	theme  themepicker.Model
	pr     prlist.Model

	// dragging is true while the user holds the mouse on the divider to resize.
	dragging bool

	// pending confirmation for a destructive file op (discard).
	confirm *confirmState

	// commit is the active commit-message dialog, or nil when closed.
	commit *commitState

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

	toast   string
	version string // build version, shown bottom-left in the footer
	width   int
	height  int
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

// commitState is the modal commit-message dialog: a single-line text input plus
// the staged-file count shown for context. enter commits, esc cancels.
type commitState struct {
	input  textinput.Model
	staged int
}

// commitDoneMsg is emitted after a commit attempt completes.
type commitDoneMsg struct{ err error }

// New constructs the root model. version is the build version shown in the
// footer (pass "dev" for local builds). It loads persisted config and applies
// the saved theme so the UI launches in the user's preferred colors.
func New(repo *git.Service, version string) tea.Model {
	cfg, _ := config.Load()
	styles.SetTheme(cfg.Theme)
	if version == "" {
		version = "dev"
	}
	return &App{
		repo:        repo,
		cfg:         cfg,
		dispatcher:  config.NewDispatcher(config.DefaultKeymap()),
		active:      viewDiff,
		diff:        diffview.New(),
		branch:      branchpanel.New(),
		help:        help.New(),
		theme:       themepicker.New(),
		pr:          prlist.New(),
		listWidth:   defaultListWidth,
		autoRefresh: true,
		version:     version,
	}
}

// Init fires the initial status + remote loads, starts the auto-refresh tick,
// and enables all-motion mouse tracking so clicks/scroll/drag AND hover (motion
// with no button) are delivered to Update — the latter drives hover highlights.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.loadStatusCmd(), a.loadRemoteCmd(), scheduleTick(), tea.EnableMouseAllMotion)
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
	if a.commit != nil {
		return true
	}
	if a.active == viewHelp {
		return true
	}
	if a.active == viewBranch {
		return true
	}
	if a.active == viewTheme {
		return true
	}
	if a.active == viewPR {
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

// loadPRsCmd fetches the open pull/merge requests for the origin remote off the
// UI goroutine via internal/github (gh for GitHub, glab for GitLab).
func (a *App) loadPRsCmd() tea.Cmd {
	remote := a.remote
	return func() tea.Msg {
		if remote == nil {
			return prsMsg{err: errors.New("no origin remote configured")}
		}
		svc := github.New(github.HostForRemote(remote))
		prs, err := svc.ListPRs(remote)
		return prsMsg{prs: prs, err: err}
	}
}

// loadPRDetailCmd fetches a single request's message + pipeline status (ViewPR)
// and its diff (PRDiff) off the UI goroutine. A diff-fetch failure still shows
// the request, with the error in place of the diff.
func (a *App) loadPRDetailCmd(number int) tea.Cmd {
	remote := a.remote
	return func() tea.Msg {
		if remote == nil {
			return prDetailMsg{err: errors.New("no origin remote configured")}
		}
		svc := github.New(github.HostForRemote(remote))
		pr, err := svc.ViewPR(remote, number)
		if err != nil {
			return prDetailMsg{err: err}
		}
		diff, derr := svc.PRDiff(remote, number)
		if derr != nil {
			return prDetailMsg{pr: pr, diff: "diff unavailable: " + derr.Error()}
		}
		return prDetailMsg{pr: pr, diff: diff}
	}
}

// prTitle labels the request overlay per the origin host: GitLab uses "Merge
// Requests", everything else "Pull Requests".
func (a *App) prTitle() string {
	if a.remote != nil && strings.Contains(a.remote.Host, "gitlab") {
		return "Merge Requests"
	}
	return "Pull Requests"
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

	case prsMsg:
		if msg.err != nil {
			a.pr.SetError(msg.err.Error())
			return a, nil
		}
		a.pr.SetPRs(msg.prs)
		return a, nil

	case prDetailMsg:
		if msg.err != nil {
			a.pr.SetDetailError(msg.err.Error())
			return a, nil
		}
		a.pr.SetDetail(msg.pr, msg.diff)
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

	case commitDoneMsg:
		if msg.err != nil {
			a.toast = msg.err.Error()
			return a, nil
		}
		a.toast = "committed"
		// Force the selected file's diff to reload — its staged content is gone.
		a.forceDiffReload = true
		return a, a.loadStatusCmd()

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

	case tea.MouseMsg:
		return a.handleMouse(msg)

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

// currentLayout snapshots the geometry the mouse hit-test needs.
func (a *App) currentLayout() layout {
	return layout{
		headerHeight:    1,
		bodyHeight:      a.bodyHeight(),
		width:           a.width,
		listWidth:       a.listWidth,
		scrollbarWidth:  diffview.ScrollbarWidth,
		diffYOffset:     a.diff.ViewportYOffset(),
		listHidden:      a.diff.ListHidden(),
		commitBarHeight: a.diff.CommitBarHeight(),
	}
}

// handleMouse implements click-to-select, click-to-cursor, scroll, and
// divider-drag. Overlays swallow mouse events (except releasing a drag) so the
// keyboard flow inside them is never disturbed.
func (a *App) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Always honor a drag release so a divider drag can't get stuck.
	if msg.Action == tea.MouseActionRelease {
		a.dragging = false
		return a, nil
	}
	// While an overlay is open, ignore mouse input entirely.
	if a.active != viewDiff || a.confirm != nil || a.commit != nil {
		return a, nil
	}

	h := hitTest(a.currentLayout(), msg.X, msg.Y)

	// Wheel scroll acts on whatever region is under the pointer: the diff pane
	// scrolls its content; the file list moves the file selection (and takes
	// focus, so it's clear what j/k will now move). Wheel over the divider or
	// outside the body is ignored.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		switch h.region {
		case hitDiff:
			a.diff.ScrollDiff(-3)
		case hitList:
			a.diff.FocusList()
			a.diff.SelectPrev()
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case tea.MouseButtonWheelDown:
		switch h.region {
		case hitDiff:
			a.diff.ScrollDiff(3)
		case hitList:
			a.diff.FocusList()
			a.diff.SelectNext()
			return a, a.refreshDiffCmd()
		}
		return a, nil
	}

	// Drag in progress: resize the split to the pointer column.
	if a.dragging && msg.Action == tea.MouseActionMotion {
		a.resizeListTo(msg.X)
		return a, nil
	}

	// Hover (motion with no button held): highlight the file row under the
	// pointer. Purely cosmetic — it never changes the selection.
	if msg.Action == tea.MouseActionMotion {
		if h.region == hitList {
			if row, ok := a.diff.ListLineToRow(h.line); ok {
				a.diff.SetHoverRow(row)
				return a, nil
			}
		}
		a.diff.ClearHover()
		return a, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return a, nil
	}

	switch h.region {
	case hitCommit:
		// Clicking the commit affordance opens the commit dialog (same as ⇧C).
		return a.openCommit()
	case hitDivider:
		a.dragging = true
		return a, nil
	case hitList:
		if row, ok := a.diff.ListLineToRow(h.line); ok {
			a.diff.SelectRow(row)
			// Clicking a folder toggles it; clicking a file selects + loads it.
			if a.diff.Activate() {
				return a, nil
			}
			a.diff.FocusList()
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case hitDiff:
		a.diff.FocusDiff()
		a.diff.MoveCursorToRendered(h.line)
		return a, a.refreshDiffCmd()
	}
	return a, nil
}

// resizeListTo sets the list width so the divider follows the pointer column,
// clamped to sane minimums, and re-lays out.
func (a *App) resizeListTo(x int) {
	a.listWidth = diffview.ClampListWidth(a.width, x)
	a.applyLayout()
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

	// Commit dialog captures everything (text entry).
	if a.commit != nil {
		return a.handleCommitKey(msg)
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
	case viewTheme:
		return a.handleThemeKey(msg)
	case viewPR:
		return a.handlePRKey(msg)
	}

	// viewDiff: route through the dispatcher.
	return a.handleDiffKey(msg)
}

// handleThemeKey routes a key to the theme picker. Selection moves apply the
// theme live; enter persists, esc reverts to the theme active on open.
func (a *App) handleThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res, cmd := a.theme.Update(msg)
	switch res.Kind {
	case themepicker.ResultConfirm:
		a.cfg.Theme = res.Theme
		if err := a.cfg.Save(); err != nil {
			a.toast = "save theme: " + err.Error()
		}
		a.active = viewDiff
	case themepicker.ResultCancel:
		a.active = viewDiff
	}
	return a, cmd
}

// handlePRKey routes a key to the request overlay. The panel reports an Intent:
// close returns to the diff view; open-detail fetches the selected request.
func (a *App) handlePRKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	intent := a.pr.Update(msg)
	switch intent.Kind {
	case prlist.IntentClose:
		a.active = viewDiff
		return a, nil
	case prlist.IntentOpenDetail:
		return a, a.loadPRDetailCmd(intent.Number)
	}
	return a, nil
}

// normalizeKey maps Bubble Tea's key strings onto the names the config keymap
// uses. Notably Bubble Tea reports the space key as " ", which the keymap spells
// "space".
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
	case config.ActThemePicker:
		a.active = viewTheme
		a.theme.Open()
		return a, nil
	case config.ActPRList:
		a.active = viewPR
		a.pr.Open(a.prTitle())
		return a, a.loadPRsCmd()
	case config.ActToggleRawDiff:
		a.diff.ToggleRawDiff()
		if a.diff.RawMode() {
			a.toast = "raw diff: on"
		} else {
			a.toast = "raw diff: off"
		}
		return a, nil
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
		// On a folder, enter/space toggles it; on a file, focus the diff pane so
		// j/k move the line cursor.
		if a.diff.Activate() {
			return a, nil
		}
		a.diff.FocusDiff()
		return a, a.refreshDiffCmd()
	case config.ActCancel:
		// Return focus to the file list.
		a.diff.FocusList()
		return a, nil
	case config.ActFocusToggle:
		// Tab moves focus between the file tree and the diff contents. With the
		// tree hidden there's nothing to focus but the diff, so it's a no-op.
		if a.diff.ListHidden() {
			return a, nil
		}
		if a.diff.Focus() == diffview.FocusDiff {
			a.diff.FocusList()
			return a, nil
		}
		a.diff.FocusDiff()
		return a, a.refreshDiffCmd()
	case config.ActHideTree:
		hidden := !a.diff.ListHidden()
		a.diff.SetListHidden(hidden)
		if hidden {
			a.diff.FocusDiff()
			a.toast = "file tree: hidden (E to show)"
		} else {
			a.diff.FocusList()
			a.toast = "file tree: shown"
		}
		a.applyLayout()
		return a, a.refreshDiffCmd()
	case config.ActCollapse:
		a.diff.FocusList()
		a.diff.Collapse()
		return a, a.refreshDiffCmd()
	case config.ActExpand:
		a.diff.FocusList()
		a.diff.Expand()
		return a, a.refreshDiffCmd()
	case config.ActToggleTree:
		a.diff.ToggleTreeMode()
		if a.diff.TreeMode() {
			a.toast = "file tree: on"
		} else {
			a.toast = "file tree: off (flat)"
		}
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
	case config.ActStageAll:
		return a, a.stageAllCmd()
	case config.ActUnstageAll:
		return a, a.unstageAllCmd()
	case config.ActCommit:
		return a.openCommit()
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

// stageAllCmd stages every unstaged + untracked file (git add -A). It is a no-op
// (with a toast) when there is nothing to stage.
func (a *App) stageAllCmd() tea.Cmd {
	if a.status != nil && len(a.status.Unstaged) == 0 && len(a.status.Untracked) == 0 {
		a.toast = "nothing to stage"
		return nil
	}
	repo := a.repo
	a.toast = ""
	return fileMutationCmd(func() error { return repo.StageAll() })
}

// unstageAllCmd unstages every staged file (git restore --staged .). It is a
// no-op (with a toast) when nothing is staged.
func (a *App) unstageAllCmd() tea.Cmd {
	if a.status != nil && len(a.status.Staged) == 0 {
		a.toast = "nothing to unstage"
		return nil
	}
	repo := a.repo
	a.toast = ""
	return fileMutationCmd(func() error { return repo.UnstageAll() })
}

// openCommit opens the commit-message dialog. It refuses (with a toast) when
// there is nothing staged, mirroring `git commit` rejecting an empty commit.
func (a *App) openCommit() (tea.Model, tea.Cmd) {
	staged := 0
	if a.status != nil {
		staged = len(a.status.Staged)
	}
	if staged == 0 {
		a.toast = "nothing staged to commit — press s to stage a file"
		return a, nil
	}
	ti := textinput.New()
	ti.Placeholder = "commit message"
	ti.CharLimit = 0 // no limit; long messages wrap in git
	a.commit = &commitState{input: ti, staged: staged}
	a.toast = ""
	// Focus the stored input (not the local copy) so it actually receives keys.
	return a, a.commit.input.Focus()
}

// handleCommitKey routes a key to the commit dialog: enter commits, esc cancels,
// everything else feeds the text input.
func (a *App) handleCommitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		a.commit = nil
		return a, nil
	case "enter":
		message := strings.TrimSpace(a.commit.input.Value())
		if message == "" {
			// Keep the dialog open; an empty message can't be committed.
			a.commit.input.Placeholder = "a commit message is required"
			return a, nil
		}
		a.commit = nil
		return a, a.commitCmd(message)
	}
	var cmd tea.Cmd
	a.commit.input, cmd = a.commit.input.Update(msg)
	return a, cmd
}

// commitCmd commits the staged changes with the given message off the UI thread.
func (a *App) commitCmd(message string) tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		return commitDoneMsg{err: repo.Commit(message)}
	}
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
	switch {
	case a.commit != nil:
		body = a.renderCommitOverlay(a.width, a.bodyHeight())
	case a.confirm != nil:
		// A pending confirmation renders as a centered modal over the body, so the
		// header/footer stay a single row each (the old footer-rendered bordered
		// box overflowed the 1-row footer assumption).
		body = a.renderConfirmOverlay(a.width, a.bodyHeight())
	case a.active == viewHelp:
		body = a.help.View(a.width, a.bodyHeight())
	case a.active == viewBranch:
		body = a.branch.View(a.width, a.bodyHeight())
	case a.active == viewTheme:
		body = a.theme.View(a.width, a.bodyHeight())
	case a.active == viewPR:
		body = a.pr.View(a.width, a.bodyHeight())
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
	// Truncate to the inner width (total minus the Header's 1-col side padding) so
	// a long path can never wrap to a second row and break the layout.
	content := fitWidth(strings.Join(segs, sep), a.width-2)
	return styles.Header.Render(content)
}

// footerHint returns the key hint line, abbreviated on narrow terminals so it
// stays on one row instead of being aggressively ellipsized.
func (a *App) footerHint() string {
	if a.width < 100 {
		return "j/k move · enter diff · s stage · a/A stage/unstage all · C commit · u/U discard · ? help · q quit"
	}
	auto := "on"
	if !a.autoRefresh {
		auto = "off"
	}
	return "j/k move · tab focus · enter open · h/l fold · . flat · E hide tree · u hunk · U file · ctrl+r recover · < > resize · s stage · a/A stage/unstage all · C commit · r refresh · ctrl+t auto:" + auto + " · B branches · T theme · P prs · ? help · q quit"
}

func (a *App) renderFooter() string {
	var body string
	switch {
	case a.commit != nil:
		body = styles.Hint.Render("enter commit · esc cancel")
	case a.confirm != nil:
		body = styles.Toast.Render("y confirm · n cancel")
	case a.toast != "":
		body = styles.Toast.Render(a.toast)
	default:
		body = styles.Hint.Render(a.footerHint())
	}
	// Version badge pinned bottom-left, then the hint/toast fills the rest.
	left := styles.Version.Render("gui " + a.version)
	line := left + styles.HeaderSep.Render(" · ") + body
	return styles.Header.Render(fitWidth(line, a.width-2))
}

// renderCommitOverlay renders the commit-message dialog as a centered modal: a
// title, the staged-file count, the text input, and key hints.
func (a *App) renderCommitOverlay(width, height int) string {
	if a.commit == nil {
		return ""
	}
	title := styles.OverlayTitle.Render("Commit")
	noun := "files"
	if a.commit.staged == 1 {
		noun = "file"
	}
	ctx := styles.Desc.Render(fmt.Sprintf("%d staged %s", a.commit.staged, noun))
	hint := styles.Desc.Render("enter commit · esc cancel")
	box := styles.Confirm.Render(lipgloss.JoinVertical(lipgloss.Left,
		title, "", ctx, "", a.commit.input.View(), "", hint))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// renderConfirmOverlay renders the pending confirmation as a centered modal.
func (a *App) renderConfirmOverlay(width, height int) string {
	if a.confirm == nil {
		return ""
	}
	title := styles.OverlayTitle.Render("⚠  Confirm")
	prompt := a.confirm.prompt
	hint := styles.Desc.Render("y confirm · n cancel · esc")
	box := styles.Confirm.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", prompt, "", hint))
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// fitWidth truncates s (ANSI-aware) to at most w display columns, appending an
// ellipsis when it had to cut. Returns "" for non-positive widths.
func fitWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
