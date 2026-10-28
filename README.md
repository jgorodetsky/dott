# dott

One fuzzy picker for your dotfiles: what is fine, what needs you, and the fixes.

dott reads a `dott.toml` from your dotfiles repo, checks each item on this Mac, and lists them in a picker built on fzf. Type any words to filter. A look never changes anything; a fix shows its plan, asks once, backs up what it changes and checks the item again after.

```sh
brew install jgorodetsky/dott/dott
dott --init owner/dotfiles      # clones to ~/dotfiles (or give a folder), then remembers it
dott                            # the picker over every item
dott aws                        # the same, filtered; a single match is picked at once
dott fix everything --yes       # without a terminal: run one change row, never a dangerous one
dott --json                     # every item's state as JSON; changes nothing
```

On a new Mac, the dotfiles repo's one-line bootstrap installs Homebrew, then dott, then runs `dott --init`.

## The picker

| Mark | Kind | What happens when you pick it |
|---|---|---|
| ● | read-only | runs at once |
| ◆ | change | shows exactly what it will do, then asks `Run it? [Y/n/copy]` (copy puts the shell commands on the clipboard) |
| ▲ | dangerous | asks you to type `yes`; used when a fix would replace something of yours |
| ▸ | guided | opens a follow-up picker, or asks a few questions first |

After an action, dott checks again and goes back to the picker. Esc or ctrl-c quits. The rows you pick most often float to the top.

Each item has one of six states:

| State | Means | Row |
|---|---|---|
| ok | set up as the repo says | ● the item, with its details |
| drift | not set up, or the repo has something newer | ◆ `fix <item>` and ◆ `turn off <item>` |
| conflict | something of yours is in the way | ▸ `resolve <item>`: keep mine, replace (▲, with a backup), compare |
| blocked | it needs something else first | ● `fix <item>` says what |
| off | an optional group you said no to, or an item you turned off | ◆ `turn on ...` |
| absent | the tool it is for isn't on this Mac | ● the item |

The other rows: `fix everything`, `summary`, `refresh` (pull the repo, see the commits, the items they touch and the diff of every changed script, then pull and fix after one yes), `upgrade` (newer brew versions; you pick which), `add alias`, `remove alias`, `edit <file>`, `permissions <tool>` (what claude, codex, opencode and pi may do without asking, with risky rules flagged; read-only), `add skill`, `restore a backup`, `turn off <item>`, and rows of your own from the config.

## What it never does

- It never runs `sudo`. When a step needs it (Xcode's command line tools), dott prints the command for you.
- A look never runs `npx`, `uvx`, `brew trust`, `brew install`, `xcode-select --install` or `git fetch`. It runs git only when Xcode's command line tools are installed, because `/usr/bin/git` without them opens an install dialog.
- It never replaces a file or folder of yours without a typed `yes`, and it backs it up first to `~/.local/state/dott/backups`. `restore a backup` puts it back.
- It never touches a path the manifest protects (`protect = [...]`).
- It never commits for you without asking. `add alias` can commit just the alias line, even when the file has other edits, and never pushes.

## The manifest

`dott.toml` sits at the root of the dotfiles repo. Each `[[item]]` has an `id`, a `kind` and the fields of that kind. Unknown keys are errors.

| Kind | Sets up |
|---|---|
| `link` | a symlink into the repo (`link`, `to`) |
| `block` | a marked block in a file of yours (`file`, `start`, `end`, `body`); `mode = "seed"` adds it once, and `skip_if` leaves it out when you have your own line |
| `include-line` | an include in a git config (`format = "gitconfig"`), or lines from a repo file (`lines_from`) |
| `copy` | a repo file or folder copied to this Mac; a copy you changed is yours |
| `brew-bundle` | the packages of a Brewfile; `package_groups` makes some optional |
| `script` | anything else, through a script: `check` exits 0 ok, 1 drift, 2 conflict, 3 blocked, 4 absent |
| `skills` | agent skills installed with the installer you name, one shared copy and a link for claude |
| `rules` | one rules file for every agent tool: a link, or an `@path` import line in a marked block |

Common fields: `section`, `desc`, `group` (an optional group from `[groups]`), `when` (`cmd`, `app`, `dir`, `file`, `os`, `any`), `needs` (item ids, or `brew:NAME`), `verify` and `after` (commands). Paths take `~`, `${VAR:-default}`, `{{repo}}` and named paths (`@name`, from `[paths.name]`). Commands are argv arrays.

[jgorodetsky/dotfiles](https://github.com/jgorodetsky/dotfiles/blob/main/dott.toml) is a full example.

## This Mac's settings

`~/.config/dott/config.toml` holds what is true only on this Mac, and never goes in a repo: the repo path, your answers for the optional groups, items you turned off, local skill folders, and rows of your own.

```toml
repo = "~/dotfiles"
off = ["nvim"]

[groups]
aws = false

[local]
skills = ["~/my-skills"]

[[row]]
name = "vpn"
desc = "connect the work VPN"
run = ["open", "-a", "Tunnelblick"]
```

`DOTT_REPO` and `DOTT_CONFIG` override the repo and the settings file.

## Develop

```sh
git config core.hooksPath githooks   # once per clone: checks run before every push
scripts/check.sh                     # gofmt, go vet, go test -race, staticcheck, govulncheck
go build ./cmd/dott
```

Tests run on a fake HOME with stub `brew`, `npx` and `uvx` (`internal/testkit`). One test hashes the whole fake HOME before and after a look; another runs `fix everything` on a fresh one, then again to show there is nothing left. `DOTT_CONTRACT_REPO=<dotfiles folder> go test -run TestContract ./cmd/dott` checks a real manifest.

`scripts/release.sh <version>` tags, makes the GitHub release and updates the formula in [homebrew-dott](https://github.com/jgorodetsky/homebrew-dott).

## License

MIT. dott includes [fzf](https://github.com/junegunn/fzf) (MIT); see THIRD_PARTY_NOTICES.md.
