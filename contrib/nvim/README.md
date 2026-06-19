# gui.nvim

Open the `gui` git TUI in a floating terminal from Neovim, via `:Gui`.

This is a thin launcher — it runs the standalone `gui` binary in a terminal
buffer (the same approach as `lazygit.nvim` / `toggleterm.nvim`), so the full
TUI (mouse, folder tree, diffs) works unchanged inside Neovim.

## Prerequisites

Build the binary onto your `PATH`:

```sh
go build -o ~/.local/bin/gui .   # from the repo root
```

(or `go install .` if your `GOBIN` is on `PATH`).

## Install

### lazy.nvim

The plugin lives in the `contrib/nvim` subdirectory, so point lazy at it and add
that dir to the runtimepath:

```lua
{
  "seltonfiuza/gui",
  build = "go build -o ~/.local/bin/gui .",
  config = function()
    vim.opt.runtimepath:append(vim.fn.stdpath("data") .. "/lazy/gui/contrib/nvim")
    require("gui").setup({ keymap = "<leader>gg" })
  end,
}
```

### Manual (any plugin manager, or none)

```lua
vim.opt.runtimepath:append("/path/to/gui/contrib/nvim")
require("gui").setup({ keymap = "<leader>gg" })
```

## Usage

- `:Gui` — open the TUI in the current working directory.
- `:Gui /path/to/repo` — open it in a specific directory.
- `<leader>gg` — if you set `keymap` in `setup()`.

Quit the TUI with `q` (or `Ctrl+C`); the float closes and your buffers are
refreshed (`:checktime`, plus `Gitsigns refresh` when gitsigns is installed).

## Configuration

`setup()` is optional. Defaults shown:

```lua
require("gui").setup({
  cmd = "gui",        -- binary name or absolute path
  width = 0.9,        -- fraction of columns (<=1) or absolute width
  height = 0.9,       -- fraction of lines (<=1) or absolute height
  border = "rounded", -- nvim_open_win border style
  cwd = nil,          -- nil -> getcwd(); "file" -> current file's dir; or a path
  refresh = true,     -- on close: :checktime (+ Gitsigns refresh)
  keymap = nil,       -- e.g. "<leader>gg" to bind a normal-mode opener
  on_exit = nil,      -- optional function(exit_code)
})
```

`require("gui").open(opts)` accepts the same keys to override per call.

## Notes

- Needs `set mouse=a` for hover/click/scroll to reach the TUI (the default in
  most configs).
- In the floating terminal, `q` quits the app; `<C-\><C-n>` leaves terminal mode
  without quitting.
