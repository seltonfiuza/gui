package git

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the test if git is not on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// runGit runs a hermetic git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{
		"-c", "user.email=test@test",
		"-c", "user.name=test",
		"-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main",
		"-c", "protocol.file.allow=always",
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return out.String()
}

// writeFile writes content to a path relative to dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo creates a fixture repo with an initial commit on branch main.
func newRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	writeFile(t, dir, "README.md", "hello\n")
	writeFile(t, dir, "keep.txt", "original\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func codeNames(c StatusCode) string {
	names := map[StatusCode]string{
		Unmodified: "Unmodified", Added: "Added", Modified: "Modified",
		Deleted: "Deleted", Renamed: "Renamed", Copied: "Copied",
		Untracked: "Untracked", TypeChanged: "TypeChanged", Unmerged: "Unmerged",
	}
	return names[c]
}

func findFile(files []ChangedFile, path string) (ChangedFile, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return ChangedFile{}, false
}

func TestOpen(t *testing.T) {
	dir := newRepo(t)

	t.Run("opens repo", func(t *testing.T) {
		svc, err := Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		// Root may differ by symlink resolution (/var vs /private/var on macOS).
		gotEval, _ := filepath.EvalSymlinks(svc.Root())
		wantEval, _ := filepath.EvalSymlinks(dir)
		if gotEval != wantEval {
			t.Errorf("Root = %q, want %q", svc.Root(), dir)
		}
	})

	t.Run("opens from subdir", func(t *testing.T) {
		sub := filepath.Join(dir, "nested")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		svc, err := Open(sub)
		if err != nil {
			t.Fatalf("Open subdir: %v", err)
		}
		gotEval, _ := filepath.EvalSymlinks(svc.Root())
		wantEval, _ := filepath.EvalSymlinks(dir)
		if gotEval != wantEval {
			t.Errorf("Root = %q, want %q", svc.Root(), dir)
		}
	})

	t.Run("non-repo returns ErrNotARepo", func(t *testing.T) {
		requireGit(t)
		nonRepo := t.TempDir()
		_, err := Open(nonRepo)
		if !errors.Is(err, ErrNotARepo) {
			t.Fatalf("expected ErrNotARepo, got %v", err)
		}
	})
}

func TestStatusStagedAdd(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "new.txt", "added file\n")
	runGit(t, dir, "add", "new.txt")

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "main" {
		t.Errorf("Branch = %q, want main", st.Branch)
	}
	f, ok := findFile(st.Staged, "new.txt")
	if !ok {
		t.Fatalf("new.txt not in Staged: %+v", st.Staged)
	}
	if f.Staged != Added {
		t.Errorf("Staged code = %s, want Added", codeNames(f.Staged))
	}
}

func TestStatusModifiedUnstaged(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "keep.txt", "changed\n")

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(st.Unstaged, "keep.txt")
	if !ok {
		t.Fatalf("keep.txt not in Unstaged: %+v", st.Unstaged)
	}
	if f.Worktree != Modified {
		t.Errorf("Worktree code = %s, want Modified", codeNames(f.Worktree))
	}
	if _, ok := findFile(st.Staged, "keep.txt"); ok {
		t.Errorf("keep.txt should not be staged")
	}
}

func TestStatusDeleted(t *testing.T) {
	dir := newRepo(t)
	if err := os.Remove(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal(err)
	}

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(st.Unstaged, "keep.txt")
	if !ok {
		t.Fatalf("keep.txt not in Unstaged: %+v", st.Unstaged)
	}
	if f.Worktree != Deleted {
		t.Errorf("Worktree code = %s, want Deleted", codeNames(f.Worktree))
	}
}

func TestStatusRenamed(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "mv", "keep.txt", "renamed.txt")

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(st.Staged, "renamed.txt")
	if !ok {
		t.Fatalf("renamed.txt not in Staged: %+v", st.Staged)
	}
	if f.Staged != Renamed {
		t.Errorf("Staged code = %s, want Renamed", codeNames(f.Staged))
	}
	if f.OrigPath != "keep.txt" {
		t.Errorf("OrigPath = %q, want keep.txt", f.OrigPath)
	}
}

func TestStatusUntracked(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "untracked.txt", "loose\n")

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(st.Untracked, "untracked.txt")
	if !ok {
		t.Fatalf("untracked.txt not in Untracked: %+v", st.Untracked)
	}
	if f.Worktree != Untracked {
		t.Errorf("Worktree code = %s, want Untracked", codeNames(f.Worktree))
	}
}

func TestStatusStagedAndUnstaged(t *testing.T) {
	dir := newRepo(t)
	// Stage one change, then modify again in the worktree.
	writeFile(t, dir, "keep.txt", "v2\n")
	runGit(t, dir, "add", "keep.txt")
	writeFile(t, dir, "keep.txt", "v3\n")

	svc, _ := Open(dir)
	st, _ := svc.Status()
	if _, ok := findFile(st.Staged, "keep.txt"); !ok {
		t.Errorf("keep.txt should be in Staged")
	}
	if _, ok := findFile(st.Unstaged, "keep.txt"); !ok {
		t.Errorf("keep.txt should be in Unstaged")
	}
}

func TestStatusDetachedHead(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "second.txt", "x\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "second")
	runGit(t, dir, "checkout", "--detach", "HEAD")

	svc, _ := Open(dir)
	st, err := svc.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "(detached)" {
		t.Errorf("Branch = %q, want (detached)", st.Branch)
	}
}

func TestStageUnstageDiscardRoundTrip(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	writeFile(t, dir, "keep.txt", "modified\n")

	// Stage.
	if err := svc.Stage("keep.txt"); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Status()
	if _, ok := findFile(st.Staged, "keep.txt"); !ok {
		t.Fatalf("after Stage, keep.txt not staged")
	}

	// Unstage.
	if err := svc.Unstage("keep.txt"); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Status()
	if _, ok := findFile(st.Staged, "keep.txt"); ok {
		t.Fatalf("after Unstage, keep.txt still staged")
	}
	if _, ok := findFile(st.Unstaged, "keep.txt"); !ok {
		t.Fatalf("after Unstage, keep.txt should be unstaged-modified")
	}

	// Discard.
	if err := svc.Discard("keep.txt"); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Status()
	if _, ok := findFile(st.Unstaged, "keep.txt"); ok {
		t.Fatalf("after Discard, keep.txt still modified")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if string(got) != "original\n" {
		t.Errorf("after Discard, content = %q, want original", string(got))
	}
}

func TestStageAllUnstageAll(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	writeFile(t, dir, "keep.txt", "x\n")
	writeFile(t, dir, "fresh.txt", "y\n")

	if err := svc.StageAll(); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Status()
	if len(st.Staged) != 2 {
		t.Errorf("after StageAll, Staged len = %d, want 2: %+v", len(st.Staged), st.Staged)
	}
	if len(st.Untracked) != 0 {
		t.Errorf("after StageAll, Untracked should be empty: %+v", st.Untracked)
	}

	if err := svc.UnstageAll(); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.Status()
	if len(st.Staged) != 0 {
		t.Errorf("after UnstageAll, Staged should be empty: %+v", st.Staged)
	}
}

func TestDiffTracked(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)
	writeFile(t, dir, "keep.txt", "changed line\n")

	d, err := svc.Diff("keep.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(d), []byte("changed line")) {
		t.Errorf("unstaged diff missing change:\n%s", d)
	}

	runGit(t, dir, "add", "keep.txt")
	ds, err := svc.Diff("keep.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(ds), []byte("changed line")) {
		t.Errorf("staged diff missing change:\n%s", ds)
	}
}

func TestDiffUntracked(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)
	writeFile(t, dir, "loose.txt", "brand new\n")

	d, err := svc.Diff("loose.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(d), []byte("brand new")) {
		t.Errorf("untracked diff missing content:\n%s", d)
	}
}

func TestOriginRemoteNone(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)
	_, err := svc.OriginRemote()
	if !errors.Is(err, ErrNoRemote) {
		t.Fatalf("expected ErrNoRemote, got %v", err)
	}
}

func TestOriginRemoteFixture(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
	svc, _ := Open(dir)
	r, err := svc.OriginRemote()
	if err != nil {
		t.Fatal(err)
	}
	if r.Host != "github.com" || r.Owner != "acme" || r.Repo != "widget" {
		t.Errorf("parsed = %+v", r)
	}
	if r.Name != "origin" {
		t.Errorf("Name = %q, want origin", r.Name)
	}
}

func TestParseRemote(t *testing.T) {
	cases := []struct {
		url, host, owner, repo string
	}{
		{"git@github.com:acme/widget.git", "github.com", "acme", "widget"},
		{"git@github.com:acme/widget", "github.com", "acme", "widget"},
		{"ssh://git@github.com/acme/widget.git", "github.com", "acme", "widget"},
		{"ssh://git@github.com:22/acme/widget.git", "github.com", "acme", "widget"},
		{"https://github.com/acme/widget.git", "github.com", "acme", "widget"},
		{"https://github.com/acme/widget", "github.com", "acme", "widget"},
		{"https://user@gitlab.com/acme/widget.git", "gitlab.com", "acme", "widget"},
		{"git://github.com/acme/widget.git", "github.com", "acme", "widget"},
		{"https://example.com/group/sub/widget.git", "example.com", "group/sub", "widget"},
	}
	for _, c := range cases {
		r := parseRemote(c.url)
		if r.URL != c.url {
			t.Errorf("%s: URL = %q", c.url, r.URL)
		}
		if r.Host != c.host {
			t.Errorf("%s: Host = %q, want %q", c.url, r.Host, c.host)
		}
		if r.Owner != c.owner {
			t.Errorf("%s: Owner = %q, want %q", c.url, r.Owner, c.owner)
		}
		if r.Repo != c.repo {
			t.Errorf("%s: Repo = %q, want %q", c.url, r.Repo, c.repo)
		}
	}
}

func TestParseRemoteNonStandard(t *testing.T) {
	// A weird URL must not panic and must keep URL/Host robust.
	r := parseRemote("https://example.com")
	if r.URL != "https://example.com" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.Host != "example.com" {
		t.Errorf("Host = %q, want example.com", r.Host)
	}
}

func TestBranchesAndCurrent(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	cur, err := svc.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if cur != "main" {
		t.Errorf("CurrentBranch = %q, want main", cur)
	}

	branches, err := svc.Branches()
	if err != nil {
		t.Fatal(err)
	}
	var main Branch
	found := false
	for _, b := range branches {
		if b.Name == "main" {
			main = b
			found = true
		}
	}
	if !found {
		t.Fatalf("main not in branches: %+v", branches)
	}
	if !main.IsCurrent {
		t.Errorf("main should be current")
	}
	if main.IsRemote {
		t.Errorf("main should not be remote")
	}
}

func TestCreateCheckoutDeleteIsMerged(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	// Create feature branch off HEAD and switch to it.
	if err := svc.CreateBranch("feature", ""); err != nil {
		t.Fatal(err)
	}
	cur, _ := svc.CurrentBranch()
	if cur != "feature" {
		t.Fatalf("after CreateBranch, current = %q, want feature", cur)
	}

	// feature has no new commits, so it is merged into main.
	merged, err := svc.IsMerged("feature")
	if err != nil {
		t.Fatal(err)
	}
	if !merged {
		t.Errorf("feature should be merged into HEAD")
	}

	// Switch back to main and delete feature.
	if err := svc.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteBranch("feature", false); err != nil {
		t.Fatal(err)
	}
	branches, _ := svc.Branches()
	for _, b := range branches {
		if b.Name == "feature" {
			t.Errorf("feature should be deleted")
		}
	}
}

func TestDeleteBranchForce(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	if err := svc.CreateBranch("wip", ""); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "wip.txt", "work\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "wip commit")

	if err := svc.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	// Non-force delete of an unmerged branch must fail.
	if err := svc.DeleteBranch("wip", false); err == nil {
		t.Errorf("expected error deleting unmerged branch without force")
	}
	// Force delete succeeds.
	if err := svc.DeleteBranch("wip", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
}

func TestRebase(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	// main gets an extra commit.
	writeFile(t, dir, "base.txt", "base\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "base advance")

	// topic branches off the original commit.
	runGit(t, dir, "checkout", "-b", "topic", "HEAD~1")
	writeFile(t, dir, "topic.txt", "topic\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "topic work")

	if err := svc.Rebase("main"); err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	// After a clean rebase, base.txt (from main) should now exist on topic.
	if _, err := os.Stat(filepath.Join(dir, "base.txt")); err != nil {
		t.Errorf("base.txt missing after rebase: %v", err)
	}
}

func TestRebaseInProgress(t *testing.T) {
	dir := newRepo(t)
	svc, _ := Open(dir)

	inProgress, err := svc.RebaseInProgress()
	if err != nil {
		t.Fatal(err)
	}
	if inProgress {
		t.Errorf("no rebase should be in progress on a clean repo")
	}
}
