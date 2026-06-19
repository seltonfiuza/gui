-- Defines the :Gui command. Sourced automatically once this directory is on the
-- runtimepath (see contrib/nvim/README.md).
if vim.g.loaded_gui then
  return
end
vim.g.loaded_gui = true

vim.api.nvim_create_user_command("Gui", function(o)
  require("gui").open({ cwd = (o.args ~= "" and o.args) or nil })
end, {
  nargs = "?",
  complete = "dir",
  desc = "Open the gui git TUI (optionally in the given directory)",
})
