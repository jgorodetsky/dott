package state

import (
	"testing"
	"time"
)

func TestFrecent(t *testing.T) {
	s := State{Dir: t.TempDir()}
	now := time.Now()
	day := 24 * time.Hour
	for _, p := range []struct {
		key string
		age time.Duration
	}{{"old", 200 * day}, {"old", 199 * day}, {"old", 198 * day}, {"often", 2 * day}, {"often", day}, {"once", 3 * day}, {"last", 0}} {
		if err := s.Pick(p.key, now.Add(-p.age)); err != nil {
			t.Fatal(err)
		}
	}
	last, ranked := s.Frecent(now)
	if last != "last" || len(ranked) != 3 || ranked[0] != "often" || ranked[1] != "once" || ranked[2] != "old" {
		t.Fatalf("last %q ranked %v", last, ranked)
	}
}

func TestPickLogKeeps500(t *testing.T) {
	s := State{Dir: t.TempDir()}
	for i := 0; i < 510; i++ {
		s.Pick("k", time.Now())
	}
	if n := len(s.Picks()); n != 500 {
		t.Fatalf("%d picks kept", n)
	}
}

func TestRecordsAndLock(t *testing.T) {
	s := State{Dir: t.TempDir()}
	s.SetRecord("skill:a", "h1")
	s.SetRecord("skill:b", "h2")
	s.SetRecord("skill:a", "")
	if r := s.Records(); len(r) != 1 || r["skill:b"] != "h2" {
		t.Fatalf("records %v", r)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lock(); err == nil {
		t.Fatal("a second lock while one is held")
	}
	unlock()
	unlock2, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock2()
}
