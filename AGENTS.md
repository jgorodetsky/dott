# dott

dott is a Go command-line tool: one fuzzy picker over a dotfiles setup. It reads a `dott.toml` manifest from a dotfiles repo, shows the state of each item and fixes items after one confirm.

## Rules

- Tests never touch the real HOME. `TestMain` sets `HOME` and the `XDG_*` folders to a temp folder. Use `t.TempDir()` for every file that a test writes.
- Run `scripts/check.sh` before you push. The pre-push hook runs it. Turn the hook on once per clone: `git config core.hooksPath githooks`.
- A look (the scan, the picker, `--json`) changes nothing on disk. The only writes are dott's own: the pick log and the search history, after the user picks a row, and the answers to the optional-group questions. `TestLookIsReadOnly` checks it.
- Run commands as argv arrays. Never pass a string built from user input to a shell.
- dott never runs `sudo`.
- Only `internal/picker/fzflib.go` imports fzf.
- Pin each tool version: `go run tool@version` in scripts, a commit SHA for each GitHub Action.
- Never put a private path or a private name in this repo. Things that exist only on one Mac go in that Mac's `~/.config/dott`.
- Every fix goes through `engine.Apply`: lock, backup, apply, check again. A change that replaces something of the user's sets `Danger`, so the user types yes.
