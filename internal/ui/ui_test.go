package ui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func newTerm(input string) (*Term, *bytes.Buffer) {
	var out bytes.Buffer
	return &Term{In: bufio.NewReader(strings.NewReader(input)), Out: &out}, &out
}

func TestConfirm(t *testing.T) {
	cases := map[string]Answer{
		"\n": Yes, "y\n": Yes, "Y\n": Yes, "yes\n": Yes,
		"n\n": No, "no\n": No, "maybe\n": No, "": No,
		"c\n": Copy, "copy\n": Copy, " COPY \n": Copy,
	}
	for in, want := range cases {
		tm, out := newTerm(in)
		if got := tm.Confirm(); got != want {
			t.Errorf("Confirm(%q) = %v, want %v", in, got, want)
		}
		if !strings.Contains(out.String(), "Run it? [Y/n/copy]") {
			t.Errorf("prompt missing: %q", out.String())
		}
	}
}

func TestTypeYes(t *testing.T) {
	cases := map[string]bool{"yes\n": true, "YES\n": true, "y\n": false, "\n": false, "": false, "yes please\n": false}
	for in, want := range cases {
		tm, _ := newTerm(in)
		if got := tm.TypeYes(); got != want {
			t.Errorf("TypeYes(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPlan(t *testing.T) {
	tm, out := newTerm("")
	tm.Plan("fix aws", []string{"run bin/awssetup"})
	if !strings.Contains(out.String(), "fix aws") || !strings.Contains(out.String(), "◆\x1b[0m run bin/awssetup") {
		t.Fatalf("plan output: %q", out.String())
	}
}
