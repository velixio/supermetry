package appingest

import (
	"strings"
	"testing"
)

func TestScrubFrames(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"relative path untouched", "backend/services/model_manager.py:12 load", "backend/services/model_manager.py:12 load"},
		{"mac home", "/Users/alice/Projects/app/src/main.ts:5 run", "~/Projects/app/src/main.ts:5 run"},
		{"linux home", "/home/bob/app/main.py:9 go", "~/app/main.py:9 go"},
		{"windows home", `C:\Users\Carol\app\main.js:7:3 start`, "~/app/main.js:7:3 start"},
		{"windows home any case", `c:\users\Carol\App\x.js:1 f`, "~/App/x.js:1 f"},
		{"other absolute keeps last 3 segments", "/opt/vendor/app/backend/main.py:3 run", "app/backend/main.py:3 run"},
		{"short absolute path", "/app/main.py:3 run", "app/main.py:3 run"},
		{"other windows absolute", `D:\build\agent\work\src\a.js:3 run`, "work/src/a.js:3 run"},
		{"file url", "file:///opt/x/y/z/w.js:1:1 boot", "y/z/w.js:1:1 boot"},
		{"file url windows", "file:///C:/Users/dave/app/a.js:2 boot", "~/app/a.js:2 boot"},
		{"parenthesised path", "at load (/home/erin/app/a.js:4:5)", "at load (~/app/a.js:4:5)"},
		{"home is not matched mid path", "/opt/home/x/y/z.py:1 f", "x/y/z.py:1 f"},
		{"division sign survives", "a / b", "a / b"},
		{"blank lines and CR dropped", "a.py:1 f\r\n\r\nb.py:2 g\r\n", "a.py:1 f\nb.py:2 g"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ScrubFrames(c.in); got != c.want {
				t.Fatalf("ScrubFrames(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestScrubFramesCutsOnLineBoundary(t *testing.T) {
	line := strings.Repeat("x", 99) // 99 bytes + newline = 100 per line
	in := strings.TrimSuffix(strings.Repeat(line+"\n", 50), "\n")
	got := ScrubFrames(in)
	if len(got) > 4096 {
		t.Fatalf("len = %d, want <= 4096", len(got))
	}
	if got != strings.TrimSuffix(strings.Repeat(line+"\n", 40), "\n") {
		t.Fatalf("expected exactly 40 whole lines, got %d bytes", len(got))
	}
}

func TestScrubFramesCutsAnOversizedSingleLine(t *testing.T) {
	got := ScrubFrames(strings.Repeat("é", 3000)) // 2 bytes per rune
	if len(got) != 4096 || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("len=%d valid=%v", len(got), !strings.ContainsRune(got, '\uFFFD'))
	}
}

func TestFingerprintUsesValidClientValue(t *testing.T) {
	if got := Fingerprint("crash", "E", "a.py:1 f", "ABCDEF0123"); got != "abcdef0123" {
		t.Fatalf("got %q", got)
	}
}

func TestFingerprintComputed(t *testing.T) {
	// sha256("crash|RuntimeError|a.py load\nb.py run"), hand-computed outside Go.
	const want = "c332bfa066b450480ad1980d4bd647c61ac515174c3edbdf0d901cf39a3fd42c"
	frames := "a.py:10 load\nb.py:20 run"
	if got := Fingerprint("crash", "RuntimeError", frames, ""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Line numbers and a client value that is not hex do not change the result.
	if got := Fingerprint("crash", "RuntimeError", "a.py:99 load\nb.py:1 run", "not hex!"); got != want {
		t.Fatalf("line numbers must not matter: %q", got)
	}
	if got := Fingerprint("crash", "RuntimeError", frames, strings.Repeat("a", 65)); got != want {
		t.Fatalf("over-long client value must be ignored: %q", got)
	}
}

func TestFingerprintOnlyTopFiveFrames(t *testing.T) {
	five := "a:1 f\nb:2 g\nc:3 h\nd:4 i\ne:5 j"
	if Fingerprint("error", "E", five, "") != Fingerprint("error", "E", five+"\nf:6 k\ng:7 l", "") {
		t.Fatal("frames beyond the fifth must not change the fingerprint")
	}
	if Fingerprint("error", "E", five, "") == Fingerprint("error", "E", "z:1 f\n"+five, "") {
		t.Fatal("a different top frame must change the fingerprint")
	}
}

func TestIssueTitle(t *testing.T) {
	cases := []struct{ kind, errType, frames, want string }{
		{"crash", "RuntimeError", "a/b.py:10 load\nc.py:2 x", "RuntimeError in a/b.py load"},
		{"crash", "RuntimeError", "", "RuntimeError"},
		{"hang", "", "m.js:3 tick", "hang in m.js tick"},
		{"install_failed", "", "", "install_failed"},
	}
	for _, c := range cases {
		if got := IssueTitle(c.kind, c.errType, c.frames); got != c.want {
			t.Errorf("IssueTitle(%q,%q,%q) = %q, want %q", c.kind, c.errType, c.frames, got, c.want)
		}
	}
}
