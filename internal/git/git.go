// Package git is a thin wrapper over the git CLI exposing repo status, diffs,
// and branch operations. See docs/specs/01-git.md. No UI dependencies.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// StatusCode is the state of a file in the index or worktree.
type StatusCode int

const (
	Unmodified StatusCode = iota
	Added
	Modified
	Deleted
	Renamed
	Copied
	Untracked
	TypeChanged
	Unmerged
)

// ChangedFile is one path with index (staged) and worktree (unstaged) status.
type ChangedFile struct {
	Path     string
	OrigPath string // source path for renames/copies, else ""
	Staged   StatusCode
	Worktree StatusCode
}

// Status is the parsed result of `git status`.
type Status struct {
	Branch    string
	Upstream  string
	Ahead     int
	Behind    int
	Staged    []ChangedFile
	Unstaged  []ChangedFile
	Untracked []ChangedFile
}

// Branch is a local or remote-tracking branch.
type Branch struct {
	Name      string
	IsCurrent bool
	IsRemote  bool
	Upstream  string
}

// Remote identifies a GitHub remote.
type Remote struct {
	Name  string
	Owner string
	Repo  string
	Host  string
	URL   string
}

// Sentinel errors.
var (
	ErrNotARepo = errors.New("not a git repository")
	ErrNoRemote = errors.New("no origin remote configured")
)

// Service performs git operations rooted at a repository.
type Service struct {
	root string
}

// Open locates the repository root for startDir. Returns ErrNotARepo if none.
func Open(startDir string) (*Service, error) {
	cmd := exec.Command("git", "-C", startDir, "rev-parse", "--show-toplevel")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Most failures here mean "not in a git repository".
		return nil, fmt.Errorf("%w: %s", ErrNotARepo, strings.TrimSpace(stderr.String()))
	}
	root := strings.TrimSpace(out.String())
	if root == "" {
		return nil, fmt.Errorf("%w", ErrNotARepo)
	}
	return &Service{root: root}, nil
}

// Root returns the repository top-level directory.
func (s *Service) Root() string { return s.root }

// run executes git with args rooted at the repo, returning stdout. On failure
// it returns an error including trimmed stderr.
func (s *Service) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.root
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		sub := ""
		if len(args) > 0 {
			sub = args[0]
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", sub, msg)
	}
	return out.String(), nil
}

// Status parses the current working tree and index state.
func (s *Service) Status() (*Status, error) {
	out, err := s.run("status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return nil, err
	}
	return parseStatus(out)
}

// parseStatus parses the NUL-separated porcelain v2 output.
func parseStatus(out string) (*Status, error) {
	st := &Status{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		switch {
		case strings.HasPrefix(rec, "# "):
			parseHeader(strings.TrimPrefix(rec, "# "), st)
		case strings.HasPrefix(rec, "1 "):
			cf := parseOrdinary(rec)
			placeFile(st, cf)
		case strings.HasPrefix(rec, "2 "):
			// Rename/copy: the original path is the next NUL-separated field.
			orig := ""
			if i+1 < len(fields) {
				orig = fields[i+1]
				i++
			}
			cf := parseRename(rec, orig)
			placeFile(st, cf)
		case strings.HasPrefix(rec, "u "):
			cf := parseUnmerged(rec)
			placeFile(st, cf)
		case strings.HasPrefix(rec, "? "):
			path := strings.TrimPrefix(rec, "? ")
			st.Untracked = append(st.Untracked, ChangedFile{
				Path:     path,
				Worktree: Untracked,
			})
		case strings.HasPrefix(rec, "! "):
			// Ignored: skip.
		}
	}
	if st.Branch == "" {
		st.Branch = "(detached)"
	}
	return st, nil
}

func parseHeader(h string, st *Status) {
	switch {
	case strings.HasPrefix(h, "branch.head "):
		v := strings.TrimPrefix(h, "branch.head ")
		if v == "(detached)" {
			st.Branch = "(detached)"
		} else {
			st.Branch = v
		}
	case strings.HasPrefix(h, "branch.upstream "):
		st.Upstream = strings.TrimPrefix(h, "branch.upstream ")
	case strings.HasPrefix(h, "branch.ab "):
		ab := strings.TrimPrefix(h, "branch.ab ")
		parts := strings.Fields(ab)
		for _, p := range parts {
			if len(p) < 2 {
				continue
			}
			n, err := strconv.Atoi(p[1:])
			if err != nil {
				continue
			}
			switch p[0] {
			case '+':
				st.Ahead = n
			case '-':
				st.Behind = n
			}
		}
	}
}

// parseOrdinary parses a "1" record:
// 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
func parseOrdinary(rec string) ChangedFile {
	parts := strings.SplitN(rec, " ", 9)
	xy := ""
	path := ""
	if len(parts) >= 2 {
		xy = parts[1]
	}
	if len(parts) >= 9 {
		path = parts[8]
	}
	return changedFromXY(xy, path, "")
}

// parseRename parses a "2" record:
// 2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <Xscore> <path>
// with orig path supplied separately.
func parseRename(rec, orig string) ChangedFile {
	parts := strings.SplitN(rec, " ", 10)
	xy := ""
	path := ""
	if len(parts) >= 2 {
		xy = parts[1]
	}
	if len(parts) >= 10 {
		path = parts[9]
	}
	cf := changedFromXY(xy, path, orig)
	return cf
}

// parseUnmerged parses a "u" record:
// u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
func parseUnmerged(rec string) ChangedFile {
	parts := strings.SplitN(rec, " ", 11)
	path := ""
	if len(parts) >= 11 {
		path = parts[10]
	}
	return ChangedFile{
		Path:     path,
		Staged:   Unmerged,
		Worktree: Unmerged,
	}
}

// changedFromXY builds a ChangedFile from the XY status chars.
func changedFromXY(xy, path, orig string) ChangedFile {
	cf := ChangedFile{Path: path, OrigPath: orig}
	if len(xy) == 2 {
		cf.Staged = mapCode(xy[0])
		cf.Worktree = mapCode(xy[1])
	}
	return cf
}

// mapCode maps a porcelain XY status char to a StatusCode.
func mapCode(c byte) StatusCode {
	switch c {
	case '.':
		return Unmodified
	case 'M':
		return Modified
	case 'A':
		return Added
	case 'D':
		return Deleted
	case 'R':
		return Renamed
	case 'C':
		return Copied
	case 'U':
		return Unmerged
	case 'T':
		return TypeChanged
	case '?':
		return Untracked
	case '!':
		return Unmodified // ignored; not surfaced
	default:
		return Unmodified
	}
}

// placeFile appends cf to Staged and/or Unstaged based on its codes.
func placeFile(st *Status, cf ChangedFile) {
	if cf.Staged == Unmerged || cf.Worktree == Unmerged {
		// Unmerged files appear in both groups so the UI can surface them.
		st.Unstaged = append(st.Unstaged, cf)
		return
	}
	if cf.Staged != Unmodified {
		st.Staged = append(st.Staged, cf)
	}
	if cf.Worktree != Unmodified {
		st.Unstaged = append(st.Unstaged, cf)
	}
}

// Diff returns the unified diff for path; staged selects the index diff.
func (s *Service) Diff(path string, staged bool) (string, error) {
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	out, err := s.run(args...)
	if err != nil {
		return "", err
	}
	if !staged && strings.TrimSpace(out) == "" {
		// Possibly an untracked file: git diff yields nothing for it.
		// Use --no-index against /dev/null to synthesize an "added" diff.
		if synth, ok := s.diffUntracked(path); ok {
			return synth, nil
		}
	}
	return out, nil
}

// diffUntracked produces a synthetic diff for an untracked file via --no-index.
// git diff --no-index exits non-zero (1) when files differ, which is expected,
// so we run it directly and accept stdout regardless of exit status.
func (s *Service) diffUntracked(path string) (string, bool) {
	cmd := exec.Command("git", "diff", "--no-color", "--no-index", "--", "/dev/null", path)
	cmd.Dir = s.root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	_ = cmd.Run() // exit code 1 means "differences found" — not an error here.
	if out.Len() == 0 {
		return "", false
	}
	return out.String(), true
}

// OriginRemote returns the parsed origin remote, or ErrNoRemote.
func (s *Service) OriginRemote() (*Remote, error) {
	out, err := s.run("remote", "get-url", "origin")
	if err != nil {
		return nil, fmt.Errorf("%w", ErrNoRemote)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return nil, fmt.Errorf("%w", ErrNoRemote)
	}
	return parseRemote(url), nil
}

// parseRemote parses common ssh and https remote URL forms.
func parseRemote(url string) *Remote {
	r := &Remote{Name: "origin", URL: url}
	host, ownerRepo := splitRemote(url)
	r.Host = host
	if ownerRepo != "" {
		ownerRepo = strings.TrimSuffix(ownerRepo, ".git")
		ownerRepo = strings.Trim(ownerRepo, "/")
		// owner is everything up to the last segment's parent; standard form
		// is owner/repo. If more nesting exists, repo is the last segment and
		// owner is the remainder joined.
		idx := strings.LastIndex(ownerRepo, "/")
		if idx >= 0 {
			r.Owner = ownerRepo[:idx]
			r.Repo = ownerRepo[idx+1:]
		} else {
			r.Repo = ownerRepo
		}
	}
	return r
}

// splitRemote returns (host, ownerRepoPath) for an url, best-effort.
func splitRemote(url string) (host, path string) {
	switch {
	case strings.HasPrefix(url, "ssh://"):
		rest := strings.TrimPrefix(url, "ssh://")
		rest = stripUserInfo(rest)
		host, path = splitHostPath(rest)
	case strings.HasPrefix(url, "https://"):
		rest := strings.TrimPrefix(url, "https://")
		rest = stripUserInfo(rest)
		host, path = splitHostPath(rest)
	case strings.HasPrefix(url, "http://"):
		rest := strings.TrimPrefix(url, "http://")
		rest = stripUserInfo(rest)
		host, path = splitHostPath(rest)
	case strings.HasPrefix(url, "git://"):
		rest := strings.TrimPrefix(url, "git://")
		host, path = splitHostPath(rest)
	case strings.Contains(url, "@") && strings.Contains(url, ":") && !strings.Contains(url, "://"):
		// scp-like: [user@]host:owner/repo(.git)
		rest := stripUserInfo(url)
		if i := strings.Index(rest, ":"); i >= 0 {
			host = rest[:i]
			path = rest[i+1:]
		}
	default:
		// Unknown form: leave host empty, treat whole thing as path-ish.
		path = ""
	}
	// Strip any port from host (host:port).
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	return host, path
}

// stripUserInfo removes a leading "user@" from a host segment.
func stripUserInfo(s string) string {
	if i := strings.Index(s, "@"); i >= 0 {
		// Only strip if "@" comes before the first "/" (i.e. in the authority).
		slash := strings.Index(s, "/")
		if slash == -1 || i < slash {
			return s[i+1:]
		}
	}
	return s
}

// splitHostPath splits "host/owner/repo" into host and "owner/repo".
func splitHostPath(s string) (host, path string) {
	i := strings.Index(s, "/")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func (s *Service) Stage(path string) error {
	_, err := s.run("add", "--", path)
	return err
}

func (s *Service) Unstage(path string) error {
	_, err := s.run("restore", "--staged", "--", path)
	return err
}

func (s *Service) Discard(path string) error {
	_, err := s.run("restore", "--", path)
	return err
}

func (s *Service) StageAll() error {
	_, err := s.run("add", "-A")
	return err
}

func (s *Service) UnstageAll() error {
	_, err := s.run("restore", "--staged", "--", ".")
	return err
}

// Branches returns local and remote-tracking branches, current marked.
func (s *Service) Branches() ([]Branch, error) {
	var branches []Branch

	localOut, err := s.run("branch",
		"--format=%(refname:short)%09%(HEAD)%09%(upstream:short)")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(localOut, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		name := cols[0]
		if name == "" {
			continue
		}
		b := Branch{Name: name}
		if len(cols) >= 2 && cols[1] == "*" {
			b.IsCurrent = true
		}
		if len(cols) >= 3 {
			b.Upstream = cols[2]
		}
		branches = append(branches, b)
	}

	remoteOut, err := s.run("branch", "-r",
		"--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(remoteOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip the symbolic origin/HEAD ref.
		if line == "origin/HEAD" || strings.HasSuffix(line, "/HEAD") {
			continue
		}
		branches = append(branches, Branch{Name: line, IsRemote: true})
	}

	return branches, nil
}

// CurrentBranch returns the short name of HEAD's branch.
func (s *Service) CurrentBranch() (string, error) {
	out, err := s.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (s *Service) Checkout(name string) error {
	_, err := s.run("checkout", name)
	return err
}

func (s *Service) CreateBranch(name, startPoint string) error {
	args := []string{"checkout", "-b", name}
	if startPoint != "" {
		args = append(args, startPoint)
	}
	_, err := s.run(args...)
	return err
}

func (s *Service) DeleteBranch(name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := s.run("branch", flag, name)
	return err
}

// IsMerged reports whether name is contained in `git branch --merged`.
func (s *Service) IsMerged(name string) (bool, error) {
	out, err := s.run("branch", "--merged")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) Rebase(onto string) error {
	_, err := s.run("rebase", onto)
	return err
}

// RebaseInProgress reports whether a rebase is currently in progress.
func (s *Service) RebaseInProgress() (bool, error) {
	out, err := s.run("rev-parse", "--git-dir")
	if err != nil {
		return false, err
	}
	gitDir := strings.TrimSpace(out)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(s.root, gitDir)
	}
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if dirExists(filepath.Join(gitDir, d)) {
			return true, nil
		}
	}
	return false, nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
