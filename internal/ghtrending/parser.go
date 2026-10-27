// Package ghtrending watches github.com/trending for the repositories an
// instance tracks and keeps a history of the episodes in which each one was
// listed.
//
// The trending page has no stable contract: it is scraped on a best-effort
// basis, politely (see Fetcher), and a page that cannot be read is never taken
// as evidence that a repository left the list.
package ghtrending

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// MinEntries is the fewest repositories a page must list to be trusted. GitHub
// lists dozens; anything below this means the markup changed or the page is an
// error or interstitial page.
const MinEntries = 5

// ErrUnreadable reports a page that could not be understood as a trending
// list. It must never be treated as "the repository is not trending".
var ErrUnreadable = errors.New("ghtrending: trending page is unreadable")

var repoHref = regexp.MustCompile(`^/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/?$`)

// Entry is one repository on a trending list. Repo is lowercase owner/name and
// Rank is the 1-based position on the page.
type Entry struct {
	Repo string `json:"repo"`
	Rank int    `json:"rank"`
}

// ParseList extracts the ordered repositories from a trending page. The HTML
// parser is tolerant of broken markup; the checks that matter are on the
// result: at least one repository row container, and at least MinEntries
// readable rows. Anything else returns an error wrapping ErrUnreadable.
func ParseList(r io.Reader) ([]Entry, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	var rows []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "article" && hasClass(n, "Box-row") {
			// Keep descending: unclosed rows nest the following ones.
			rows = append(rows, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no repository rows found", ErrUnreadable)
	}
	var out []Entry
	seen := map[string]bool{}
	for i, row := range rows {
		repo := rowRepo(row)
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		out = append(out, Entry{Repo: repo, Rank: i + 1})
	}
	if len(out) < MinEntries {
		return nil, fmt.Errorf("%w: only %d readable repositories", ErrUnreadable, len(out))
	}
	return out, nil
}

// rowRepo returns the owner/name from the row's heading link. Other links in
// a row (sponsor buttons, contributors, login) are ignored on purpose.
func rowRepo(row *html.Node) string {
	h := findFirst(row, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "h2" })
	if h == nil {
		return ""
	}
	a := findFirst(h, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && attr(n, "href") != ""
	})
	if a == nil {
		return ""
	}
	href := attr(a, "href")
	if i := strings.IndexAny(href, "?#"); i >= 0 {
		href = href[:i]
	}
	m := repoHref.FindStringSubmatch(href)
	if m == nil {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "sponsors", "login", "orgs", "users":
		return ""
	}
	return strings.ToLower(m[1] + "/" + m[2])
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if match(c) {
			return c
		}
		if f := findFirst(c, match); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}
