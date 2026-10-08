package enrich

import (
	"container/list"
	"strings"
	"sync"

	"github.com/mileusna/useragent"
)

const uaCacheSize = 10_000

// Client contains the durable, non-identifying dimensions derived from a
// User-Agent. Device is 0 for desktop, 1 for mobile and 2 for tablet.
type Client struct {
	Browser        string
	BrowserVersion string
	OS             string
	OSVersion      string
	Device         int16
}

type uaCacheEntry struct {
	ua     string
	client Client
}

type uaCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	recent  list.List
	limit   int
}

func newUACache(limit int) *uaCache {
	return &uaCache{
		entries: make(map[string]*list.Element, limit),
		limit:   limit,
	}
}

func (c *uaCache) get(ua string) (Client, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[ua]
	if !ok {
		return Client{}, false
	}
	c.recent.MoveToFront(elem)
	return elem.Value.(uaCacheEntry).client, true
}

func (c *uaCache) add(ua string, client Client) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[ua]; ok {
		elem.Value = uaCacheEntry{ua: ua, client: client}
		c.recent.MoveToFront(elem)
		return
	}

	elem := c.recent.PushFront(uaCacheEntry{ua: ua, client: client})
	c.entries[ua] = elem
	if c.recent.Len() <= c.limit {
		return
	}

	oldest := c.recent.Back()
	delete(c.entries, oldest.Value.(uaCacheEntry).ua)
	c.recent.Remove(oldest)
}

// Client parses a User-Agent into analytics dimensions. The original value is
// retained only in the bounded in-memory cache.
func (e *Enricher) Client(ua string) Client {
	if client, ok := e.ua.get(ua); ok {
		return client
	}

	parsed := useragent.Parse(ua)
	client := Client{
		Browser: browserName(parsed.Name),
		OS:      parsed.OS,
	}
	client.BrowserVersion = browserVersionLabel(client.Browser, parsed.Version)
	client.OSVersion = osVersionLabel(client.OS, parsed.OSVersion)
	switch {
	case parsed.Tablet:
		client.Device = 2
	case parsed.Mobile:
		client.Device = 1
	}
	e.ua.add(ua, client)
	return client
}

func browserName(name string) string {
	return strings.TrimPrefix(name, "Mobile ")
}

func majorVersion(version string) string {
	major, _, _ := strings.Cut(strings.ReplaceAll(version, "_", "."), ".")
	if major == "" || len(major) > 4 || strings.Trim(major, "0123456789") != "" {
		return ""
	}
	return major
}

func browserVersionLabel(name, version string) string {
	if major := majorVersion(version); name != "" && major != "" {
		return name + " " + major
	}
	return ""
}

var windowsNT = map[string]string{
	"10.0": "Windows 10/11",
	"6.3":  "Windows 8.1",
	"6.2":  "Windows 8",
	"6.1":  "Windows 7",
}

func osVersionLabel(os, version string) string {
	switch os {
	case "Windows":
		return windowsNT[version]
	case "macOS":
		parts := strings.Split(strings.ReplaceAll(version, "_", "."), ".")
		if majorVersion(parts[0]) == "" {
			return ""
		}
		if parts[0] == "10" && len(parts) > 1 && majorVersion(parts[1]) != "" {
			return "macOS 10." + parts[1]
		}
		return "macOS " + parts[0]
	default:
		return browserVersionLabel(os, version)
	}
}
