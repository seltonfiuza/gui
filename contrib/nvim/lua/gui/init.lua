-- gui.nvim — open the `gui` git TUI in a floating terminal from Neovim.
--
-- The plugin does not embed the TUI; it launches the standalone `gui` binary in
-- a terminal buffer (the same pattern lazygit.nvim / toggleterm use). Build the
-- binary onto your PATH first:  go build -o ~/.local/bin/gui .
local M = {}

M.config = {
  cmd = "gui", -- binary name or absolute path
  width = 0.9, -- fraction of columns when <= 1, else absolute columns
  height = 0.9, -- fraction of lines when <= 1, else absolute lines
  border = "rounded", -- any nvim_open_win border style
  cwd = nil, -- nil/"" -> getcwd(); "file" -> current file's dir; or a path
  refresh = true, -- on close: :checktime (+ Gitsigns refresh if present)
  on_exit = nil, -- optional extra callback(exit_code)
}

local function dimension(frac, total)
  if frac == nil then
    return total
  end
  if frac <= 1 then
    return math.max(1, math.floor(total * frac))
  end
  return math.min(math.floor(frac), total)
end

local function resolve_cwd(cwd)
  if cwd == "file" then
    local dir = vim.fn.expand("%:p:h")
    if dir ~= "" and vim.fn.isdirectory(dir) == 1 then
      return dir
    end
  elseif type(cwd) == "string" and cwd ~= "" then
    return cwd
  end
  return vim.fn.getcwd()
end

-- open launches the TUI in a centered floating terminal. opts override M.config
-- for this invocation only.
function M.open(opts)
  opts = vim.tbl_deep_extend("force", {}, M.config, opts or {})

  if vim.fn.executable(opts.cmd) == 0 then
    vim.notify(
      ("gui.nvim: '%s' not found on PATH — build it with `go build -o ~/.local/bin/gui .`"):format(opts.cmd),
      vim.log.levels.ERROR
    )
    return
  end

  local width = dimension(opts.width, vim.o.columns)
  local height = dimension(opts.height, vim.o.lines)
  local buf = vim.api.nvim_create_buf(false, true)
  local win = vim.api.nvim_open_win(buf, true, {
    relative = "editor",
    width = width,
    height = height,
    row = math.floor((vim.o.lines - height) / 2),
    col = math.floor((vim.o.columns - width) / 2),
    style = "minimal",
    border = opts.border,
  })

  vim.fn.termopen({ opts.cmd }, {
    cwd = resolve_cwd(opts.cwd),
    on_exit = function(_, code, _)
      if vim.api.nvim_win_is_valid(win) then
        vim.api.nvim_win_close(win, true)
      end
      if vim.api.nvim_buf_is_valid(buf) then
        pcall(vim.api.nvim_buf_delete, buf, { force = true })
      end
      if opts.refresh then
        pcall(vim.cmd, "checktime")
        pcall(vim.cmd, "silent! Gitsigns refresh")
      end
      if type(opts.on_exit) == "function" then
        opts.on_exit(code)
      end
    end,
  })
  vim.cmd("startinsert") -- enter terminal mode so keys/mouse reach the TUI
end

-- setup merges user options and optionally binds a normal-mode keymap.
function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", M.config, opts or {})
  if M.config.keymap then
    vim.keymap.set("n", M.config.keymap, function()
      M.open()
    end, { desc = "Open the gui git TUI" })
  end
end

return M
