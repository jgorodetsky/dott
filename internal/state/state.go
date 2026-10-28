// Package state is what dott remembers between runs, in ~/.local/state/dott: the rows you
// pick, the hash of each copy it installed, and backups of what it replaced.
package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// State is one state folder.
type State struct{ Dir string }

func (s State) path(name string) string { return filepath.Join(s.Dir, name) }

// History is the file fzf keeps the typed queries in.
func (s State) History() string { return s.path("history") }

// Backups is the folder that holds one subfolder per fix that replaced something.
func (s State) Backups() string { return s.path("backups") }

// Picked is one entry of the pick log.
type Picked struct {
	At  time.Time
	Key string
}

// Picks reads the pick log, oldest first. Each line is "<unix time>\t<row key>".
func (s State) Picks() []Picked {
	f, err := os.Open(s.path("picks"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Picked
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ts, key, ok := strings.Cut(strings.TrimSpace(sc.Text()), "\t")
		if !ok || key == "" {
			continue
		}
		n, _ := strconv.ParseInt(ts, 10, 64)
		out = append(out, Picked{time.Unix(n, 0), key})
	}
	return out
}

// Pick adds one pick to the log. The log keeps the last 500.
func (s State) Pick(key string, at time.Time) error {
	picks := append(s.Picks(), Picked{at, key})
	if len(picks) > 500 {
		picks = picks[len(picks)-500:]
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, p := range picks {
		fmt.Fprintf(&b, "%d\t%s\n", p.At.Unix(), p.Key)
	}
	return os.WriteFile(s.path("picks"), []byte(b.String()), 0o644)
}

// Frecent ranks keys the way ghh does: each pick counts 0.5^(age in days / 30), so a key
// picked often and lately comes first. The last pick is returned on its own.
func (s State) Frecent(now time.Time) (last string, ranked []string) {
	picks := s.Picks()
	if len(picks) == 0 {
		return "", nil
	}
	score := map[string]float64{}
	for _, p := range picks {
		age := now.Sub(p.At).Hours() / 24
		score[p.Key] += math.Pow(0.5, age/30)
	}
	last = picks[len(picks)-1].Key
	for k := range score {
		if k != last {
			ranked = append(ranked, k)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if score[ranked[i]] != score[ranked[j]] {
			return score[ranked[i]] > score[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})
	return last, ranked
}

// Records are hashes of what dott last installed, by key (skill:NAME, copy:PATH).
func (s State) Records() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(s.path("records.json"))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// SetRecord stores one hash; an empty hash removes it.
func (s State) SetRecord(key, hash string) error {
	m := s.Records()
	if hash == "" {
		delete(m, key)
	} else {
		m[key] = hash
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	tmp := s.path("records.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path("records.json"))
}

// Lock makes sure only one dott changes things at a time. The returned func lets go.
func (s State) Lock() (func(), error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, err
	}
	p := s.path("lock")
	for range 2 {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		// a lock from a dott that is gone is stale
		b, _ := os.ReadFile(p)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			return nil, fmt.Errorf("another dott (pid %d) is changing things; wait for it to finish", pid)
		}
		os.Remove(p)
	}
	return nil, fmt.Errorf("could not take the lock %s", p)
}
