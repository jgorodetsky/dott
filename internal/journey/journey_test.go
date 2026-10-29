package journey

import "testing"

// TestWholeRunes: a read can end inside a character (◆ is 3 bytes). The part that came is
// held back until the rest comes, so the screen never loses it.
func TestWholeRunes(t *testing.T) {
	b := []byte("a ◆ b")
	for cut := 1; cut < len(b); cut++ {
		ready, rest := wholeRunes(nil, b[:cut])
		ready2, rest2 := wholeRunes(rest, b[cut:])
		if got := string(ready) + string(ready2); got != "a ◆ b" || len(rest2) != 0 {
			t.Fatalf("cut at %d: %q, left %q", cut, got, rest2)
		}
	}
}
