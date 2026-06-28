// Package github wraps the gh CLI and a PAT/keychain fallback for auth and PR
// flows. See docs/specs/02-github.md. No UI dependencies.
package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/seltonfiuza/gui/internal/git"
)

// Host is a GitHub hostname (github.com or an enterprise host).
type Host struct {
	Hostname string
}

// DefaultHost returns the github.com host.
func DefaultHost() Host { return Host{Hostname: "github.com"} }

// HostForRemote returns the host for a remote, defaulting to github.com.
func HostForRemote(r *git.Remote) Host {
	if r != nil && r.Host != "" {
		return Host{Hostname: r.Host}
	}
	return DefaultHost()
}

// AuthState describes auth for a host.
type AuthState struct {
	Hostname      string
	Authenticated bool
	Login         string
	Source        string // "gh" | "keychain" | ""
}

// PR is a pull request summary/detail.
type PR struct {
	Number        int
	Title         string
	State         string
	Author        string
	HeadRef       string
	BaseRef       string
	URL           string
	Body          string
	Draft         bool
	ChecksSummary string
}

// CreatePROpts are inputs to CreatePR.
type CreatePROpts struct {
	Title string
	Body  string
	Head  string
	Base  string
	Draft bool
}

// ErrNoToken indicates no token is available for the host.
var ErrNoToken = errors.New("no github token for host")

// Service performs GitHub operations for a host.
type Service struct {
	host Host
}

// New builds a Service for host.
func New(host Host) *Service { return &Service{host: host} }

func (s *Service) AuthStatus() (AuthState, error) { panic("not implemented") }
func (s *Service) Token() (string, error)         { panic("not implemented") }
func (s *Service) SetToken(pat string) error      { panic("not implemented") }
func (s *Service) ClearToken() error              { panic("not implemented") }

// ListPRs returns the open pull/merge requests for repo. GitLab hosts are
// queried via the glab CLI (merge requests); all other hosts via the gh CLI
// (pull requests). The chosen CLI must be installed and authenticated.
func (s *Service) ListPRs(repo *git.Remote) ([]PR, error) {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return nil, errors.New("no origin remote configured")
	}
	if isGitLab(repo.Host) {
		return listGitLabMRs(repo)
	}
	return listGitHubPRs(repo)
}

// ViewPR returns a single pull/merge request with its body (Body) and pipeline
// / checks status (ChecksSummary) populated.
func (s *Service) ViewPR(repo *git.Remote, number int) (PR, error) {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return PR{}, errors.New("no origin remote configured")
	}
	if isGitLab(repo.Host) {
		return viewGitLabMR(repo, number)
	}
	return viewGitHubPR(repo, number)
}

// PRDiff returns the unified diff for a single pull/merge request.
func (s *Service) PRDiff(repo *git.Remote, number int) (string, error) {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return "", errors.New("no origin remote configured")
	}
	if isGitLab(repo.Host) {
		return runCLI("glab", "mr", "diff", strconv.Itoa(number), "-R", prRepoArg(repo))
	}
	return runCLI("gh", "pr", "diff", strconv.Itoa(number), "-R", prRepoArg(repo))
}

// CreatePR opens a new pull/merge request for repo and returns its number/URL.
// GitHub uses `gh pr create`; GitLab uses `glab mr create`. The branch must
// already exist on the remote (the UI pushes it first).
func (s *Service) CreatePR(repo *git.Remote, o CreatePROpts) (PR, error) {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return PR{}, errors.New("no origin remote configured")
	}
	var out string
	var err error
	if isGitLab(repo.Host) {
		out, err = runCLI("glab", glabCreateArgs(repo, o)...)
	} else {
		out, err = runCLI("gh", ghCreateArgs(repo, o)...)
	}
	if err != nil {
		return PR{}, err
	}
	url := lastNonEmptyLine(out)
	return PR{
		Number:  prNumberFromURL(url),
		Title:   o.Title,
		URL:     url,
		HeadRef: o.Head,
		BaseRef: o.Base,
		Draft:   o.Draft,
	}, nil
}

// ghCreateArgs builds the `gh pr create` argument list.
func ghCreateArgs(repo *git.Remote, o CreatePROpts) []string {
	args := []string{
		"pr", "create",
		"-R", prRepoArg(repo),
		"--head", o.Head,
		"--base", o.Base,
		"--title", o.Title,
		"--body", o.Body,
	}
	if o.Draft {
		args = append(args, "--draft")
	}
	return args
}

// glabCreateArgs builds the `glab mr create` argument list. --yes skips the
// interactive prompts so the call is non-interactive.
func glabCreateArgs(repo *git.Remote, o CreatePROpts) []string {
	args := []string{
		"mr", "create",
		"-R", prRepoArg(repo),
		"--source-branch", o.Head,
		"--target-branch", o.Base,
		"--title", o.Title,
		"--description", o.Body,
		"--yes",
	}
	if o.Draft {
		args = append(args, "--draft")
	}
	return args
}

// errGitLabUnsupported is returned by approve/merge for GitLab remotes; the
// glab path is a deliberate v1 seam (see CreatePR's gh/glab split).
var errGitLabUnsupported = errors.New("approve/merge from gui is not yet supported for GitLab")

// MergeMethod selects how `gh pr merge` integrates the PR.
type MergeMethod int

const (
	MergeCommit MergeMethod = iota // --merge
	Squash                         // --squash
	Rebase                         // --rebase
)

// flag returns the gh merge-method flag for m.
func (m MergeMethod) flag() string {
	switch m {
	case Squash:
		return "--squash"
	case Rebase:
		return "--rebase"
	default:
		return "--merge"
	}
}

// ApprovePR submits an approving review for the PR via `gh pr review`.
func (s *Service) ApprovePR(repo *git.Remote, number int) error {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return errors.New("no origin remote configured")
	}
	if isGitLab(repo.Host) {
		return errGitLabUnsupported
	}
	_, err := runCLI("gh", ghApproveArgs(repo, number)...)
	return err
}

// MergePR merges the PR via `gh pr merge` using method, optionally deleting the
// head branch.
func (s *Service) MergePR(repo *git.Remote, number int, method MergeMethod, deleteBranch bool) error {
	if repo == nil || repo.Owner == "" || repo.Repo == "" {
		return errors.New("no origin remote configured")
	}
	if isGitLab(repo.Host) {
		return errGitLabUnsupported
	}
	_, err := runCLI("gh", ghMergeArgs(repo, number, method, deleteBranch)...)
	return err
}

// ghApproveArgs builds the `gh pr review --approve` argument list.
func ghApproveArgs(repo *git.Remote, number int) []string {
	return []string{
		"pr", "review", strconv.Itoa(number),
		"-R", prRepoArg(repo),
		"--approve",
	}
}

// ghMergeArgs builds the `gh pr merge` argument list.
func ghMergeArgs(repo *git.Remote, number int, method MergeMethod, deleteBranch bool) []string {
	args := []string{
		"pr", "merge", strconv.Itoa(number),
		"-R", prRepoArg(repo),
		method.flag(),
	}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	return args
}

// prNumberFromURL extracts the trailing integer from a PR/MR URL, e.g.
// ".../pull/42" or ".../merge_requests/7". Returns 0 when not parseable.
func prNumberFromURL(url string) int {
	trimmed := strings.TrimRight(strings.TrimSpace(url), "/")
	i := strings.LastIndex(trimmed, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(trimmed[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// lastNonEmptyLine returns the last non-blank line of s (the create CLIs print
// the new request URL last, possibly after warning lines).
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// viewGitLabMR fetches a single merge request via `glab mr view`.
func viewGitLabMR(repo *git.Remote, number int) (PR, error) {
	out, err := runCLI("glab", "mr", "view", strconv.Itoa(number),
		"-R", prRepoArg(repo), "--output", "json")
	if err != nil {
		return PR{}, err
	}
	var raw struct {
		IID         int    `json:"iid"`
		Title       string `json:"title"`
		State       string `json:"state"`
		Description string `json:"description"`
		Author      struct {
			Username string `json:"username"`
		} `json:"author"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		WebURL       string `json:"web_url"`
		Draft        bool   `json:"draft"`
		HeadPipeline *struct {
			Status string `json:"status"`
		} `json:"head_pipeline"`
		Pipeline *struct {
			Status string `json:"status"`
		} `json:"pipeline"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PR{}, fmt.Errorf("parse glab output: %w", err)
	}
	status := "none"
	if raw.HeadPipeline != nil && raw.HeadPipeline.Status != "" {
		status = raw.HeadPipeline.Status
	} else if raw.Pipeline != nil && raw.Pipeline.Status != "" {
		status = raw.Pipeline.Status
	}
	return PR{
		Number:        raw.IID,
		Title:         raw.Title,
		State:         raw.State,
		Author:        raw.Author.Username,
		HeadRef:       raw.SourceBranch,
		BaseRef:       raw.TargetBranch,
		URL:           raw.WebURL,
		Body:          raw.Description,
		Draft:         raw.Draft,
		ChecksSummary: status,
	}, nil
}

// viewGitHubPR fetches a single pull request via `gh pr view`.
func viewGitHubPR(repo *git.Remote, number int) (PR, error) {
	out, err := runCLI("gh", "pr", "view", strconv.Itoa(number),
		"-R", prRepoArg(repo),
		"--json", "number,title,state,author,headRefName,baseRefName,url,isDraft,body,statusCheckRollup")
	if err != nil {
		return PR{}, err
	}
	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Body   string `json:"body"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		HeadRefName       string `json:"headRefName"`
		BaseRefName       string `json:"baseRefName"`
		URL               string `json:"url"`
		IsDraft           bool   `json:"isDraft"`
		StatusCheckRollup []struct {
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PR{}, fmt.Errorf("parse gh output: %w", err)
	}
	return PR{
		Number:        raw.Number,
		Title:         raw.Title,
		State:         raw.State,
		Author:        raw.Author.Login,
		HeadRef:       raw.HeadRefName,
		BaseRef:       raw.BaseRefName,
		URL:           raw.URL,
		Body:          raw.Body,
		Draft:         raw.IsDraft,
		ChecksSummary: summarizeChecks(raw.StatusCheckRollup),
	}, nil
}

// summarizeChecks reduces a gh statusCheckRollup into a compact pass/fail/pending
// summary string.
func summarizeChecks(rollup []struct {
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}) string {
	if len(rollup) == 0 {
		return "none"
	}
	var pass, fail, pending int
	for _, c := range rollup {
		v := c.Conclusion
		if v == "" {
			v = c.State
		}
		switch strings.ToUpper(v) {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			pass++
		case "FAILURE", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			fail++
		default:
			pending++
		}
	}
	return fmt.Sprintf("%d passed · %d failed · %d pending", pass, fail, pending)
}

// isGitLab reports whether host is a GitLab instance (gitlab.com or a
// self-hosted instance such as gitlab.delfos.im).
func isGitLab(host string) bool { return strings.Contains(host, "gitlab") }

// prRepoArg returns the repository selector passed to gh/glab via -R. The
// original remote URL is preferred because it carries the host unambiguously
// (needed for self-hosted instances); it falls back to host/owner/repo.
func prRepoArg(repo *git.Remote) string {
	if repo.URL != "" {
		return repo.URL
	}
	if repo.Host != "" {
		return repo.Host + "/" + repo.Owner + "/" + repo.Repo
	}
	return repo.Owner + "/" + repo.Repo
}

// listGitHubPRs lists open pull requests via `gh pr list`.
func listGitHubPRs(repo *git.Remote) ([]PR, error) {
	out, err := runCLI("gh", "pr", "list",
		"-R", prRepoArg(repo),
		"--json", "number,title,state,author,headRefName,baseRefName,url,isDraft",
		"--limit", "50")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
		URL         string `json:"url"`
		IsDraft     bool   `json:"isDraft"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:  r.Number,
			Title:   r.Title,
			State:   r.State,
			Author:  r.Author.Login,
			HeadRef: r.HeadRefName,
			BaseRef: r.BaseRefName,
			URL:     r.URL,
			Draft:   r.IsDraft,
		})
	}
	return prs, nil
}

// listGitLabMRs lists open merge requests via `glab mr list`.
func listGitLabMRs(repo *git.Remote) ([]PR, error) {
	out, err := runCLI("glab", "mr", "list",
		"-R", prRepoArg(repo),
		"--output", "json")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		IID    int    `json:"iid"`
		Title  string `json:"title"`
		State  string `json:"state"`
		Author struct {
			Username string `json:"username"`
		} `json:"author"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		WebURL       string `json:"web_url"`
		Draft        bool   `json:"draft"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse glab output: %w", err)
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:  r.IID,
			Title:   r.Title,
			State:   r.State,
			Author:  r.Author.Username,
			HeadRef: r.SourceBranch,
			BaseRef: r.TargetBranch,
			URL:     r.WebURL,
			Draft:   r.Draft,
		})
	}
	return prs, nil
}

// runCLI executes an external CLI (gh/glab), returning stdout. On failure it
// returns an error including trimmed stderr.
func runCLI(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("%s: %s", name, msg)
	}
	return out.String(), nil
}
