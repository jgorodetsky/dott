package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/gitx"
	"github.com/jgorodetsky/dott/internal/ui"
)

// aliasName can't start with - or ., which zsh would read as an option.
var aliasName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// oneField makes text one line with no tabs, for one field of notes.tsv.
func oneField(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func zsh(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zsh", args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	return string(out), err
}

// AliasLine is the line that defines an alias, with a ' inside written as '\”.
func AliasLine(name, cmd string) string {
	return "alias " + name + "='" + strings.ReplaceAll(cmd, "'", `'\''`) + "'"
}

// InsertAlias puts the alias line in a file's text. An alias with that name is replaced
// where it is. In the repo file, aliases go in one block under header, above the first
// line matching before; in a local file the line (and its note) goes at the end.
func InsertAlias(text []byte, name, line, header string, before *regexp.Regexp, repo bool, note string) []byte {
	ls := splitLines(text)
	for i, l := range ls {
		if strings.HasPrefix(l, "alias "+name+"=") {
			ls[i] = line
			return joinLines(ls)
		}
	}
	if !repo {
		if note != "" {
			ls = append(ls, "# "+name+" - "+note)
		}
		return joinLines(append(ls, line))
	}
	var out []string
	inBlock, done := false, false
	for _, l := range ls {
		if !done && l == header {
			out = append(out, l)
			inBlock = true
			continue
		}
		if inBlock && !strings.HasPrefix(l, "alias ") {
			out = append(out, line)
			inBlock, done = false, true
		}
		if !done && before != nil && before.MatchString(l) {
			out = append(out, header, line, "")
			done = true
		}
		out = append(out, l)
	}
	if inBlock {
		out = append(out, line)
	} else if !done {
		out = append(out, "", header, line)
	}
	return joinLines(out)
}

// changeLines are the plan lines for one file: each line the change removes, then each it
// adds. A file that doesn't change has none.
func changeLines(short string, old, new []byte) []string {
	// missing are the lines of a that b doesn't have, as many times as b lacks them
	missing := func(a, b []string) []string {
		have := map[string]int{}
		for _, l := range b {
			have[l]++
		}
		var out []string
		for _, l := range a {
			if have[l] > 0 {
				have[l]--
				continue
			}
			out = append(out, l)
		}
		return out
	}
	o, n := splitLines(old), splitLines(new)
	var out []string
	for _, l := range missing(o, n) {
		out = append(out, short+": remove "+l)
	}
	for _, l := range missing(n, o) {
		out = append(out, short+": add "+l)
	}
	return out
}

// RemoveAlias drops the alias line, and a "# name - ..." note right above it.
func RemoveAlias(text []byte, name string) []byte {
	re := regexp.MustCompile(`^\s*alias (-- )?` + regexp.QuoteMeta(name) + `=`)
	ls := splitLines(text)
	drop := map[int]bool{}
	for i, l := range ls {
		if re.MatchString(l) {
			drop[i] = true
			if i > 0 && strings.HasPrefix(ls[i-1], "# "+name+" - ") {
				drop[i-1] = true
			}
		}
	}
	var out []string
	for i, l := range ls {
		if !drop[i] {
			out = append(out, l)
		}
	}
	return joinLines(out)
}

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func joinLines(ls []string) []byte {
	if len(ls) == 0 {
		return nil
	}
	return []byte(strings.Join(ls, "\n") + "\n")
}

// dropLines removes the lines matching re.
func dropLines(b []byte, re *regexp.Regexp) []byte {
	var out []string
	for _, l := range splitLines(b) {
		if !re.MatchString(l) {
			out = append(out, l)
		}
	}
	return joinLines(out)
}

// writeChecked writes the files with a backup first, then checks each with zsh -n and puts
// all of them back when one doesn't parse.
func (a *App) writeChecked(id string, files map[string][]byte, check []string) error {
	b := fsops.NewBackup(a.C.St.Backups(), id)
	for p := range files {
		if err := b.Save(p); err != nil {
			return err
		}
	}
	if err := b.Close(); err != nil {
		return err
	}
	for p, data := range files {
		if err := fsops.WriteFile(p, data, 0o644); err != nil {
			_ = fsops.Restore(b.Dir)
			return err
		}
	}
	for _, p := range check {
		if _, err := zsh(10*time.Second, "-n", p); err != nil {
			_ = fsops.Restore(b.Dir)
			return fmt.Errorf("%s wouldn't parse with that change; put back as it was", a.Env.Short(p))
		}
	}
	return nil
}

func (a *App) addAlias() error {
	al := a.M.Aliases
	if !a.Env.Has("zsh") {
		return fmt.Errorf("zsh isn't here")
	}
	name, ok := a.T.Ask("alias name", "")
	if !ok {
		return nil
	}
	if !aliasName.MatchString(name) {
		return fmt.Errorf("an alias name is letters, numbers, - _ . only, and starts with a letter, number or _")
	}
	cmd, ok := a.T.Ask("what should "+name+" run", "")
	if !ok {
		return nil
	}
	if _, err := zsh(5*time.Second, "-n", "-c", cmd); err != nil {
		return fmt.Errorf("that command doesn't parse on its own (a quote left open?)")
	}
	line := AliasLine(name, cmd)
	a.say("%s %s", amber("◆"), bold(line))
	all, _ := zsh(15*time.Second, "-i", "-c", `for k v in ${(kv)aliases}; do print -r -- "$k	$v"; done`)
	var twins []string
	for _, l := range strings.Split(all, "\n") {
		k, v, ok := strings.Cut(l, "\t")
		switch {
		case !ok:
		case k == name:
			a.say("  %s %s already runs: %s (this replaces it)", amber("~"), name, v)
		case v == cmd:
			twins = append(twins, k)
		}
	}
	if len(twins) > 0 {
		a.say("  %s that command already has a name: %s (you can use that instead)", amber("~"), strings.Join(twins, " "))
	}
	if p, err := exec.LookPath(name); err == nil {
		a.say("  %s %s is also a program (%s); the alias hides it", amber("~"), name, p)
	}
	repoFile, localFile := a.C.Path(al.RepoFile), a.C.Path(al.LocalFile)
	where, ok := a.T.Choose("Where should it go?", []string{
		"every machine: " + al.RepoFile + " in the repo, then you can commit it",
		"this Mac only: " + a.Env.Short(localFile) + ", never committed",
	})
	if !ok {
		return nil
	}
	repo := where == 0
	note, _ := a.T.Ask("what does it do? one line for the notes (enter: skip)", "")
	note = oneField(note)
	before := regexp.MustCompile(al.Before)
	file := localFile
	if repo {
		file = repoFile
	}
	edit := func(b []byte) ([]byte, error) { return InsertAlias(b, name, line, al.Header, before, repo, note), nil }
	notesRe := regexp.MustCompile("^" + regexp.QuoteMeta(name) + "\t")
	notesPath := a.C.Path(al.Notes)
	notesEdit := func(b []byte) ([]byte, error) {
		for _, l := range splitLines(b) {
			if notesRe.MatchString(l) {
				return b, nil
			}
		}
		return append(append([]byte{}, b...), []byte(name+"\talias\t\t"+note+"\t\n")...), nil
	}
	p := &Plan{Title: "add alias " + name, Lines: []string{"add to " + a.Env.Short(file) + ": " + line}}
	withNote := repo && note != "" && al.Notes != ""
	if withNote {
		p.Lines = append(p.Lines, "add its note to "+al.Notes)
	}
	p.Lines = append(p.Lines, dim("checked with zsh -n after; put back if it doesn't parse"))
	printPlan(a.T.Out, p)
	if ans := a.T.Confirm(); ans != ui.Yes {
		if ans == ui.Copy {
			_ = ui.CopyText(line)
			a.say("copied: %s", line)
		}
		return nil
	}
	cur, _ := os.ReadFile(file)
	newText, _ := edit(cur)
	files := map[string][]byte{file: newText}
	if withNote {
		n, _ := os.ReadFile(notesPath)
		nn, _ := notesEdit(n)
		files[notesPath] = nn
	}
	if err := a.writeChecked("alias-"+name, files, []string{file}); err != nil {
		return err
	}
	a.say("%s added to %s", green("✓"), a.Env.Short(file))
	if repo {
		if lines, _ := os.ReadFile(localFile); regexp.MustCompile(`(?m)^alias ` + regexp.QuoteMeta(name) + `=`).Match(lines) {
			a.say("  %s %s also sets %s and loads later, so this Mac keeps that one", amber("~"), a.Env.Short(localFile), name)
		}
		edits := []gitx.Edit{{Path: al.RepoFile, Fn: edit}}
		if withNote {
			edits = append(edits, gitx.Edit{Path: al.Notes, Fn: notesEdit})
		}
		a.offerCommit("shell: alias "+name, "Commit it, so other machines get it on their next pull?", edits)
	}
	a.say("  open a new terminal, or run: %s", bold("exec zsh"))
	return nil
}

// offerCommit commits only dott's change, after a yes. It never pushes.
func (a *App) offerCommit(msg, q string, edits []gitx.Edit) {
	if !a.Env.Has("git") {
		return
	}
	if !(gitx.Repo{Dir: a.M.Dir}).Checkout() {
		a.say("  %s", dim(a.Env.Short(a.M.Dir)+" isn't a git checkout, so there is nothing to commit"))
		return
	}
	if !a.T.YesNo(q+dim(" (only this change; your signing prompt may pop up; not pushed)"), false) {
		a.say("  %s", dim("not committed; it works here. commit and push when you're ready"))
		return
	}
	commit, err := (gitx.Repo{Dir: a.M.Dir}).CommitOnly(msg, edits)
	if err != nil {
		a.say("%s not committed: %v", red("✗"), err)
		return
	}
	a.say("%s committed %s (not pushed)", green("✓"), commit[:min(len(commit), 9)])
}

func (a *App) removeAlias() error {
	al := a.M.Aliases
	name, ok := a.T.Ask("which alias", "")
	if !ok {
		return nil
	}
	if !aliasName.MatchString(name) {
		return fmt.Errorf("an alias name is letters, numbers, - _ . only, and starts with a letter, number or _")
	}
	re := regexp.MustCompile(`(?m)^\s*alias (-- )?` + regexp.QuoteMeta(name) + `=`)
	repoFile, localFile := a.C.Path(al.RepoFile), a.C.Path(al.LocalFile)
	var where []string
	for _, f := range al.Search {
		p := a.C.Path(f)
		if b, err := os.ReadFile(p); err == nil && re.Match(b) {
			where = append(where, p)
		}
	}
	if len(where) == 0 {
		out, _ := zsh(15*time.Second, "-i", "-c", "whence -w -- "+name)
		switch {
		case strings.Contains(out, ": function"):
			return fmt.Errorf("%s is a function, not an alias; open the file with edit shell", name)
		case strings.Contains(out, ": alias"):
			return fmt.Errorf("%s is set outside these files (a plugin or a sourced file); nothing removed", name)
		}
		return fmt.Errorf("no alias called %s", name)
	}
	label := func(p string) string {
		if p == repoFile || strings.HasPrefix(p, a.M.Dir+"/") {
			return a.Env.Short(p) + " (every machine)"
		}
		return a.Env.Short(p) + " (this Mac)"
	}
	var opts []string
	for _, p := range where {
		opts = append(opts, "from "+label(p))
	}
	onlyHere := where[0] == repoFile
	if onlyHere {
		opts = append(opts, "only on this Mac: an unalias line in "+a.Env.Short(localFile)+"; the repo keeps it")
	}
	if len(where) > 1 {
		opts = append(opts, "from all of them")
	}
	n, ok := a.T.Choose("Remove alias "+name+" from where?", opts)
	if !ok {
		a.say("nothing changed")
		return nil
	}
	unalias := "unalias " + name + " 2>/dev/null   # the repo sets it; off on this Mac (dott remove alias)"
	files := map[string][]byte{}
	var check []string
	switch {
	case n < len(where):
		where = where[n : n+1]
	case onlyHere && n == len(where):
		cur, _ := os.ReadFile(localFile)
		files[localFile] = append(joinLines(splitLines(cur)), []byte(unalias+"\n")...)
		where = nil
	}
	notesRe := regexp.MustCompile("^" + regexp.QuoteMeta(name) + "\t")
	unRe := regexp.MustCompile("^unalias " + regexp.QuoteMeta(name) + ` 2>/dev/null   # the repo sets it`)
	repoChanged := false
	for _, p := range where {
		cur, _ := os.ReadFile(p)
		files[p] = RemoveAlias(cur, name)
		check = append(check, p)
		if p == repoFile {
			repoChanged = true
			if lc, err := os.ReadFile(localFile); err == nil && unRe.Match(lc) {
				files[localFile] = dropLines(lc, unRe)
			}
			if al.Notes != "" {
				np := a.C.Path(al.Notes)
				if nb, err := os.ReadFile(np); err == nil {
					files[np] = dropLines(nb, notesRe)
				}
			}
		}
	}
	p := &Plan{Title: "remove alias " + name}
	paths := make([]string, 0, len(files))
	for f := range files {
		paths = append(paths, f)
	}
	sort.Strings(paths)
	for _, f := range paths {
		cur, _ := os.ReadFile(f)
		lines := changeLines(a.Env.Short(f), cur, files[f])
		if len(lines) == 0 {
			delete(files, f) // nothing to change there: no write, no backup
			continue
		}
		p.Lines = append(p.Lines, lines...)
	}
	printPlan(a.T.Out, p)
	if a.T.Confirm() != ui.Yes {
		a.say("skipped remove alias %s", name)
		return nil
	}
	if err := a.writeChecked("unalias-"+name, files, check); err != nil {
		return err
	}
	a.say("%s done", green("✓"))
	if repoChanged {
		edits := []gitx.Edit{{Path: al.RepoFile, Fn: func(b []byte) ([]byte, error) { return RemoveAlias(b, name), nil }}}
		if al.Notes != "" {
			edits = append(edits, gitx.Edit{Path: al.Notes, Fn: func(b []byte) ([]byte, error) { return dropLines(b, notesRe), nil }})
		}
		a.offerCommit("shell: remove alias "+name, "Commit it, so other machines lose it on their next pull?", edits)
	}
	a.say("  open a new terminal, or run: %s (this one still has it until then)", bold("exec zsh"))
	return nil
}
