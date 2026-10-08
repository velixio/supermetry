// Package icon discovers a site's browser icon without allowing requests to
// private or special-purpose networks.
package icon

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"panel/internal/safehttp"
)

const (
	requestTimeout = 5 * time.Second
	maxPageBytes   = 256 << 10
	maxIconBytes   = 512 << 10
	maxIconURL     = 2048
	maxRedirects   = 3
)

type Options struct {
	Resolver    safehttp.Resolver
	DialContext safehttp.DialContextFunc
}

type Discoverer struct {
	client *http.Client
}

func New(options Options) *Discoverer {
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dial := options.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: requestTimeout}
		dial = dialer.DialContext
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safehttp.GuardedDial(resolver, dial, false),
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
	}
	return &Discoverer{client: &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects || !validURL(request.URL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}
}

var defaultDiscoverer = New(Options{})

// Discover returns a verified icon URL for host, or an empty string when no
// safe image can be found.
func Discover(ctx context.Context, host string) string {
	return defaultDiscoverer.Discover(ctx, host)
}

func (d *Discoverer) Discover(ctx context.Context, host string) string {
	if strings.TrimSpace(host) != host || host == "" || strings.ContainsAny(host, "/?#@") {
		return ""
	}
	for _, scheme := range []string{"https", "http"} {
		if iconURL := d.discoverPage(ctx, scheme+"://"+host+"/"); iconURL != "" {
			return iconURL
		}
	}
	return ""
}

func (d *Discoverer) discoverPage(ctx context.Context, rawURL string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil || !validURL(request.URL) {
		return ""
	}
	response, err := d.client.Do(request)
	base := request.URL
	if err == nil {
		base = response.Request.URL
		if response.StatusCode == http.StatusOK {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+1))
			_ = response.Body.Close()
			if readErr == nil && len(body) <= maxPageBytes {
				for _, href := range preferredHrefs(body) {
					candidate, resolveErr := base.Parse(strings.TrimSpace(href))
					if resolveErr == nil && validURL(candidate) && len(candidate.String()) <= maxIconURL && d.isImage(ctx, candidate.String()) {
						return candidate.String()
					}
				}
			}
		} else {
			_ = response.Body.Close()
		}
	}
	fallback := base.ResolveReference(&url.URL{Path: "/favicon.ico"})
	if validURL(fallback) && len(fallback.String()) <= maxIconURL && d.isImage(ctx, fallback.String()) {
		return fallback.String()
	}
	return ""
}

func (d *Discoverer) isImage(ctx context.Context, rawURL string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	response, err := d.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	size, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxIconBytes+1))
	return readErr == nil && size <= maxIconBytes && response.StatusCode == http.StatusOK && strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "image/")
}

func preferredHrefs(body []byte) []string {
	var groups [3][]string
	tokenizer := html.NewTokenizer(strings.NewReader(string(body)))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		if !strings.EqualFold(token.Data, "link") {
			continue
		}
		var rel, href string
		for _, attribute := range token.Attr {
			switch strings.ToLower(attribute.Key) {
			case "rel":
				rel = strings.ToLower(attribute.Val)
			case "href":
				href = attribute.Val
			}
		}
		if href == "" {
			continue
		}
		tokens := strings.Fields(rel)
		switch {
		case hasToken(tokens, "apple-touch-icon"):
			groups[2] = append(groups[2], href)
		case hasToken(tokens, "shortcut") && hasToken(tokens, "icon"):
			groups[1] = append(groups[1], href)
		case hasToken(tokens, "icon"):
			groups[0] = append(groups[0], href)
		}
	}
	return append(append(groups[0], groups[1]...), groups[2]...)
}

func hasToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func validURL(candidate *url.URL) bool {
	return candidate != nil && (candidate.Scheme == "http" || candidate.Scheme == "https") && candidate.Hostname() != "" && candidate.User == nil
}
