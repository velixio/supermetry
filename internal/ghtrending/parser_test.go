package ghtrending

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func parseFixture(t *testing.T, name string) ([]Entry, error) {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return ParseList(f)
}

// The real fixture is a trimmed copy of github.com/trending?since=daily with 14
// rows; some rows carry a sponsor link before the heading link.
func TestParseRealPage(t *testing.T) {
	got, err := parseFixture(t, "trending_all_daily.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 14 {
		t.Fatalf("entries = %d, want 14", len(got))
	}
	if got[0] != (Entry{Repo: "nutlope/hallmark", Rank: 1}) {
		t.Fatalf("first = %+v", got[0])
	}
	if got[13].Rank != 14 {
		t.Fatalf("last rank = %d", got[13].Rank)
	}
	for _, e := range got {
		if e.Repo != strings.ToLower(e.Repo) || strings.HasPrefix(e.Repo, "sponsors/") || strings.Contains(e.Repo, "login") {
			t.Fatalf("bad entry %+v", e)
		}
	}
	if got[4].Repo != "affaan-m/ecc" || got[10].Repo != "debpalash/voicestudio" {
		t.Fatalf("sponsor rows must resolve to the heading link: %+v %+v", got[4], got[10])
	}
}

func TestParseBrokenMarkup(t *testing.T) {
	got, err := parseFixture(t, "trending_broken.html")
	if err != nil {
		t.Fatal(err)
	}
	// Unclosed tags nest the rows inside each other; the login row has no repo.
	want := []string{"alpha/one", "beta/two", "gamma/three.js", "delta/four-4", "epsilon/five_5", "zeta/six"}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v", got)
	}
	for i, w := range want {
		if got[i].Repo != w || got[i].Rank != i+1 {
			t.Fatalf("entry %d = %+v, want %s rank %d", i, got[i], w, i+1)
		}
	}
}

func TestParseUnreadable(t *testing.T) {
	row := func(r string) string {
		return `<article class="Box-row"><h2><a href="/` + r + `">x</a></h2></article>`
	}
	cases := map[string]string{
		"empty":        "",
		"no container": `<html><body><h1>Rate limited</h1><a href="/a/b">a</a></body></html>`,
		"too few":      "<div>" + row("a/a") + row("b/b") + row("c/c") + row("d/d") + "</div>",
		"unparsable":   "<div>" + strings.Repeat(`<article class="Box-row"><h2>no link</h2></article>`, 8) + "</div>",
		"duplicates":   "<div>" + strings.Repeat(row("a/a"), 9) + "</div>",
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseList(strings.NewReader(page))
			if !errors.Is(err, ErrUnreadable) || got != nil {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestParseExactlyMinimum(t *testing.T) {
	var b strings.Builder
	for _, r := range []string{"a/a", "b/b", "c/c", "d/d", "E/E"} {
		b.WriteString(`<article class="Box-row extra"><h2><a href="/` + r + `?x=1">x</a></h2></article>`)
	}
	got, err := ParseList(strings.NewReader(b.String()))
	if err != nil || len(got) != 5 || got[4].Repo != "e/e" {
		t.Fatalf("got %v, %v", got, err)
	}
}
