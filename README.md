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
| `j` / `↓`          | Move down — file selection (list focus) or **diff line cursor** (diff focus) |
| `k` / `↑`          | Move up — file selection or diff line cursor             |
| `Enter`            | Focus the diff pane for the selected file (j/k then move by line) |
| `Esc`              | Return focus to the file list / close an overlay         |
| `}` / `{`          | Jump to the next / previous hunk in the diff             |
| `s`                | Stage or unstage the selected file                       |
| `u`                | Discard the **hunk under the cursor** (unstaged: reverse-apply; staged: unstage the hunk) |
| `U`                | Discard the **whole file** (always confirms; `git restore` / `git clean` for untracked) |
| `Ctrl+R`           | Recover the most recently discarded change (LIFO undo stack) |
| `>` / `<`          | Grow / shrink the diff pane (resize the list ↔ diff split) |
| `r`                | Refresh Git status (force an immediate reload)           |
| `Ctrl+T`           | Toggle background auto-refresh on/off                    |
| `<leader> b`       | Open the branch panel                                    |
| `?`                | Toggle the help / keymap overlay                         |
| `q`                | Quit (warns if a git operation is in progress)           |
| `Ctrl+C`           | Quit immediately                                         |

`u` discards only the hunk the diff cursor sits in, leaving the file's other
changes intact; on an untracked file it falls back to the whole-file path. `U`
always shows a confirmation naming the file. `Ctrl+R` re-applies the last
discarded change — the undo stack is in-memory (capped at 50, lost on quit). The
file list / diff split is resizable with `>` / `<` and clamps to minimum pane
widths so nothing clips in narrow terminals.

### Auto-refresh

The interface reloads itself **near real time** — changes made on disk, by
external `git` commands, or by branch switches show up without pressing `r`. A
background poll runs every ~750 ms and re-reads `git status` off the UI thread;
the next poll is chained off the previous one's completion, so a slow status
never overlaps or queues itself.

The refresh is non-disruptive:

- It only re-renders when the status **actually changed** (a cheap fingerprint of
  branch / upstream / ahead-behind / each file's path+code is compared) — an
  idle repo causes no redraw and no diff re-fetch.
- The **selected file stays selected by path**; if it disappears the selection
  falls to a sensible neighbor.
- The **diff line cursor and scroll position are preserved** while the selected
  file's diff is unchanged, and reset only when that file's content actually
  changed.
- Active focus (list vs. diff) is left alone, and an open overlay (branch / help /
  confirm) or an in-progress text prompt is **never** closed, refocused, or
  interrupted — the data refreshes underneath and the view reconciles once the
  overlay closes.
- A background status failure is surfaced as a toast **at most once** (not on
  every tick).

Auto-refresh is **on by default**. Press **`Ctrl+T`** to toggle it off (useful on
very large repos) and on again; the footer shows `auto:on` / `auto:off`. Manual
`r` always forces an immediate reload regardless of the toggle.

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
