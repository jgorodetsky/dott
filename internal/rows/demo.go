package rows

// Demo is a fixed set of rows for the picker until the scan engine exists.
// Picking one changes nothing; the actions only print what they would do.
func Demo() []Row {
	return []Row{
		{ID: "status-shell", Tier: RO, Command: "status shell", Desc: "is ~/.zshrc loading the repo", State: "ok", Section: "shell"},
		{ID: "fix-git-ignore", Tier: MUT, Command: "fix git ignore", Desc: "add the repo's lines to your global git ignore", State: "drift", Section: "git",
			Plan: []string{"append 2 lines to ~/.config/git/ignore: .lavish/ .playwright-mcp/"}, Shell: "printf '.lavish/\\n.playwright-mcp/\\n' >> ~/.config/git/ignore"},
		{ID: "turn-on-aws", Tier: MUT, Command: "turn on aws", Desc: "use AWS SSO on this Mac: awscli and a profile per account", State: "off", Section: "cloud",
			Plan: []string{"remember aws = on in ~/.config/dott/config.toml", "then offer: fix aws"}, Shell: "dott fix aws"},
		{ID: "fix-aws", Tier: MUT, Command: "fix aws", Desc: "run awssetup: SSO start URL, one browser login", State: "off", Section: "cloud",
			Plan: []string{"run bin/awssetup (asks a few questions, writes inside its block in ~/.aws/config)"}, Shell: "bin/awssetup"},
		{ID: "resolve-nvim", Tier: PICK, Command: "resolve nvim", Desc: "~/.config/nvim is your own config", State: "conflict", Section: "editor",
			Next: []Row{
				{ID: "nvim-keep", Tier: MUT, Command: "keep mine", Desc: "turn the nvim item off on this Mac", Plan: []string{"remember nvim = off in ~/.config/dott/config.toml"}, Shell: "true"},
				{ID: "nvim-compare", Tier: RO, Command: "compare", Desc: "open yours and the repo's side by side in nvim -d"},
				{ID: "nvim-replace", Tier: DGR, Command: "replace with the repo's", Desc: "back yours up, then link ~/.config/nvim to the repo", Plan: []string{"move ~/.config/nvim to ~/.local/state/dott/backups/<time>-nvim/", "link ~/.config/nvim -> <repo>/nvim"}, Shell: "mv ~/.config/nvim ~/.local/state/dott/backups/nvim && ln -s <repo>/nvim ~/.config/nvim"},
			}},
		{ID: "fix-all", Tier: MUT, Command: "fix everything", Desc: "every item that needs you, one plan, one confirm", Section: "all",
			Plan: []string{"fix git ignore"}, Shell: "dott --yes fix everything"},
		{ID: "refresh", Tier: MUT, Command: "refresh", Desc: "update: show what's new in the repo, then pull and fix", Section: "repo",
			Plan: []string{"git pull --ff-only (3 new commits)", "then fix the items they touch"}, Shell: "git -C <repo> pull --ff-only"},
		{ID: "upgrade", Tier: PICK, Command: "upgrade", Desc: "newer versions of brew packages, tick which", Section: "brew",
			Next: []Row{
				{ID: "upgrade-safe", Tier: MUT, Command: "upgrade the safe ones", Desc: "patch and minor versions only", Plan: []string{"brew upgrade jq ripgrep"}, Shell: "brew upgrade jq ripgrep"},
				{ID: "upgrade-choose", Tier: RO, Command: "choose", Desc: "pick packages one by one"},
			}},
		{ID: "alias-add", Tier: PICK, Command: "add alias <name>", Desc: "a new shell alias, every machine or this Mac only", Section: "shell"},
		{ID: "alias-remove", Tier: PICK, Command: "remove alias <name>", Desc: "delete an alias wherever it is defined", Section: "shell"},
		{ID: "edit-shell", Tier: RO, Command: "edit shell", Desc: "open shell/.zshrc, every machine", Section: "files"},
		{ID: "edit-local", Tier: RO, Command: "edit local", Desc: "open ~/.zshrc.local, this Mac only", Section: "files"},
		{ID: "skills", Tier: RO, Command: "skills", Desc: "every skill: where it lives, what it does, its state", Section: "agents"},
		{ID: "add-skill", Tier: PICK, Command: "add skill", Desc: "from npx skills, a GitHub URL, a local folder, or create a new one", Section: "agents",
			Next: []Row{
				{ID: "add-skill-npx", Tier: PICK, Command: "from npx skills", Desc: "search the skills.sh catalog"},
				{ID: "add-skill-github", Tier: PICK, Command: "from a GitHub URL", Desc: "shows SKILL.md and scripts before installing"},
				{ID: "add-skill-local", Tier: PICK, Command: "from a local folder", Desc: "link a folder that lives on this Mac only"},
				{ID: "add-skill-new", Tier: PICK, Command: "create a new one", Desc: "name, description, a spec-checked SKILL.md, then skill-creator"},
			}},
		{ID: "perms-claude", Tier: RO, Command: "permissions claude", Desc: "what Claude Code may run without asking", State: "drift", Section: "agents"},
		{ID: "perms-codex", Tier: RO, Command: "permissions codex", Desc: "what Codex may run without asking", State: "ok", Section: "agents"},
	}
}
