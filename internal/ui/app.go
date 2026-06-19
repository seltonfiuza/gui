// Package ui implements the Bubble Tea UI layer for gui. The root App model
// owns the git.Service and config.Dispatcher, routes keys through the leader
// chord dispatcher, and renders the active view (diff / branch / help). All
// blocking git calls run inside tea.Cmds; Update never blocks.
package ui

import (
	"errors"
	"fmt"
	"strings"

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

	toast  string
	width  int
	height int
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
		branch:     branchpanel.New(),
		help:       help.New(),
	}
}

// Init fires the initial status + remote loads.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.loadStatusCmd(), a.loadRemoteCmd())
}

// ---- commands ----

func (a *App) loadStatusCmd() tea.Cmd {
	repo := a.repo
	return func() tea.Msg {
		s, err := repo.Status()
		return statusMsg{status: s, err: err}
	}
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
		a.diff.SetSize(msg.Width, a.bodyHeight())
		return a, nil

	case statusMsg:
		if msg.err != nil {
			a.toast = "status: " + msg.err.Error()
			return a, nil
		}
		a.status = msg.status
		a.diff.SetStatus(msg.status)
		return a, a.refreshDiffCmd()

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
	// Success: refresh everything relevant.
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
	case config.ActDown:
		a.diff.CursorDown()
		return a, a.refreshDiffCmd()
	case config.ActUp:
		a.diff.CursorUp()
		return a, a.refreshDiffCmd()
	case config.ActConfirm:
		// Open/focus diff: just ensure it's loaded.
		return a, a.refreshDiffCmd()
	case config.ActStageToggle:
		return a, a.stageToggleCmd()
	case config.ActUndo:
		return a.discardSelected()
	case config.ActCancel:
		return a, nil
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
	if a.diff.DiffPath() == row.File.Path {
		return nil
	}
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

func (a *App) discardSelected() (tea.Model, tea.Cmd) {
	row, ok := a.diff.Selected()
	if !ok {
		return a, nil
	}
	path := row.File.Path
	repo := a.repo

	destructive := row.Group == diffview.GroupUntracked || hasBothStagedAndUnstaged(a.status, path)
	doDiscard := func(app *App) tea.Cmd {
		return fileMutationCmd(func() error { return repo.Discard(path) })
	}
	if destructive {
		a.confirm = &confirmState{
			prompt: fmt.Sprintf("Discard changes to '%s'? This cannot be undone. (y/n)", path),
			onYes:  doDiscard,
		}
		return a, nil
	}
	return a, doDiscard(a)
}

func hasBothStagedAndUnstaged(s *git.Status, path string) bool {
	if s == nil {
		return false
	}
	staged, unstaged := false, false
	for _, f := range s.Staged {
		if f.Path == path {
			staged = true
		}
	}
	for _, f := range s.Unstaged {
		if f.Path == path {
			unstaged = true
		}
	}
	return staged && unstaged
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
	hint := "j/k move · s stage · u discard · r refresh · space b branches · ? help · q quit"
	if a.confirm != nil {
		return styles.Confirm.Render(a.confirm.prompt)
	}
	line := styles.Hint.Render(hint)
	if a.toast != "" {
		line = styles.Toast.Render(a.toast)
	}
	return styles.Header.Render(line)
}
