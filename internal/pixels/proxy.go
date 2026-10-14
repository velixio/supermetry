package pixels

import (
	containerlist "container/list"
	"strings"
	"sync"
	"time"
)

var proxies = []struct{ needle, name string }{
	{"github-camo", "camo"},
	{"GoogleImageProxy", "google"},
}

func DetectProxy(ua string) (string, bool) {
	for _, p := range proxies {
		if strings.Contains(ua, p.needle) {
			return p.name, true
		}
	}
	return "", false
}

const (
	proxyLimiterCapacity = 10_000
	proxyRate            = 20.0
	proxyBurst           = 200.0
)

type proxyKey struct {
	id    int64
	proxy string
}

type proxyBucket struct {
	key    proxyKey
	tokens float64
	last   time.Time
	elem   *containerlist.Element
}

type ProxyLimiter struct {
	mu      sync.Mutex
	buckets map[proxyKey]*proxyBucket
	lru     *containerlist.List
	now     func() time.Time
}

func NewProxyLimiter() *ProxyLimiter {
	return &ProxyLimiter{buckets: make(map[proxyKey]*proxyBucket), lru: containerlist.New(), now: time.Now}
}

func (l *ProxyLimiter) Allow(key int64, proxy string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	k := proxyKey{id: key, proxy: proxy}
	bucket := l.buckets[k]
	if bucket == nil {
		if len(l.buckets) >= proxyLimiterCapacity {
			oldest := l.lru.Front()
			stale := oldest.Value.(*proxyBucket)
			delete(l.buckets, stale.key)
			l.lru.Remove(oldest)
		}
		bucket = &proxyBucket{key: k, tokens: proxyBurst, last: now}
		bucket.elem = l.lru.PushBack(bucket)
		l.buckets[k] = bucket
	} else {
		bucket.tokens = min(proxyBurst, bucket.tokens+now.Sub(bucket.last).Seconds()*proxyRate)
		bucket.last = now
		l.lru.MoveToBack(bucket.elem)
	}
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}
