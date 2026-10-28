// Package perms reads what each agent tool may do without asking you. It only reads, and
// it reads only the permission keys: never env blocks, tokens or other settings.
package perms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tailscale/hujson"
)

// Rule is one thing a tool may do, ask about or refuse.
type Rule struct {
	Where  string // the file it is in, short
	What   string // the rule as written
	Action string // allow, ask, deny, trust, mode
	Risky  string // why it is risky, empty when it isn't
}

// Report is one tool's rules.
type Report struct {
	Tool  string
	Note  string // a fact about the tool's defaults
	Rules []Rule
	Err   string
}

// Short writes paths under home as ~/...
type Short func(string) string

// Tools are the readers, by tool name.
var Tools = []string{"claude", "codex", "opencode", "pi"}

// Read reads one tool's permissions from home.
func Read(tool, home string, short Short) Report {
	switch tool {
	case "claude":
		return claude(home, short)
	case "codex":
		return codex(home, short)
	case "opencode":
		return opencode(home, short)
	case "pi":
		return Report{Tool: "pi", Note: "pi has no approval step: it runs every tool call with your account's rights (pi docs/security.md)",
			Rules: []Rule{{Where: "built in", What: "every tool call", Action: "allow", Risky: "no approval at all"}}}
	}
	return Report{Tool: tool, Err: "dott has no reader for " + tool}
}

// claudeRisk flags allow rules that cover any command, any site or any file.
func claudeRisk(rule string) string {
	switch {
	case rule == "Bash" || rule == "Bash(*)" || rule == "Bash(:*)":
		return "any shell command, without asking"
	case strings.HasPrefix(rule, "Bash(") && (strings.Contains(rule, "rm ") || strings.Contains(rule, "rm:") || strings.Contains(rule, "sudo") || strings.Contains(rule, "delete")):
		return "a command that deletes or needs root"
	case strings.HasPrefix(rule, "Bash(") && strings.HasSuffix(rule, " *)") && strings.Count(rule, " ") <= 2:
		return "a broad wildcard"
	case rule == "WebFetch":
		return "any website"
	case rule == "Write" || rule == "Edit":
		return "any file, without asking"
	}
	return ""
}

func claude(home string, short Short) Report {
	r := Report{Tool: "claude", Note: "an allow rule runs without asking; a deny rule wins over an allow rule"}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		p := filepath.Join(home, ".claude", name)
		var s struct {
			Permissions struct {
				Allow       []string `json:"allow"`
				Ask         []string `json:"ask"`
				Deny        []string `json:"deny"`
				DefaultMode string   `json:"defaultMode"`
			} `json:"permissions"`
			Skip bool `json:"skipDangerousModePermissionPrompt"`
		}
		if err := readJSON(p, &s); err != nil {
			if !os.IsNotExist(err) {
				r.Err = err.Error()
			}
			continue
		}
		w := short(p)
		for _, a := range s.Permissions.Allow {
			r.Rules = append(r.Rules, Rule{w, a, "allow", claudeRisk(a)})
		}
		for _, a := range s.Permissions.Ask {
			r.Rules = append(r.Rules, Rule{w, a, "ask", ""})
		}
		for _, a := range s.Permissions.Deny {
			r.Rules = append(r.Rules, Rule{w, a, "deny", ""})
		}
		if m := s.Permissions.DefaultMode; m != "" {
			risk := ""
			if m == "bypassPermissions" {
				risk = "skips every permission check"
			}
			r.Rules = append(r.Rules, Rule{w, "defaultMode = " + m, "mode", risk})
		}
		if s.Skip {
			r.Rules = append(r.Rules, Rule{w, "skipDangerousModePermissionPrompt", "mode", "no warning before bypass mode"})
		}
	}
	p := filepath.Join(home, ".claude.json")
	var cj struct {
		Projects map[string]struct {
			AllowedTools []string `json:"allowedTools"`
		} `json:"projects"`
	}
	if err := readJSON(p, &cj); err == nil {
		keys := make([]string, 0, len(cj.Projects))
		for k := range cj.Projects {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, a := range cj.Projects[k].AllowedTools {
				r.Rules = append(r.Rules, Rule{short(p) + " (" + short(k) + ")", a, "allow", claudeRisk(a)})
			}
		}
	}
	return r
}

var prefixRule = regexp.MustCompile(`^\s*prefix_rule\((.*)\)\s*$`)
var ruleArg = regexp.MustCompile(`(pattern|decision)\s*=\s*(\[[^\]]*\]|"[^"]*")`)

func codex(home string, short Short) Report {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".codex")
	}
	r := Report{Tool: "codex", Note: "trusted folders and allow rules let codex run commands without asking"}
	p := filepath.Join(dir, "config.toml")
	var cfg struct {
		Approval string `toml:"approval_policy"`
		Sandbox  string `toml:"sandbox_mode"`
		Projects map[string]struct {
			Trust string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if _, err := toml.DecodeFile(p, &cfg); err != nil {
		if !os.IsNotExist(err) {
			r.Err = short(p) + ": " + err.Error()
		}
	} else {
		w := short(p)
		if cfg.Approval != "" {
			risk := ""
			if cfg.Approval == "never" {
				risk = "never asks"
			}
			r.Rules = append(r.Rules, Rule{w, "approval_policy = " + cfg.Approval, "mode", risk})
		}
		if cfg.Sandbox != "" {
			risk := ""
			if cfg.Sandbox == "danger-full-access" {
				risk = "no sandbox"
			}
			r.Rules = append(r.Rules, Rule{w, "sandbox_mode = " + cfg.Sandbox, "mode", risk})
		}
		keys := make([]string, 0, len(cfg.Projects))
		for k := range cfg.Projects {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if t := cfg.Projects[k].Trust; t != "" {
				risk := ""
				if t == "trusted" && (filepath.Clean(k) == filepath.Clean(home) || k == "/") {
					risk = "your whole home folder is trusted"
				}
				r.Rules = append(r.Rules, Rule{w, short(k), t, risk})
			}
		}
	}
	rp := filepath.Join(dir, "rules", "default.rules")
	if b, err := os.ReadFile(rp); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			m := prefixRule.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			var pattern []string
			decision := "allow"
			for _, a := range ruleArg.FindAllStringSubmatch(m[1], -1) {
				if a[1] == "decision" {
					decision = strings.Trim(a[2], `"`)
				} else {
					_ = json.Unmarshal([]byte(a[2]), &pattern)
				}
			}
			risk := ""
			if decision == "allow" && len(pattern) == 1 {
				risk = "every " + pattern[0] + " command"
			}
			r.Rules = append(r.Rules, Rule{short(rp), strings.Join(pattern, " "), decision, risk})
		}
	}
	return r
}

func opencode(home string, short Short) Report {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home, ".config")
	}
	r := Report{Tool: "opencode", Note: "with no permission key, most actions are allowed without asking; doom_loop and external_directory ask (opencode.ai/docs/permissions)"}
	var p string
	var raw []byte
	for _, name := range []string{"opencode.jsonc", "opencode.json", "config.json"} {
		p = filepath.Join(dir, "opencode", name)
		if b, err := os.ReadFile(p); err == nil {
			raw = b
			break
		}
	}
	if raw == nil {
		r.Rules = append(r.Rules, Rule{"built in", "edit, bash, webfetch", "allow", "the defaults: no approval"})
		return r
	}
	std, err := hujson.Standardize(raw)
	if err != nil {
		r.Err = short(p) + ": " + err.Error()
		return r
	}
	var cfg struct {
		Permission json.RawMessage `json:"permission"`
	}
	if err := json.Unmarshal(std, &cfg); err != nil {
		r.Err = short(p) + ": " + err.Error()
		return r
	}
	w := short(p)
	if len(cfg.Permission) == 0 {
		r.Rules = append(r.Rules, Rule{w + " (no permission key)", "edit, bash, webfetch", "allow", "the defaults: no approval"})
		return r
	}
	var all string
	if json.Unmarshal(cfg.Permission, &all) == nil {
		risk := ""
		if all == "allow" {
			risk = "every action, without asking"
		}
		return Report{Tool: r.Tool, Note: r.Note, Rules: []Rule{{w, "permission = " + all, all, risk}}}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(cfg.Permission, &m); err != nil {
		r.Err = w + ": permission: " + err.Error()
		return r
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var s string
		if json.Unmarshal(m[k], &s) == nil {
			risk := ""
			if s == "allow" && (k == "bash" || k == "edit" || k == "webfetch") {
				risk = "any " + k + " action"
			}
			r.Rules = append(r.Rules, Rule{w, k, s, risk})
			continue
		}
		var pats map[string]string
		if json.Unmarshal(m[k], &pats) == nil {
			ps := make([]string, 0, len(pats))
			for p := range pats {
				ps = append(ps, p)
			}
			sort.Strings(ps)
			for _, p := range ps {
				risk := ""
				if pats[p] == "allow" && (p == "*" || p == "**") {
					risk = "any " + k + " action"
				}
				r.Rules = append(r.Rules, Rule{w, k + " " + p, pats[p], risk})
			}
		}
	}
	for _, k := range []string{"bash", "edit", "webfetch"} {
		if _, ok := m[k]; !ok {
			r.Rules = append(r.Rules, Rule{w + " (not set)", k, "allow", "the default: no approval"})
		}
	}
	return r
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}
