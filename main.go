// Command gui is a terminal UI for Git diff, branch, and pull-request
// workflows. See TASK.md and docs/specs/.
package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/selton/gui/internal/git"
	"github.com/selton/gui/internal/ui"
)

// Build metadata, injected at release time via -ldflags -X (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
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
