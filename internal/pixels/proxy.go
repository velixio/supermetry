package pixels

import (
	"strconv"
	"strings"

	"supermetry/internal/ingest"
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

type ProxyLimiter struct{ buckets ingest.RateLimiter }

func NewProxyLimiter() *ProxyLimiter { return &ProxyLimiter{buckets: ingest.NewTokenBuckets(20, 200)} }

func (l *ProxyLimiter) Allow(key int64, proxy string) bool {
	return l.buckets.Allow(strconv.FormatInt(key, 10) + ":" + proxy)
}
