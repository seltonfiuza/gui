// Command gui is a terminal UI for Git diff, branch, and pull-request
// workflows. See TASK.md and docs/specs/.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/seltonfiuza/gui/internal/git"
	"github.com/seltonfiuza/gui/internal/ui"
)

// Build metadata, injected at release time via -ldflags -X (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// resolveVersion falls back to the module version embedded by the Go toolchain
// when ldflags weren't applied — so `go install …@vX.Y.Z` binaries report their
// version instead of "dev". GoReleaser builds already set `version` via -X.
func resolveVersion() {
	if version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			version = v
		}
	}
}

func main() {
	resolveVersion()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Printf("gui %s (%s, %s)\n", version, commit, date)
			return
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gui:", err)
		os.Exit(1)
	}

	repo, err := git.Open(cwd)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			fmt.Fprintln(os.Stderr, "gui: not inside a git repository")
		} else {
			fmt.Fprintln(os.Stderr, "gui:", err)
		}
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(repo, version), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gui:", err)
		os.Exit(1)
	}
}
