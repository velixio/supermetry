package enrich

import (
	"strings"
	"testing"
)

func TestSourceClassifierMappings(t *testing.T) {
	classifier, err := NewSourceClassifier()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		host   string
		alias  string
		want   string
		medium string
	}{
		{name: "Google", host: "www.google.co.nz", alias: "google", want: "Google", medium: "organic"},
		{name: "Bing", host: "www.bing.com", alias: "bing", want: "Bing", medium: "organic"},
		{name: "DuckDuckGo", host: "duckduckgo.com", alias: "duckduckgo", want: "DuckDuckGo", medium: "organic"},
		{name: "Yahoo", host: "search.yahoo.com", alias: "yahoo", want: "Yahoo", medium: "organic"},
		{name: "Yandex", host: "yandex.ru", alias: "yandex", want: "Yandex", medium: "organic"},
		{name: "Baidu", host: "baidu.com", alias: "baidu", want: "Baidu", medium: "organic"},
		{name: "Ecosia", host: "ecosia.org", alias: "ecosia", want: "Ecosia", medium: "organic"},
		{name: "Facebook", host: "l.facebook.com", alias: "fb", want: "Facebook", medium: "social"},
		{name: "Instagram", host: "instagram.com", alias: "instagram", want: "Instagram", medium: "social"},
		{name: "X Twitter via t.co", host: "t.co", alias: "twitter", want: "X/Twitter", medium: "social"},
		{name: "X Twitter via x.com", host: "x.com", alias: "x", want: "X/Twitter", medium: "social"},
		{name: "LinkedIn via lnkd.in", host: "lnkd.in", alias: "linkedin", want: "LinkedIn", medium: "social"},
		{name: "LinkedIn", host: "linkedin.com", alias: "linkedin", want: "LinkedIn", medium: "social"},
		{name: "Reddit", host: "reddit.com", alias: "reddit", want: "Reddit", medium: "social"},
		{name: "YouTube", host: "youtube.com", alias: "youtube", want: "YouTube", medium: "social"},
		{name: "Pinterest", host: "pinterest.com", alias: "pinterest", want: "Pinterest", medium: "social"},
		{name: "GitHub", host: "github.com", alias: "github", want: "GitHub", medium: "referral"},
		{name: "Hacker News", host: "news.ycombinator.com", alias: "hacker news", want: "Hacker News", medium: "referral"},
		{name: "ChatGPT via chatgpt.com", host: "chatgpt.com", alias: "chatgpt", want: "ChatGPT", medium: "referral"},
		{name: "ChatGPT via OpenAI", host: "chat.openai.com", alias: "chatgpt", want: "ChatGPT", medium: "referral"},
		{name: "Perplexity", host: "perplexity.ai", alias: "perplexity", want: "Perplexity", medium: "referral"},
	}

	for _, test := range tests {
		t.Run(test.name+" referrer", func(t *testing.T) {
			source, medium := classifier.Source("https://"+strings.ToUpper(test.host)+"/path", "https://site.test/", UTM{})
			if source != test.want || medium != test.medium {
				t.Errorf("Source() = %q/%q, want %q/%q", source, medium, test.want, test.medium)
			}
		})
		t.Run(test.name+" UTM", func(t *testing.T) {
			source, medium := classifier.Source("", "https://site.test/", UTM{Source: strings.ToUpper(test.alias)})
			if source != test.want || medium != test.medium {
				t.Errorf("Source() = %q/%q, want %q/%q", source, medium, test.want, test.medium)
			}
		})
	}

	for _, rule := range classifier.sources {
		for _, domain := range rule.Domains {
			t.Run("configured domain "+rule.Name+" "+domain, func(t *testing.T) {
				host := strings.ReplaceAll(domain, "*", "test")
				source, medium := classifier.Source("https://"+strings.ToUpper(host)+"/", "https://site.test/", UTM{})
				if source != rule.Name || medium != rule.Medium {
					t.Errorf("Source() = %q/%q, want %q/%q", source, medium, rule.Name, rule.Medium)
				}
			})
		}
		for _, alias := range rule.Aliases {
			t.Run("configured UTM "+rule.Name+" "+alias, func(t *testing.T) {
				source, medium := classifier.Source("", "https://site.test/", UTM{Source: strings.ToUpper(alias)})
				if source != rule.Name || medium != rule.Medium {
					t.Errorf("Source() = %q/%q, want %q/%q", source, medium, rule.Name, rule.Medium)
				}
			})
		}
	}

	data, err := loadSourceData()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range data.Mediums {
		for _, alias := range rule.Aliases {
			t.Run("configured medium "+rule.Name+" "+alias, func(t *testing.T) {
				source, medium := classifier.Source("", "https://site.test/", UTM{Source: "campaign", Medium: strings.ToUpper(alias)})
				if source != "campaign" || medium != rule.Name {
					t.Errorf("Source() = %q/%q, want campaign/%q", source, medium, rule.Name)
				}
			})
		}
	}
}

func TestSourceClassifierFallbacks(t *testing.T) {
	classifier, err := NewSourceClassifier()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		referrer string
		pageURL  string
		utm      UTM
		want     string
		medium   string
	}{
		{name: "unknown host", referrer: "https://WWW.supermetry.com./path", pageURL: "https://site.test/", want: "supermetry.com", medium: "referral"},
		{name: "unknown UTM stays original", pageURL: "https://site.test/", utm: UTM{Source: "MyNewsletter", Medium: "Email"}, want: "MyNewsletter", medium: "email"},
		{name: "paid medium", pageURL: "https://site.test/", utm: UTM{Source: "GOOGLE", Medium: "CPC"}, want: "Google", medium: "paid"},
		{name: "explicit social medium", pageURL: "https://site.test/", utm: UTM{Source: "newsletter", Medium: "SOCIAL"}, want: "newsletter", medium: "social"},
		{name: "unknown medium stays original", pageURL: "https://site.test/", utm: UTM{Source: "newsletter", Medium: "partner"}, want: "newsletter", medium: "partner"},
		{name: "internal referrer", referrer: "https://www.site.test/page", pageURL: "https://site.test/", want: "", medium: ""},
		{name: "malformed referrer", referrer: "://bad", pageURL: "https://site.test/", want: "", medium: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, medium := classifier.Source(test.referrer, test.pageURL, test.utm)
			if source != test.want || medium != test.medium {
				t.Errorf("Source() = %q/%q, want %q/%q", source, medium, test.want, test.medium)
			}
		})
	}
}
