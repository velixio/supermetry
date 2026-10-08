package enrich

import "testing"

func TestChannel(t *testing.T) {
	tests := []struct {
		name           string
		source, medium string
		want           string
	}{
		{name: "ChatGPT referral", source: "ChatGPT", medium: "referral", want: ChannelAI},
		{name: "Claude referral", source: "Claude", medium: "referral", want: ChannelAI},
		{name: "Perplexity referral", source: "Perplexity", medium: "referral", want: ChannelAI},
		{name: "Gemini referral", source: "Gemini", medium: "referral", want: ChannelAI},
		{name: "Copilot referral", source: "Copilot", medium: "referral", want: ChannelAI},
		{name: "AI precedence over paid", source: "chatgpt", medium: "paid", want: ChannelAI},
		{name: "Google paid", source: "Google", medium: "paid", want: ChannelPaidSearch},
		{name: "Bing cpc", source: "Bing", medium: "cpc", want: ChannelPaidSearch},
		{name: "DuckDuckGo paid", source: "DuckDuckGo", medium: "paid", want: ChannelPaidSearch},
		{name: "Facebook paid", source: "Facebook", medium: "paid", want: ChannelPaidSocial},
		{name: "LinkedIn paid social", source: "LinkedIn", medium: "paid-social", want: ChannelPaidSocial},
		{name: "unknown paid source", source: "Foo", medium: "paid", want: ChannelOther},
		{name: "raw paid search medium", source: "Foo", medium: "paid-search", want: ChannelPaidSearch},
		{name: "unknown paid social spelling", source: "Foo", medium: "paid_social", want: ChannelOther},
		{name: "newsletter email", source: "newsletter", medium: "email", want: ChannelEmail},
		{name: "empty source email", medium: "email", want: ChannelEmail},
		{name: "Google organic", source: "Google", medium: "organic", want: ChannelOrganicSearch},
		{name: "Google default", source: "Google", want: ChannelOrganicSearch},
		{name: "search medium", source: "Foo", medium: "search", want: ChannelOrganicSearch},
		{name: "Facebook social", source: "Facebook", medium: "social", want: ChannelOrganicSocial},
		{name: "Reddit default", source: "Reddit", want: ChannelOrganicSocial},
		{name: "external referral", source: "example.com", medium: "referral", want: ChannelReferral},
		{name: "GitHub referral", source: "GitHub", medium: "referral", want: ChannelReferral},
		{name: "Hacker News referral", source: "Hacker News", medium: "referral", want: ChannelReferral},
		{name: "both empty", want: ChannelDirect},
		{name: "literal Direct source", source: "Direct", want: ChannelOther},
		{name: "unknown source no medium", source: "newsletter", want: ChannelOther},
		{name: "unknown medium", source: "Foo", medium: "banner", want: ChannelOther},
		{name: "configured alias", source: "google", want: ChannelOrganicSearch},
		{name: "source matching is exact", source: "GOOGLE", want: ChannelOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Channel(tt.source, tt.medium); got != tt.want {
				t.Errorf("Channel(%q, %q) = %q, want %q", tt.source, tt.medium, got, tt.want)
			}
		})
	}
}

func TestAIReferrersClassify(t *testing.T) {
	classifier, err := NewSourceClassifier()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ host, want string }{
		{host: "gemini.google.com", want: "Gemini"},
		{host: "claude.ai", want: "Claude"},
		{host: "copilot.microsoft.com", want: "Copilot"},
		{host: "chatgpt.com", want: "ChatGPT"},
		{host: "www.perplexity.ai", want: "Perplexity"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			source, medium := classifier.Source("https://"+tt.host+"/x", "https://site.test/", UTM{})
			if source != tt.want || Channel(source, medium) != ChannelAI {
				t.Errorf("%s -> (%q, %q), want %q as %s", tt.host, source, medium, tt.want, ChannelAI)
			}
		})
	}
}

func TestChannelRulesAreWellFormed(t *testing.T) {
	for i, rule := range ChannelRules() {
		if rule.Sources == nil && rule.Mediums == nil {
			t.Errorf("rule %d matches every source and medium", i)
		}
		if rule.Sources != nil && len(rule.Sources) == 0 {
			t.Errorf("rule %d has an empty non-nil source set", i)
		}
		if rule.Mediums != nil && len(rule.Mediums) == 0 {
			t.Errorf("rule %d has an empty non-nil medium set", i)
		}
	}
}
