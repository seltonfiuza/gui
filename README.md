# gui — Git TUI

A keyboard-driven terminal UI for local Git workflows: view diffs, stage/unstage,
discard changes, and manage branches — aiming for parity with the VS Code / Cursor
**Source Control** experience for day-to-day work in the current repository.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). It shells out
to the `git` CLI for every operation (no Git protocol reimplementation) and does
**not** talk to GitHub/GitLab or any remote host.

## Install / run

```sh
go build -o gui .
./gui          # run from anywhere inside a git repository
```

Or directly:

```sh
go run .
```

On launch it detects the repository root (`git rev-parse --show-toplevel`) and
opens straight into the diff view for the current working tree — no subcommand
needed. If you are not inside a git repository it prints a clear message and exits.

## Layout

```
┌ origin/main · feature ↑2↓1 · owner/repo · /path/to/project   ← header (FR-1)
│ Staged        A  newfile.go                                   ← file list (FR-2)
│ Unstaged      M  main.go
│ Untracked     ?  scratch.txt
│ ───────────────────────────────────────────────
│ <unified diff of the selected file, colorized>               ← scrollable diff pane
└ <toast / hint footer>
```

- **Header** shows the current branch, ahead/behind vs. upstream (when configured),
  the `origin` identity (`owner/repo` or host) when a remote exists, and the repo path.
- **File list** groups changes into **Staged**, **Unstaged**, and **Untracked**, with
  status glyphs `A`/`M`/`D`/`R`/`?`/`C`/`U`. Renames render as `orig → new`.
- **Diff pane** shows the unified diff of the selected file (`+` green, `−` red,
  `@@` cyan) in a scrollable viewport.
- A clean tree shows a friendly *"nothing to commit, working tree clean"* message.

## Keymap

Leader key defaults to **`Space`**.

| Binding            | Action                                                   |
|--------------------|----------------------------------------------------------|
| `j` / `↓`          | Move selection down                                      |
| `k` / `↑`          | Move selection up                                        |
| `Enter`            | Open / focus the diff for the selected file              |
| `s`                | Stage or unstage the selected file                       |
| `u`                | Discard worktree change for the selected file (confirms if destructive) |
| `r`                | Refresh Git status                                       |
| `<leader> b`       | Open the branch panel                                    |
| `?`                | Toggle the help / keymap overlay                         |
| `Esc`              | Close an overlay / cancel                                |
| `q`                | Quit (warns if a git operation is in progress)           |
| `Ctrl+C`           | Quit immediately                                         |

Destructive discards (untracked files, or files with both staged and unstaged
changes) prompt for confirmation before running.

### Branch panel (`<leader> b`)

A modal overlay listing **Local** branches (current marked `*`) and
**Remote-tracking** branches.

| Binding        | Action                                                       |
|----------------|--------------------------------------------------------------|
| `j` / `k`      | Navigate branches                                            |
| `Enter` / `c`  | Checkout the selected branch                                 |
| `n`            | Create a branch (prompts for a name, from the selected ref)  |
| `d`            | Delete the selected local branch (escalates to force if unmerged) |
| `R`            | Rebase the current branch onto the selected branch           |
| `Esc`          | Close the panel                                              |

Delete (when unmerged) and rebase require explicit confirmation. Rebase
conflicts surface an inline message; resolve them with the `git` CLI.

## Architecture

```
main.go                 # entrypoint: open repo, build root model, run program
internal/
  git/                  # git CLI wrapper + domain types (Status, ChangedFile, Branch). No UI deps.
  config/               # keymap, leader-chord dispatcher, prefs persistence. No UI deps.
  ui/
    app.go              # root model: layout, key dispatch, leader chord, view routing
    diffview/           # header + file list + diff pane (FR-1, FR-2)
    branchpanel/        # branch overlay (FR-3)
    help/               # `?` keymap overlay (FR-4)
    styles/             # shared lipgloss styles
docs/specs/             # spec-driven development: architecture, git, keymap, ui
```

The exported APIs of `internal/git` and `internal/config` are frozen contracts;
the UI codes against them. All blocking git calls run inside Bubble Tea `tea.Cmd`s
so the UI goroutine never blocks, and git errors are surfaced as human-readable
toasts/inline messages rather than panics. See `docs/specs/` and `TASK.md`.

## Development

```sh
go build ./...     # build everything
go vet ./...       # static checks
go test ./...      # unit tests (git parsing/mutations, keymap dispatch, UI transitions)
```

`internal/git` tests run against hermetic temp fixture repos and skip if `git`
is not on `PATH`. **Platform:** developed and verified on macOS; the code is
portable Go and intended to build on Linux.

## Scope

In scope: local diff/stage/unstage/discard and branch checkout/create/delete/rebase.
Out of scope (per `TASK.md`): GitHub/GitLab/Bitbucket auth or API, pull requests,
issues, the `gh` CLI, full IDE integration, and non-Git VCS.
