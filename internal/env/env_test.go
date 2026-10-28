package env

import "testing"

func TestExpand(t *testing.T) {
	vars := map[string]string{"HOME": "/h", "SET": "/set"}
	e := &Env{Home: "/h", Getenv: func(k string) string { return vars[k] }}
	cases := map[string]string{
		"~":                           "/h",
		"~/x":                         "/h/x",
		"${SET}/y":                    "/set/y",
		"${UNSET:-~/.config}/g":       "/h/.config/g",
		"${SET:-~/.config}/g":         "/set/g",
		"${UNSET:-${HOME}/.config}/g": "/h/.config/g",
		"a ${UNSET:-${SET}} b":        "a /set b",
		"/abs/~/not":                  "/abs/~/not",
		"${UNSET}":                    "",
	}
	for in, want := range cases {
		if got := e.Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
	if got := e.Short("/h/a/b"); got != "~/a/b" {
		t.Errorf("Short = %q", got)
	}
	if got := e.Short("/hx/a"); got != "/hx/a" {
		t.Errorf("Short of a sibling = %q", got)
	}
}
