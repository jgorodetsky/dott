# dott

One fuzzy picker for your dotfiles: what is fine, what needs you, and the fixes.

dott reads a `dott.toml` from your dotfiles repo, checks each item on this Mac, and lists them in a picker built on fzf. Type any words to filter. A look never changes anything (dott keeps only its own pick history, in `~/.local/state/dott`); a fix shows its plan, asks once, backs up what it changes and checks the item again after.

```sh
brew install jgorodetsky/dott/dott
dott --init owner/dotfiles      # clones to ~/dotfiles (or give a folder), then remembers it
dott                            # the picker over every item
dott aws                        # the same, filtered; a single match, or a row's whole name, is picked (an item opens its view)
dott fix everything --yes       # without a terminal: run one change row, never a dangerous one
dott --json                     # every item's state as JSON; changes nothing
```

## Setup and updates

```
a new Mac                                   after that
─────────                                   ──────────
your dotfiles repo's bootstrap (curl …)     dott
  1  Homebrew, if it is missing               the header says when the repo's remote has
  2  brew install jgorodetsky/dott/dott       new commits: ↑ origin/main has new commits
  3  dott --init owner/dotfiles                 │
     clones to ~/dotfiles, remembers it         ▼
  4  the optional parts, asked once          refresh
        │                                      the new commits, the items they touch and
        ▼                                      the diff of every changed script
     fix everything                             │ Pull? [Y/n]
       the plan, one yes, a backup of           ▼
       what it changes, a check after        git pull --ff-only, then the fix plan
                                               and its own Run it? [Y/n/copy]
```

- The update check runs while dott scans. It asks the remote with `git ls-remote`, so it fetches nothing and writes nothing. It never asks for a password: git runs with no terminal and gives up after 5 seconds. Offline, there is no notice and no wait.
- dott itself updates with Homebrew: pick `upgrade`, or run `brew upgrade dott`.


## The picker

```
dott · ~/dotfiles (main) · 14 ok · 1 needs you · 3 off · 0 not here      ← the header: repo, branch, counts
✓ ok ~ drift ! conflict ✗ blocked · off │ ● runs ◆ asks ▲ type yes ▸ opens
dott ›
  ◆ fix shell-zshrc   drift  shell     the loader line in ~/.zshrc      ← what needs you, first
  ✓ brew              ok     packages  the packages in the Brewfile     ← every item; enter opens it
  · aws               off    cloud     aws sso: one login, a profile …
  ▸ optional parts                     turn on or off: aws, kube, macos ← the tools, folded
  ▸ refresh  ▸ upgrade  ▸ add  ▸ edit a file  ▸ permissions  ▸ turn off <item>
```

An item's mark is its state. A row that does something has one of four marks:

| Mark | Kind | What happens when you pick it |
|---|---|---|
| ● | read-only | runs at once |
| ◆ | change | shows exactly what it will do, then asks `Run it? [Y/n/copy]` (copy puts the shell commands on the clipboard) |
| ▲ | dangerous | asks you to type `yes`; used when a fix would replace something of yours |
| ▸ | opens | opens a list (an item's view, a folder, a folded list of tools), or asks a few questions first |

Enter on an item opens its view: where it lives on this Mac and in the repo (a file opens in your editor, a folder opens its list), its parts (each opens its own view), its fix, its `[[item]]` entry in dott.toml, and `turn off`. After an action you stay in that view and see the new state.

Esc goes back one screen and keeps what you had typed there; on the first screen it quits. ctrl-c quits from any screen. The rows you pick most often float to the top.

Each item has one of six states:

| State | Means | What you can do |
|---|---|---|
| ok | set up as the repo says | open it |
| drift | not set up, or the repo has something newer | ◆ `fix <item>`, or turn it off in its view |
| conflict | something of yours is in the way | ▸ `resolve <item>`: keep mine, replace (▲, with a backup), compare |
| blocked | it needs something else first | ● `fix <item>` says what |
| off | an optional group you said no to, or an item you turned off | turn it on in its view, or from `optional parts` |
| absent | the tool it is for isn't on this Mac | open it |

The other rows: `fix everything`, `optional parts` (turn each group on or off), `refresh` (fetch, then see the commits, the items they touch and the diff of every changed script; `Pull?` asks before the pull, and the fix plan after it has its own `Run it?`), `upgrade` (newer brew versions; you pick which), `add` (a file, a brew package, an alias or a skill; see below), `remove alias`, `edit a file`, `permissions` (what claude, codex, opencode and pi may do without asking, with risky rules flagged; read-only), `restore a backup`, `turn off <item>`, and rows of your own from the config. Words typed on the command line go on into a folded list: `dott edit shell` opens that file in your editor.

## Add things: every machine or this Mac only

`add` asks one question for each thing you add: every machine (the repo) or this Mac only.

| Add | Every machine | This Mac only |
|---|---|---|
| `add file <path>` | the file or folder moves into the repo (`~/.config/foo/x` → `foo/x`), a link takes its place, and a `link` item goes in dott.toml; then it offers a commit of just that | the same, in `~/.config/dott/files` and `~/.config/dott/local.toml` |
| `add brew <package>` | a line in the Brewfile, under the section you pick, then the install | a line in `~/.config/dott/files/Brewfile`, which a `brew-local` item installs |
| `add alias` | `shell/.zshrc` (or the repo file the manifest names) | `~/.zshrc.local` |
| `add skill` | the repo's skills folder | a folder of this Mac's own (`[local] skills`) |

- A file you added has `stop tracking` in its view: a real copy takes the link's place and its item goes. The copy in the repo (or this Mac's files) stays.
- Each add backs up what it changes first, and puts everything back when a step fails.
- The brew item's view lists the packages you installed yourself that no Brewfile lists; pick one to add it.

A file that goes in the repo may end up public, so `add file` checks it first:

- A file that holds secrets by its nature can only be tracked for this Mac: `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube/config`, `~/.netrc`, `~/.npmrc`, `~/.config/gh/hosts.yml`, keychains, `.env` files, keys and certificates (`*.pem`, `*.key`, `id_*`).
- A line that looks like a secret (a private key, an AWS key id, a GitHub or Slack token, `password = …`, a long random string) needs a typed `yes`. dott shows the line number, never the value.
- The plan shows where the repo pushes to.

This is a guard, not a full scanner: read the diff before you push.

## What it never does

- It never runs `sudo`. When a step needs it (Xcode's command line tools), dott prints the command for you.
- A look never runs `npx`, `uvx`, `brew trust`, `brew install`, `xcode-select --install` or `git fetch`. `refresh` does fetch, before it asks: that writes only git's copy of the remote branch (`origin/main`), never your files or your branch. It runs git only when Xcode's command line tools are installed, because `/usr/bin/git` without them opens an install dialog. Apart from the rows you pick, its one network call is the update check's `git ls-remote`, in a terminal session.
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
| `script` | anything else, through a script: `check` exits 0 ok, 1 drift, 2 conflict, 3 blocked, 4 absent, 5 off (not turned on yet) |
| `skills` | agent skills installed with the installer you name, one shared copy and a link for claude |
| `rules` | one rules file for every agent tool: a link, or an `@path` import line in a marked block |

Common fields: `section`, `desc`, `group` (an optional group from `[groups]`), `when` (`cmd`, `app`, `dir`, `file`, `os`, `any`), `needs` (item ids, or `brew:NAME`), `verify` and `after` (commands). Paths take `~`, `${VAR:-default}`, `{{repo}}` and named paths (`@name`, from `[paths.name]`). Commands are argv arrays.

[jgorodetsky/dotfiles](https://github.com/jgorodetsky/dotfiles/blob/main/dott.toml) is a full example.

### Script items: parts and actions

A script's `check` prints what it found on its first line and the next step on its second. Each line after those is a part: `name<TAB>state<TAB>what it is`, with the state `ok`, `off` or `absent`. Each part gets a row in the item's view and a view of its own: `dott <item> <part>` opens it. Exit 5 means off: nothing needs you, and `fix everything` skips it.

`[[item.action]]` adds rows of the script's own:

```toml
[[item.action]]
name = "turn on {{part}}"
part = true          # a row in each part's view; {{part}} is the part's name
states = ["off"]     # only for parts in these states
tier = "change"      # look: runs at once; change: Run it?; danger: a typed yes; ask: runs in the terminal
run = ["bash", "{{repo}}/rules.sh", "on", "{{part}}"]
plan = ["bash", "{{repo}}/rules.sh", "plan-on", "{{part}}"]   # optional: prints the plan, a line per change
```

dott holds its lock while an action runs, and checks the item again after it. An `ask` action asks its own questions. ctrl-c stops it and comes back to dott, and an exit of 130 says you stopped it. Scripts get `DOTT`, the path of the dott that runs them, so a script can ask with dott's own picker:

```sh
rule="$(printf '%s\n' pr issue jira | "$DOTT" --pick --prompt "which rule")" || exit 130
name="$("$DOTT" --pick --input --prompt "its name" --preview 'echo saved as {q}')" || exit 130
```

`--pick` prints the picked line. With `--multi`, tab picks more than one. `--input` is a text box instead: it prints what you typed, and its `--preview` command gets the text as `{q}`, quoted by fzf, so don't put quotes around it. Esc exits 1 and ctrl-c 130.

## This Mac's settings

`~/.config/dott/config.toml` holds what is true only on this Mac, and never goes in a repo: the repo path, your answers for the optional groups, items you turned off, local skill folders, and rows of your own. Next to it, `local.toml` holds this Mac's own items (`[[item]]` and `[[edit]]`, the same format as dott.toml), and `files/` the files they link to (`{{local}}` in a path; `[local] dir` moves it). Paths in local.toml start with `~`, `/` or `{{local}}`.

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

Tests run on a fake HOME with stub `brew`, `npx` and `uvx` (`internal/testkit`). One test hashes the whole fake HOME before and after a look; another runs `fix everything` on a fresh one, then again to show there is nothing left. Journey tests run dott in a pseudo-terminal, type keys and compare the screen a person would see with `cmd/dott/testdata/screens/*.txt` (`internal/journey`); `go test ./cmd/dott -run TestJourney -update` rewrites them after a change you meant. `DOTT_CONTRACT_REPO=<dotfiles folder> go test -run TestContract ./cmd/dott` checks a real manifest.

`scripts/release.sh <version>` tags, makes the GitHub release and updates the formula in [homebrew-dott](https://github.com/jgorodetsky/homebrew-dott).

## License

MIT. dott includes [fzf](https://github.com/junegunn/fzf) (MIT); see THIRD_PARTY_NOTICES.md.
