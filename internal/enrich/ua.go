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
	Browser string
	OS      string
	Device  int16
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
