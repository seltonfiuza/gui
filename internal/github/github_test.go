package github

import (
	"testing"

	"github.com/seltonfiuza/gui/internal/git"
)

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// pairAfter returns the element immediately following the first occurrence of
// flag, or "" if flag is absent or last.
func pairAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestGhCreateArgs(t *testing.T) {
	repo := &git.Remote{Owner: "o", Repo: "r", Host: "github.com"}
	args := ghCreateArgs(repo, CreatePROpts{Title: "T", Body: "B", Head: "feat", Base: "main"})
	if args[0] != "pr" || args[1] != "create" {
		t.Fatalf("args head = %v, want pr create", args[:2])
	}
	if pairAfter(args, "--head") != "feat" {
		t.Errorf("--head = %q, want feat", pairAfter(args, "--head"))
	}
	if pairAfter(args, "--base") != "main" {
		t.Errorf("--base = %q, want main", pairAfter(args, "--base"))
	}
	if pairAfter(args, "--title") != "T" {
		t.Errorf("--title = %q, want T", pairAfter(args, "--title"))
	}
	if pairAfter(args, "--body") != "B" {
		t.Errorf("--body = %q, want B", pairAfter(args, "--body"))
	}
	if contains(args, "--draft") {
		t.Error("--draft present when Draft is false")
	}
}

func TestGhCreateArgsDraft(t *testing.T) {
	repo := &git.Remote{Owner: "o", Repo: "r", Host: "github.com"}
	args := ghCreateArgs(repo, CreatePROpts{Title: "T", Head: "feat", Base: "main", Draft: true})
	if !contains(args, "--draft") {
		t.Error("--draft missing when Draft is true")
	}
}

func TestGlabCreateArgs(t *testing.T) {
	repo := &git.Remote{Owner: "o", Repo: "r", Host: "gitlab.com"}
	args := glabCreateArgs(repo, CreatePROpts{Title: "T", Body: "B", Head: "feat", Base: "main", Draft: true})
	if args[0] != "mr" || args[1] != "create" {
		t.Fatalf("args head = %v, want mr create", args[:2])
	}
	if pairAfter(args, "--source-branch") != "feat" {
		t.Errorf("--source-branch = %q, want feat", pairAfter(args, "--source-branch"))
	}
	if pairAfter(args, "--target-branch") != "main" {
		t.Errorf("--target-branch = %q, want main", pairAfter(args, "--target-branch"))
	}
	if pairAfter(args, "--description") != "B" {
		t.Errorf("--description = %q, want B", pairAfter(args, "--description"))
	}
	if !contains(args, "--yes") {
		t.Error("--yes missing (needed for non-interactive create)")
	}
	if !contains(args, "--draft") {
		t.Error("--draft missing when Draft is true")
	}
}

func TestPRNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/o/r/pull/42":               42,
		"https://gitlab.com/o/r/-/merge_requests/7":    7,
		"https://github.com/o/r/pull/42\n":             42,
		"not a url":                                    0,
		"":                                             0,
	}
	for in, want := range cases {
		if got := prNumberFromURL(in); got != want {
			t.Errorf("prNumberFromURL(%q) = %d, want %d", in, got, want)
		}
	}
}
