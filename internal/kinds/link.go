package kinds

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jgorodetsky/dott/internal/engine"
	"github.com/jgorodetsky/dott/internal/fsops"
	"github.com/jgorodetsky/dott/internal/manifest"
)

// Link is a symlink from a place on this Mac into the repo, so edits in the repo apply at once.
type Link struct{}

func (Link) Detect(c *engine.Ctx, it *manifest.Item) engine.Status {
	link, to := c.Path(it.Link), c.Path(it.To)
	if !exists(to) {
		return status(engine.Blocked, "the repo has no "+it.To, "check the repo")
	}
	st, detail, fix := linkState(c, link, to, it.To, "you have your own "+c.Env.Short(link))
	return status(st, detail, fix)
}

// linkState checks that link points at to (rel is to inside the repo). mine is the detail
// when a real file or folder of the user's is in the way.
func linkState(c *engine.Ctx, link, to, rel, mine string) (engine.State, string, string) {
	short := c.Env.Short(link)
	fi, err := os.Lstat(link)
	switch {
	case os.IsNotExist(err):
		return engine.Drift, "not set up: " + short + " → " + c.Rel(to), "link it"
	case err != nil:
		return engine.Blocked, "can't read " + short + ": " + err.Error(), ""
	case fi.Mode()&os.ModeSymlink == 0:
		return engine.Conflict, mine, "keep yours, or replace it (yours goes to the backup)"
	}
	t, _ := os.Readlink(link)
	if !filepath.IsAbs(t) {
		t = filepath.Join(filepath.Dir(link), t)
	}
	switch {
	case filepath.Clean(t) == filepath.Clean(to):
		return engine.OK, short + " → " + c.Rel(to), ""
	case !exists(t):
		return engine.Drift, "a broken link to " + c.Env.Short(t), "point it at the repo"
	case otherCheckout(c, t, rel) != "":
		return engine.Drift, "points at another checkout: " + c.Env.Short(otherCheckout(c, t, rel)), "point it here"
	}
	return engine.Conflict, short + " links to " + c.Env.Short(t) + ", not the repo", "keep yours, or replace it"
}

func (Link) Plan(c *engine.Ctx, it *manifest.Item, s engine.Status, part string, replace bool) ([]engine.Change, error) {
	return linkChanges(c, c.Path(it.Link), c.Path(it.To), s.State, replace)
}

func linkChanges(c *engine.Ctx, link, to string, st engine.State, replace bool) ([]engine.Change, error) {
	switch st {
	case engine.Drift:
		return []engine.Change{{
			Summary: "link " + c.Env.Short(link) + " → " + c.Rel(to),
			Shell:   engine.Quote("ln", "-sfn", to, link),
			Paths:   []string{link},
			Do:      func(*engine.Exec) error { return fsops.Symlink(to, link) },
		}}, nil
	case engine.Conflict:
		if !replace {
			return nil, fmt.Errorf("%s is yours; pick replace to swap it for the repo's (yours goes to the backup)", c.Env.Short(link))
		}
		return []engine.Change{{
			Summary: "move your " + c.Env.Short(link) + " to the backup, then link it → " + c.Rel(to),
			Shell:   engine.Quote("mv", link, link+".bak") + " && " + engine.Quote("ln", "-s", to, link),
			Paths:   []string{link},
			Danger:  true,
			Do: func(*engine.Exec) error {
				if err := os.RemoveAll(link); err != nil {
					return err
				}
				return fsops.Symlink(to, link)
			},
		}}, nil
	}
	return nil, fmt.Errorf("nothing to fix (%s)", st)
}
