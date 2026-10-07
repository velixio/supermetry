package core

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// TestSessionMemory reports heap bytes per live session (R2 sizing).
func TestSessionMemory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const n = 200_000
	countries := []string{"US", "IN", "DE"}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	c := NewCache(CacheConfig{})
	for i := range n {
		// Per-request allocated strings, like the HTTP path (URL path, source host).
		path := strings.Clone(fmt.Sprintf("/blog/post-%d-some-slug", i%2000))
		src := strings.Clone("www.google.com")
		e := &Event{SiteID: 1, Day: 20000, TS: int64(i), VisitorID: int64(i)*2654435761 + 1, Name: "pageview",
			Path: path, Source: src, Country: countries[i%3], Browser: "Chrome", OS: "Windows", Device: "desktop"}
		c.Assign(e, nil)
	}
	c.Collect(0, false) // drop dirty lists, as the 10 s flusher does
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("live sessions %d: %.0f heap bytes/session", c.Len(), float64(after.HeapAlloc-before.HeapAlloc)/n)
	runtime.KeepAlive(c)
}
