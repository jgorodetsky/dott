package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jgorodetsky/dott/internal/picker"
)

// fakePicker records the request and answers with res.
type fakePicker struct {
	got *picker.Request
	res picker.Result
}

func (f fakePicker) Pick(r picker.Request) (picker.Result, error) { *f.got = r; return f.res, nil }
func (fakePicker) Filter([]string, string) ([]string, error)      { return nil, nil }

func TestPick(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		res   picker.Result
		code  int
		out   string
		err   string
		want  picker.Request // checked when Prompt is set
	}{
		{name: "a line", args: []string{"--prompt", "which"}, stdin: "a\nb\n\n", res: picker.Result{Line: "b", Lines: []string{"b"}}, code: 0, out: "b\n",
			want: picker.Request{Plain: true, Prompt: "which › ", Lines: []string{"a", "b"}}},
		{name: "several", args: []string{"--multi", "--prompt=parts? "}, stdin: "1 acme\n3 ENG-123\n", res: picker.Result{Lines: []string{"1 acme", "3 ENG-123"}}, code: 0, out: "1 acme\n3 ENG-123\n",
			want: picker.Request{Plain: true, Multi: true, Prompt: "parts? ", Lines: []string{"1 acme", "3 ENG-123"}}},
		{name: "a text box", args: []string{"--input", "--prompt", "format", "--query", "{3}", "--preview", "echo {q}"}, res: picker.Result{Query: "{3}: {4:words}"}, code: 0, out: "{3}: {4:words}\n",
			want: picker.Request{Plain: true, Input: true, Prompt: "format › ", Query: "{3}", Preview: "echo {q}"}},
		{name: "esc", stdin: "a\n", res: picker.Result{Back: true}, code: 1},
		{name: "ctrl-c", stdin: "a\n", res: picker.Result{Code: picker.CodeInterrupt}, code: 130},
		{name: "no lines", stdin: "\n", code: 2, err: "no lines to pick from"},
		{name: "a bad argument", args: []string{"--nope"}, code: 2, err: "unknown argument --nope"},
		{name: "a value missing", args: []string{"--prompt"}, code: 2, err: "--prompt needs a value"},
		{name: "a text box takes one", args: []string{"--input", "--multi"}, code: 2, err: "can't take --multi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got picker.Request
			newPicker = func() picker.Backend { return fakePicker{&got, tc.res} }
			t.Cleanup(func() { newPicker = picker.New })
			var out, errw bytes.Buffer
			code := pick(tc.args, strings.NewReader(tc.stdin), &out, &errw)
			if code != tc.code || out.String() != tc.out || !strings.Contains(errw.String(), tc.err) {
				t.Fatalf("code %d, out %q, err %q", code, out.String(), errw.String())
			}
			if tc.want.Prompt != "" && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("asked the picker for\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// TestJourneyPickMulti runs dott --pick --multi in a terminal with the built-in fzf: tab
// picks two lines and both print.
func TestJourneyPickMulti(t *testing.T) {
	_, env := machine(t)
	out := filepath.Join(t.TempDir(), "out")
	s := open(t, env, 80, 24, "/bin/sh", "-c", `printf '1 acme\n3 ENG-123\n4 fix-login\n' | "$0" --pick --multi --prompt parts > "$1"; echo "EXIT:$?"`, dottBin(t), out)
	s.waitScreen("parts ›")
	s.send("\t\t")
	s.waitScreen("(2)")
	s.send("\r")
	s.waitFor("EXIT:0", 1)
	if err := s.Still(200*time.Millisecond, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := read(t, out); got != "1 acme\n3 ENG-123\n" {
		t.Fatalf("printed %q", got)
	}
	_ = os.Remove(out)
}
