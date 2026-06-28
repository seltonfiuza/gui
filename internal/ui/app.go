// Package ui implements the Bubble Tea UI layer for gui. The root App model
// owns the git.Service and config.Dispatcher, routes keys through the leader
// chord dispatcher, and renders the active view (diff / branch / help). All
// blocking git calls run inside tea.Cmds; Update never blocks.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	"github.com/seltonfiuza/gui/internal/ui/commitpanel"
	"github.com/seltonfiuza/gui/internal/ui/diffview"
	"github.com/seltonfiuza/gui/internal/ui/help"
	"github.com/seltonfiuza/gui/internal/ui/palette"
	"github.com/seltonfiuza/gui/internal/ui/prlist"
	"github.com/seltonfiuza/gui/internal/ui/prpanel"
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
	viewPalette
)

// leftFocus tracks which left-column region (or the diff) has keyboard focus.
type leftFocus int

const (
	focusFiles leftFocus = iota
	focusPRs
	focusCommits
	focusDiff
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

// prApproveDoneMsg / prMergeDoneMsg carry the result of an approve / merge.
type prApproveDoneMsg struct {
	number int
	err    error
}

type prMergeDoneMsg struct {
	number int
	err    error
}

type diffMsg struct {
	path   string
	staged bool
	raw    string
	err    error
}

// bgDiffMsg is a tick-driven diff fetch; it is applied with SetDiffPreserving so
// an unchanged diff keeps the user's cursor/scroll.
type bgDiffMsg struct {
	path   string
	staged bool
	raw    string
	err    error
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

// editorFinishedMsg is delivered when the external editor opened by
// ActEditFile exits. It carries any process error and triggers a refresh.
type editorFinishedMsg struct {
	err error
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

// diffKey identifies a cached diff: a path plus whether it is the index
// (staged) diff or the worktree (unstaged) diff.
type diffKey struct {
	path   string
	staged bool
}

// App is the root Bubble Tea model.
type App struct {
	repo       *git.Service
	dispatcher *config.Dispatcher
	cfg        config.Config
	keymap     config.Keymap

	status *git.Status
	remote *git.Remote

	active      view
	diff        diffview.Model
	branch      branchpanel.Model
	help        help.Model
	theme       themepicker.Model
	pr          prlist.Model
	palette     palette.Model
	prPanel     prpanel.Model
	commitPanel commitpanel.Model
	leftFocus   leftFocus

	// viewingCommit is true while the diff pane shows a historical commit (git show) instead of the working-tree diff.
	viewingCommit bool

	// dragging is true while the user holds the mouse on the divider to resize.
	dragging bool

	// pending confirmation for a destructive file op (discard).
	confirm *confirmState

	// commit is the active commit-message dialog, or nil when closed.
	commit *commitState

	// blame is the active git-blame popup, or nil when closed.
	blame *blameState

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

	// diffCache memoizes the unified diff for each (path, staged) pair so that
	// navigating away from a file and back does not re-shell `git diff`. It is
	// invalidated wholesale whenever the status fingerprint changes, since a
	// working-tree or index change can alter any cached diff.
	diffCache map[diffKey]string
	// diffShownKey identifies the (path, staged) diff currently displayed, so a
	// re-selection of the same path in a different group (staged vs unstaged)
	// still reloads. The zero value matches no real file.
	diffShownKey diffKey

	// autoRefresh toggles the background polling tick (ctrl+t). Default on.
	autoRefresh bool
	// statusFP is the fingerprint of the last applied status; a tick whose
	// fingerprint matches causes no re-render and no diff re-fetch.
	statusFP string
	// commitFP is the HEAD OID for which the commit log was last loaded. The log
	// is re-read only when HEAD moves (commit/amend/checkout/rebase), not on every
	// working-tree change — a working-tree edit cannot alter `git log` output.
	commitFP string
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
// the staged-file count shown for context. enter commits, ctrl+a toggles amend,
// esc cancels.
type commitState struct {
	input    textinput.Model
	staged   int
	amend    bool   // when true, enter runs `git commit --amend`
	canAmend bool   // a previous commit exists (set after lastCommitMsg arrives)
	lastMsg  string // the previous commit's message, used to prefill on amend
	note     string // inline validation hint (e.g. "nothing staged")
}

// blameState is the modal git-blame popup for the diff line under the cursor.
// While loading is true the git call is in flight; note is non-empty for the
// removed/no-blame/error states (no commit info to show).
type blameState struct {
	line    int
	loading bool
	entry   git.BlameEntry
	note    string
}

// blameMsg carries the result of an async BlameLine call.
type blameMsg struct {
	entry git.BlameEntry
	err   error
}

// commitDoneMsg is emitted after a commit attempt completes.
type commitDoneMsg struct {
	err   error
	amend bool
}

// lastCommitMsg carries the previous commit's message (and whether one exists)
// back to an open commit dialog so it can offer/prefill an amend.
type lastCommitMsg struct {
	message string
	ok      bool
}

// pushDoneMsg is emitted after a push attempt completes.
type pushDoneMsg struct{ err error }

// New constructs the root model. version is the build version shown in the
// footer (pass "dev" for local builds). It loads persisted config and applies
// the saved theme so the UI launches in the user's preferred colors.
func New(repo *git.Service, version string) tea.Model {
	cfg, warns, _ := config.Load()
	styles.SetTheme(cfg.Theme)
	if version == "" {
		version = "dev"
	}
	km, kwarns := cfg.Keymap()
	warns = append(warns, kwarns...)
	toast := ""
	if len(warns) > 0 {
		toast = warns[0]
	}
	return &App{
		repo:        repo,
		cfg:         cfg,
		keymap:      km,
		dispatcher:  config.NewDispatcher(km),
		active:      viewDiff,
		diff:        diffview.New(),
		branch:      branchpanel.New(),
		help:        help.New(km.Bindings()),
		theme:       themepicker.New(),
		pr:          prlist.New(),
		palette:     palette.New(),
		prPanel:     prpanel.New(),
		commitPanel: commitpanel.New(),
		listWidth:   defaultListWidth,
		autoRefresh: true,
		version:     version,
		toast:       toast,
		diffCache:   make(map[diffKey]string),
	}
}

// Init fires the initial status + remote loads, starts the auto-refresh tick,
// and enables all-motion mouse tracking so clicks/scroll/drag AND hover (motion
// with no button) are delivered to Update — the latter drives hover highlights.
func (a *App) Init() tea.Cmd {
	a.prPanel.SetPlaceholder("loading…")
	// Commits are loaded reactively off the first status (see maybeLoadCommitsCmd),
	// keyed on HEAD's OID, so they are not eagerly fetched here.
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

	// The status changed, so a working-tree or index change may have altered any
	// cached diff; drop the whole cache. (bgRefreshDiffCmd re-populates the
	// currently selected file below.)
	a.diffCache = make(map[diffKey]string)

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
	return a, tea.Batch(next, a.bgRefreshDiffCmd(), a.maybeLoadCommitsCmd(msg.status))
}

// bgRefreshDiffCmd re-fetches the selected file's diff for a background refresh.
// Unlike refreshDiffCmd it always re-fetches the selected path (so changed-in-
// place content is picked up) but the result is applied via SetDiffPreserving,
// which keeps the line cursor and scroll when the diff text is unchanged.
func (a *App) bgRefreshDiffCmd() tea.Cmd {
	if a.viewingCommit {
		return nil
	}
	row, ok := a.diff.Selected()
	if !ok {
		return nil
	}
	repo := a.repo
	path := row.File.Path
	staged := row.Group == diffview.GroupStaged
	return func() tea.Msg {
		raw, err := repo.Diff(path, staged)
		return bgDiffMsg{path: path, staged: staged, raw: raw, err: err}
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
	if a.blame != nil {
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
	if a.active == viewPalette {
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

// commitLogLimit caps how many commits the Commits block reads.
const commitLogLimit = 50

type commitsMsg struct {
	commits []git.Commit
	err     error
}

// loadCommitsCmd reads the recent commit log off the UI thread.
func (a *App) loadCommitsCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		cs, err := repo.Log(commitLogLimit)
		return commitsMsg{commits: cs, err: err}
	}
}

// maybeLoadCommitsCmd returns a commit-log fetch only when HEAD has moved since
// the log was last loaded (tracked via commitFP). `git log` output depends only
// on the commit graph reachable from HEAD, so a working-tree or index change
// alone never warrants a re-read. Returns nil when no fetch is needed.
func (a *App) maybeLoadCommitsCmd(s *git.Status) tea.Cmd {
	if s == nil || s.OID == a.commitFP {
		return nil
	}
	a.commitFP = s.OID
	return a.loadCommitsCmd()
}

type commitDiffMsg struct {
	sha string
	raw string
	err error
}

// loadCommitDiffCmd fetches a commit's diff (git show) off the UI thread.
func (a *App) loadCommitDiffCmd(sha string) tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		raw, err := repo.CommitDiff(sha)
		return commitDiffMsg{sha: sha, raw: raw, err: err}
	}
}

// shortSHA abbreviates a full commit SHA for display.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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

// prCreatePrereqMsg carries the head (current branch) and base (origin default)
// resolved off the UI goroutine before opening the create form.
type prCreatePrereqMsg struct {
	head, base string
	err        error
}

// prCreateDoneMsg is the result of a create-request attempt.
type prCreateDoneMsg struct {
	pr  github.PR
	err error
}

// prCreatePrereqCmd resolves the head branch and a prefilled base for the create
// form. A failed default-branch lookup falls back to "main".
func (a *App) prCreatePrereqCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		head, err := repo.CurrentBranch()
		if err != nil {
			return prCreatePrereqMsg{err: err}
		}
		base, berr := repo.DefaultBranch()
		if berr != nil || strings.TrimSpace(base) == "" {
			base = "main"
		}
		return prCreatePrereqMsg{head: head, base: base}
	}
}

// prCreateCmd pushes the head branch (set upstream) then creates the request.
func (a *App) prCreateCmd(opts github.CreatePROpts) tea.Cmd {
	repo := a.repo
	remote := a.remote
	return func() tea.Msg {
		if remote == nil {
			return prCreateDoneMsg{err: errors.New("no origin remote configured")}
		}
		if err := repo.PushSetUpstream(opts.Head); err != nil {
			return prCreateDoneMsg{err: err}
		}
		svc := github.New(github.HostForRemote(remote))
		pr, err := svc.CreatePR(remote, opts)
		return prCreateDoneMsg{pr: pr, err: err}
	}
}

// approveCmd approves PR number off the UI goroutine.
func (a *App) approveCmd(number int) tea.Cmd {
	remote := a.remote
	return func() tea.Msg {
		if remote == nil {
			return prApproveDoneMsg{number: number, err: errors.New("no origin remote configured")}
		}
		svc := github.New(github.HostForRemote(remote))
		return prApproveDoneMsg{number: number, err: svc.ApprovePR(remote, number)}
	}
}

// mergeCmd merges PR number via method (optionally deleting the head branch) off
// the UI goroutine.
func (a *App) mergeCmd(number int, method github.MergeMethod, deleteBranch bool) tea.Cmd {
	remote := a.remote
	return func() tea.Msg {
		if remote == nil {
			return prMergeDoneMsg{number: number, err: errors.New("no origin remote configured")}
		}
		svc := github.New(github.HostForRemote(remote))
		return prMergeDoneMsg{number: number, err: svc.MergePR(remote, number, method, deleteBranch)}
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

// prNoun returns the singular request noun for the origin host: "Merge Request"
// for GitLab, "Pull Request" otherwise.
func (a *App) prNoun() string {
	if a.remote != nil && strings.Contains(a.remote.Host, "gitlab") {
		return "Merge Request"
	}
	return "Pull Request"
}

func (a *App) loadDiffCmd(path string, staged bool) tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		raw, err := repo.Diff(path, staged)
		return diffMsg{path: path, staged: staged, raw: raw, err: err}
	}
}

// loadBlameCmd fetches blame for a single 1-based line off the UI thread.
func (a *App) loadBlameCmd(path string, line int) tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		entry, err := repo.BlameLine(path, line)
		return blameMsg{entry: entry, err: err}
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
		// A foreground status is only requested after a content-affecting op
		// (commit, push, mutation, editor return); cached diffs may be stale.
		a.diffCache = make(map[diffKey]string)
		a.diff.SetStatus(msg.status)
		return a, tea.Batch(a.refreshDiffCmd(), a.maybeLoadCommitsCmd(msg.status))

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
			return a, a.loadPRsCmd()
		}
		a.remote = msg.remote
		return a, a.loadPRsCmd()

	case prsMsg:
		if msg.err != nil {
			a.pr.SetError(msg.err.Error())
			a.prPanel.SetError(msg.err.Error())
			a.syncLeftBlocks()
			return a, nil
		}
		a.pr.SetPRs(msg.prs)
		a.prPanel.SetPRs(msg.prs)
		a.syncLeftBlocks()
		return a, nil

	case commitsMsg:
		if msg.err == nil {
			a.commitPanel.SetCommits(msg.commits)
			if len(msg.commits) == 0 {
				a.commitPanel.SetPlaceholder("no commits yet")
			}
		}
		a.syncLeftBlocks()
		return a, nil

	case commitDiffMsg:
		if msg.err != nil {
			a.toast = "commit diff: " + msg.err.Error()
			return a, nil
		}
		a.viewingCommit = true
		a.diff.SetDiff("commit "+shortSHA(msg.sha), msg.raw)
		a.diff.SetViewingCommit(true)
		return a, nil

	case prDetailMsg:
		if msg.err != nil {
			a.pr.SetDetailError(msg.err.Error())
			return a, nil
		}
		a.pr.SetDetail(msg.pr, msg.diff)
		return a, nil

	case prApproveDoneMsg:
		if msg.err != nil {
			a.toast = "approve: " + msg.err.Error()
			return a, nil
		}
		a.toast = fmt.Sprintf("approved #%d", msg.number)
		return a, a.loadPRDetailCmd(msg.number)

	case prMergeDoneMsg:
		if msg.err != nil {
			a.toast = "merge: " + msg.err.Error()
			return a, nil
		}
		a.toast = fmt.Sprintf("merged #%d", msg.number)
		a.pr.BackToList()
		return a, a.loadPRsCmd()

	case prCreatePrereqMsg:
		if msg.err != nil {
			a.toast = "create " + a.prNoun() + ": " + msg.err.Error()
			return a, nil
		}
		return a, a.pr.OpenCreate(msg.head, msg.base, a.prNoun())

	case prCreateDoneMsg:
		if msg.err != nil {
			a.pr.SetCreateError(msg.err.Error())
			return a, nil
		}
		a.toast = fmt.Sprintf("created #%d", msg.pr.Number)
		a.pr.BackToList()
		return a, a.loadPRsCmd()

	case blameMsg:
		if a.blame == nil {
			return a, nil
		}
		a.blame.loading = false
		if msg.err != nil {
			a.blame.note = "blame failed: " + msg.err.Error()
			return a, nil
		}
		a.blame.entry = msg.entry
		return a, nil

	case diffMsg:
		if msg.err != nil {
			a.toast = "diff: " + msg.err.Error()
			return a, nil
		}
		key := diffKey{path: msg.path, staged: msg.staged}
		a.diff.SetDiff(msg.path, msg.raw)
		a.diffCache[key] = msg.raw
		a.diffShownKey = key
		return a, nil

	case bgDiffMsg:
		// A poll diff-fetch that was already in flight when the user opened a
		// commit view must not overwrite it.
		if a.viewingCommit {
			return a, nil
		}
		if msg.err != nil {
			// Background diff errors are non-fatal and not toasted (the path may
			// have just vanished); the next status tick reconciles.
			return a, nil
		}
		// Only apply if the selection still points at this path (it may have
		// moved between the fetch firing and completing).
		if a.diff.SelectedPath() == msg.path {
			key := diffKey{path: msg.path, staged: msg.staged}
			a.diff.SetDiffPreserving(msg.path, msg.raw)
			a.diffCache[key] = msg.raw
			a.diffShownKey = key
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
		if msg.amend {
			a.toast = "amended"
		} else {
			a.toast = "committed"
		}
		// Force the selected file's diff to reload — its staged content is gone.
		a.forceDiffReload = true
		return a, a.loadStatusCmd()

	case lastCommitMsg:
		// The async last-commit fetch resolved; let an open dialog offer amend.
		if a.commit != nil {
			a.commit.lastMsg = msg.message
			a.commit.canAmend = msg.ok
		}
		return a, nil

	case pushDoneMsg:
		if msg.err != nil {
			a.toast = msg.err.Error()
			return a, nil
		}
		a.toast = "pushed"
		// Refresh so the ahead/behind counts update.
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

	case editorFinishedMsg:
		if msg.err != nil {
			a.toast = "editor exited with error"
		}
		// The file may have changed on disk; force the diff to reload and
		// refresh the working-tree status so edits show immediately.
		a.forceDiffReload = true
		// tea.ExecProcess releases the terminal (disabling mouse input), and
		// Bubble Tea's RestoreTerminal only re-enables alt-screen/paste/focus —
		// not mouse tracking — so we must explicitly re-arm it here.
		return a, tea.Batch(a.loadStatusCmd(), a.refreshDiffCmd(), tea.EnableMouseAllMotion)

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
	if a.confirm != nil || a.commit != nil || a.blame != nil {
		return a, nil
	}
	// In the PR overlay, the wheel scrolls the description pane when the pointer is
	// over it. headerHeight is 1 (see currentLayout), so subtract it to convert to
	// PR-view-local coordinates. All other PR-view mouse input is ignored.
	if a.active == viewPR {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			a.pr.ScrollDescriptionAt(msg.X, msg.Y-1, -3)
		case tea.MouseButtonWheelDown:
			a.pr.ScrollDescriptionAt(msg.X, msg.Y-1, 3)
		}
		return a, nil
	}
	if a.active != viewDiff {
		return a, nil
	}

	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		delta := -3
		if msg.Button == tea.MouseButtonWheelDown {
			delta = 3
		}
		if prTop, prBot, cTop, cBot, ok := a.leftBlockYRanges(); ok && msg.X < a.listWidth {
			bodyY := msg.Y - 1 // header occupies screen row 0
			switch {
			case bodyY >= cTop && bodyY < cBot:
				a.commitPanel.ScrollBy(delta)
				a.syncLeftBlocks()
				return a, nil
			case bodyY >= prTop && bodyY < prBot:
				a.prPanel.ScrollBy(delta)
				a.syncLeftBlocks()
				return a, nil
			}
		}
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
			a.setLeftFocus(focusFiles)
			a.diff.SelectPrev()
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case tea.MouseButtonWheelDown:
		switch h.region {
		case hitDiff:
			a.diff.ScrollDiff(3)
		case hitList:
			a.setLeftFocus(focusFiles)
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
			a.setLeftFocus(focusFiles)
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case hitDiff:
		a.setLeftFocus(focusDiff)
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

	// Blame popup captures esc/b/q to close.
	if a.blame != nil {
		switch key {
		case "esc", "b", "q", "enter":
			a.blame = nil
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
	case viewPalette:
		return a.handlePaletteKey(msg)
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

// handlePRKey routes a key to the request overlay. The panel reports an Intent
// (and an optional cmd from a focused input): close returns to the diff view,
// open-detail fetches a request, start-create gathers head/base, create runs the
// push+create flow.
func (a *App) handlePRKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	intent, cmd := a.pr.Update(msg)
	switch intent.Kind {
	case prlist.IntentClose:
		a.active = viewDiff
		return a, cmd
	case prlist.IntentOpenDetail:
		return a, tea.Batch(cmd, a.loadPRDetailCmd(intent.Number))
	case prlist.IntentStartCreate:
		return a, tea.Batch(cmd, a.prCreatePrereqCmd())
	case prlist.IntentCreate:
		a.toast = "creating request…"
		return a, tea.Batch(cmd, a.prCreateCmd(intent.Opts))
	case prlist.IntentApprove:
		a.toast = fmt.Sprintf("approving #%d…", intent.Number)
		return a, tea.Batch(cmd, a.approveCmd(intent.Number))
	case prlist.IntentMerge:
		a.toast = fmt.Sprintf("merging #%d…", intent.Number)
		return a, tea.Batch(cmd, a.mergeCmd(intent.Number, intent.Method, intent.DeleteBranch))
	}
	return a, cmd
}

// handlePaletteKey routes a key to the command palette. Cancel closes it; Run
// closes it and dispatches the chosen action (which may itself open another
// overlay, e.g. the commit dialog or branch panel).
func (a *App) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res, cmd := a.palette.Update(msg)
	switch res.Kind {
	case palette.ResultCancel:
		a.active = viewDiff
		return a, nil
	case palette.ResultRun:
		a.active = viewDiff
		_, dcmd := a.dispatchAction(res.Action)
		return a, tea.Batch(cmd, dcmd)
	}
	return a, cmd
}

// paletteCommands builds the palette's command list from the keymap bindings,
// omitting pure navigation/cursor actions (and the palette itself) so it reads as
// a menu of operations rather than movement keys.
func paletteCommands(km config.Keymap) []palette.Command {
	skip := map[config.Action]bool{
		config.ActNone:           true,
		config.ActDown:           true,
		config.ActUp:             true,
		config.ActConfirm:        true,
		config.ActCancel:         true,
		config.ActCollapse:       true,
		config.ActExpand:         true,
		config.ActFocusToggle:    true,
		config.ActHunkNext:       true,
		config.ActHunkPrev:       true,
		config.ActPaneGrow:       true,
		config.ActPaneShrink:     true,
		config.ActCommandPalette: true,
	}
	var cmds []palette.Command
	for _, b := range km.Bindings() {
		if skip[b.Action] {
			continue
		}
		cmds = append(cmds, palette.Command{
			Action: b.Action,
			Title:  b.Desc,
			Keys:   strings.Join(b.Keys, " / "),
		})
	}
	return cmds
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
	// When a bottom-left panel is focused, j/k/enter and → drive it directly.
	if a.leftFocus == focusPRs || a.leftFocus == focusCommits {
		switch msg.String() {
		case "j", "down", "k", "up", "enter", "right", "l":
			return a.routeLeftKey(msg)
		}
	}
	action := a.dispatcher.Resolve(normalizeKey(msg.String()))
	if action == config.ActNone {
		// Unhandled key: let the diff viewport scroll (pgup/pgdn/etc).
		return a, a.diff.ForwardViewport(msg)
	}
	return a.dispatchAction(action)
}

// dispatchAction performs a resolved high-level action. It is shared by the
// keymap dispatcher (handleDiffKey) and the command palette, so every command is
// runnable both by its key and by name.
func (a *App) dispatchAction(action config.Action) (tea.Model, tea.Cmd) {
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
		return a, tea.Batch(a.loadStatusCmd(), a.loadPRsCmd(), a.loadCommitsCmd())
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
		// Only reload when the file SELECTION moved (list focus); scrolling the
		// diff cursor needs no reload and must not clobber a shown commit diff.
		if a.diff.Focus() == diffview.FocusList {
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case config.ActUp:
		a.diff.CursorUp()
		if a.diff.Focus() == diffview.FocusList {
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case config.ActConfirm:
		// On a folder, enter/space toggles it; on a file, focus the diff pane so
		// j/k move the line cursor.
		if a.diff.Activate() {
			return a, nil
		}
		a.setLeftFocus(focusDiff)
		return a, a.refreshDiffCmd()
	case config.ActCancel:
		// Return focus to the file list and restore the working-tree diff if a commit was shown.
		a.leftFocus = focusFiles
		a.applyLeftFocus()
		a.syncLeftBlocks()
		return a, a.refreshDiffCmd()
	case config.ActFocusToggle:
		if a.diff.ListHidden() {
			return a, nil
		}
		a.leftFocus = (a.leftFocus + 1) % 4
		a.applyLeftFocus()
		a.syncLeftBlocks()
		if a.leftFocus == focusDiff {
			return a, a.refreshDiffCmd()
		}
		return a, nil
	case config.ActHideTree:
		hidden := !a.diff.ListHidden()
		a.diff.SetListHidden(hidden)
		if hidden {
			a.leftFocus = focusDiff
			a.toast = "file tree: hidden (E to show)"
		} else {
			a.leftFocus = focusFiles
			a.toast = "file tree: shown"
		}
		a.applyLeftFocus()
		a.applyLayout()
		return a, a.refreshDiffCmd()
	case config.ActCollapse:
		// While scrolling a commit's diff (entered via → from the Commits
		// block), ← returns focus to the Commits list, keeping the diff shown.
		if a.viewingCommit && a.leftFocus == focusDiff {
			a.setLeftFocus(focusCommits)
			return a, nil
		}
		a.setLeftFocus(focusFiles)
		a.diff.Collapse()
		return a, a.refreshDiffCmd()
	case config.ActExpand:
		// → while scrolling a commit's diff stays in the commit view (it's
		// already focused); don't leak to the working-tree file tree.
		if a.viewingCommit && a.leftFocus == focusDiff {
			return a, nil
		}
		a.setLeftFocus(focusFiles)
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
	case config.ActEditFile:
		row, ok := a.diff.Selected()
		if !ok {
			a.toast = "nothing to edit"
			return a, nil
		}
		abs := filepath.Join(a.repo.Root(), row.File.Path)
		if _, err := os.Stat(abs); err != nil {
			a.toast = "nothing to edit — file is gone"
			return a, nil
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vim"
		}
		a.toast = ""
		return a, tea.ExecProcess(exec.Command(editor, abs), func(err error) tea.Msg {
			return editorFinishedMsg{err: err}
		})
	case config.ActBlameLine:
		if a.diff.Focus() != diffview.FocusDiff {
			a.toast = "blame: focus the diff first"
			return a, nil
		}
		path := a.diff.DiffPath()
		if path == "" {
			a.toast = "blame: no file selected"
			return a, nil
		}
		line, removed, ok := a.diff.SourceLineAtCursor()
		if removed {
			a.blame = &blameState{note: "no blame — line was removed"}
			return a, nil
		}
		if !ok {
			a.blame = &blameState{note: "no blame for this line"}
			return a, nil
		}
		a.blame = &blameState{line: line, loading: true}
		return a, a.loadBlameCmd(path, line)
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
	case config.ActPush:
		return a.confirmPush()
	case config.ActCommandPalette:
		a.palette.SetCommands(paletteCommands(a.keymap))
		a.active = viewPalette
		return a, a.palette.Open()
	case config.ActUndo:
		return a.discardHunk()
	case config.ActUndoFile:
		return a.discardWholeFile()
	case config.ActRecover:
		return a.recover()
	}
	return a, nil
}

// refreshDiffCmd loads the diff for the currently selected file if it differs
// from what's shown.
func (a *App) refreshDiffCmd() tea.Cmd {
	// Leaving a commit view must re-show the working-tree diff even when the same
	// file is still selected, so capture the prior state before clearing it.
	wasCommit := a.viewingCommit
	a.viewingCommit = false
	a.diff.SetViewingCommit(false)
	row, ok := a.diff.Selected()
	if !ok {
		return nil
	}
	key := diffKey{path: row.File.Path, staged: row.Group == diffview.GroupStaged}
	if a.diffShownKey == key && !a.forceDiffReload && !wasCommit {
		return nil
	}
	force := a.forceDiffReload
	a.forceDiffReload = false
	// Serve from cache unless a mutation may have changed the file in place
	// (force): then the cached entry is stale and must be re-fetched.
	if force {
		delete(a.diffCache, key)
	} else if raw, ok := a.diffCache[key]; ok {
		a.diff.SetDiff(key.path, raw)
		a.diffShownKey = key
		return nil
	}
	return a.loadDiffCmd(key.path, key.staged)
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

// confirmPush opens a confirmation before pushing, since it publishes commits to
// the remote. The prompt names the branch and its upstream when known.
func (a *App) confirmPush() (tea.Model, tea.Cmd) {
	branch := "the current branch"
	target := "its upstream"
	if a.status != nil {
		if a.status.Branch != "" && a.status.Branch != "(detached)" {
			branch = "'" + a.status.Branch + "'"
		}
		if a.status.Upstream != "" {
			target = a.status.Upstream
		}
	}
	a.confirm = &confirmState{
		prompt: fmt.Sprintf("Push %s to %s? (y/n)", branch, target),
		onYes:  func(app *App) tea.Cmd { return app.pushCmd() },
	}
	return a, nil
}

// pushCmd pushes the current branch off the UI thread.
func (a *App) pushCmd() tea.Cmd {
	repo := a.repo
	a.toast = "pushing…"
	return func() tea.Msg {
		return pushDoneMsg{err: repo.Push()}
	}
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
	case a.blame != nil:
		body = a.renderBlameOverlay(a.width, a.bodyHeight())
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
	case a.active == viewPalette:
		body = a.palette.View(a.width, a.bodyHeight())
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
	a.syncLeftBlocks()
}

// leftBlockHeight is the fixed row budget (incl. title) for each bottom panel.
const leftBlockHeight = 6

// leftBlockYRanges returns the body-relative [top,bottom) row ranges of the PR
// and Commits blocks (bottom of the left column), or ok=false when the tree is
// hidden. The Commits block is bottom-most; the PR block sits just above it.
func (a *App) leftBlockYRanges() (prTop, prBot, cTop, cBot int, ok bool) {
	if a.diff.ListHidden() {
		return 0, 0, 0, 0, false
	}
	bodyH := a.bodyHeight()
	if bodyH < 2*leftBlockHeight {
		// Not enough room for both blocks; don't intercept (keep the file list
		// wheel-scrollable).
		return 0, 0, 0, 0, false
	}
	cBot = bodyH
	cTop = cBot - leftBlockHeight
	prBot = cTop
	prTop = prBot - leftBlockHeight
	return prTop, prBot, cTop, cBot, true
}

// commitBlockTopY is the body-relative first row of the Commits block (used by
// hit-testing and tests).
func (a *App) commitBlockTopY() int {
	_, _, cTop, _, _ := a.leftBlockYRanges()
	return cTop
}

// syncLeftBlocks sizes the two panels to the list width and pushes their
// rendered strings into the diff view's left column. When the file tree is
// hidden there is no left column, so the blocks are cleared.
func (a *App) syncLeftBlocks() {
	if a.diff.ListHidden() || a.listWidth <= 0 {
		a.diff.SetLeftBlocks(nil)
		return
	}
	a.prPanel.SetSize(a.listWidth, leftBlockHeight)
	a.commitPanel.SetSize(a.listWidth, leftBlockHeight)
	a.diff.SetLeftBlocks([]string{a.prPanel.View(), a.commitPanel.View()})
}

// applyLeftFocus reflects a.leftFocus into the diff view and the two panels so
// exactly one region shows the active selection style.
func (a *App) applyLeftFocus() {
	a.prPanel.SetFocused(a.leftFocus == focusPRs)
	a.commitPanel.SetFocused(a.leftFocus == focusCommits)
	if a.leftFocus == focusDiff {
		a.diff.FocusDiff()
	} else {
		a.diff.FocusList()
	}
}

// setLeftFocus updates the focused left-column region and reconciles the diff
// view focus + panel highlight + rendering. Use this instead of calling
// a.diff.FocusList()/FocusDiff() directly so keyboard routing never desyncs
// from the visible focus.
func (a *App) setLeftFocus(f leftFocus) {
	a.leftFocus = f
	a.applyLeftFocus()
	a.syncLeftBlocks()
}

// routeLeftKey forwards a navigation/activation key to the focused bottom panel.
func (a *App) routeLeftKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.leftFocus {
	case focusPRs:
		intent := a.prPanel.Update(msg)
		a.syncLeftBlocks()
		if intent.Kind == prpanel.IntentActivate {
			a.active = viewPR
			// Open straight into the PR's detail (loading), and load the list
			// behind it so Esc returns to a populated list.
			a.pr.OpenDetail(a.prTitle())
			return a, tea.Batch(a.loadPRsCmd(), a.loadPRDetailCmd(intent.Number))
		}
		return a, nil
	case focusCommits:
		// → / l opens the selected commit's diff and moves focus into the diff
		// pane so j/k scrolls the changes (← returns to the list).
		if k := msg.String(); k == "right" || k == "l" {
			if c, ok := a.commitPanel.Selected(); ok {
				a.setLeftFocus(focusDiff)
				return a, a.loadCommitDiffCmd(c.SHA)
			}
			return a, nil
		}
		intent := a.commitPanel.Update(msg)
		a.syncLeftBlocks()
		if intent.Kind == commitpanel.IntentActivate {
			return a, a.loadCommitDiffCmd(intent.SHA)
		}
		return a, nil
	}
	return a, nil
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
		return "j/k move · s stage · a/A stage all · C commit · p push · ctrl+p palette · ? help · q quit"
	}
	auto := "on"
	if !a.autoRefresh {
		auto = "off"
	}
	return "j/k move · tab focus · enter open · h/l fold · . flat · E hide tree · u hunk · U file · e edit · ctrl+r recover · < > resize · s stage · a/A stage/unstage all · C commit · p push · ctrl+p palette · r refresh · ctrl+t auto:" + auto + " · B branches · T theme · P prs · ? help · q quit"
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

// renderBlameOverlay renders the git-blame popup as a centered modal.
func (a *App) renderBlameOverlay(width, height int) string {
	var content string
	switch {
	case a.blame.note != "":
		content = styles.Inline.Render(a.blame.note)
	case a.blame.loading:
		content = styles.Desc.Render("loading blame…")
	case a.blame.entry.NotCommitted:
		content = styles.Inline.Render("Not Committed Yet")
	default:
		e := a.blame.entry
		when := e.AuthorTime.Format("2006-01-02 15:04")
		head := fmt.Sprintf("%s  %s  %s (%s)", e.CommitHash, e.Author, humanizeSince(e.AuthorTime), when)
		content = lipgloss.JoinVertical(lipgloss.Left,
			styles.OverlayTitle.Render(head),
			styles.Desc.Render(e.Summary),
		)
	}
	box := styles.Overlay.Render(content)
	return lipgloss.Place(maxi(width, 1), maxi(height, 1), lipgloss.Center, lipgloss.Center, box)
}

// humanizeSince renders a coarse relative age like "3 weeks ago".
func humanizeSince(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d weeks ago", int(d.Hours()/24/7))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	default:
		return fmt.Sprintf("%d years ago", int(d.Hours()/24/365))
	}
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
