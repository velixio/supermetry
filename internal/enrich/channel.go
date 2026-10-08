package enrich

import (
	"slices"
	"sync"
)

const (
	ChannelDirect        = "Direct"
	ChannelOrganicSearch = "Organic Search"
	ChannelPaidSearch    = "Paid Search"
	ChannelOrganicSocial = "Organic Social"
	ChannelPaidSocial    = "Paid Social"
	ChannelEmail         = "Email"
	ChannelReferral      = "Referral"
	ChannelAI            = "AI Assistants"
	ChannelOther         = "Other"
)

// ChannelRule matches stored source and medium values exactly. A nil set
// matches any value; "" in a set matches an empty value (dict id 0).
type ChannelRule struct {
	Channel          string
	Sources, Mediums []string
}

var channelRules = sync.OnceValue(buildChannelRules)

// ChannelRules returns the ordered rules; first match wins, else Other.
// Callers must treat the returned rules as read-only.
func ChannelRules() []ChannelRule { return channelRules() }

// Channel maps a stored source and medium to a channel label.
func Channel(source, medium string) string {
	for _, rule := range ChannelRules() {
		if inSet(rule.Sources, source) && inSet(rule.Mediums, medium) {
			return rule.Channel
		}
	}
	return ChannelOther
}

func inSet(set []string, value string) bool {
	return set == nil || slices.Contains(set, value)
}

func buildChannelRules() []ChannelRule {
	data, err := loadSourceData()
	if err != nil {
		panic("enrich: embedded sources.json: " + err.Error())
	}
	category := make(map[string][]string)
	medium := make(map[string][]string)
	for _, rule := range data.Sources {
		if rule.Category != "" {
			category[rule.Category] = append(category[rule.Category], append([]string{rule.Name}, rule.Aliases...)...)
		}
	}
	for _, rule := range data.Mediums {
		medium[rule.Name] = append([]string{rule.Name}, rule.Aliases...)
	}
	for _, set := range [][]string{
		category["ai"], category["search"], category["social"], medium["paid"],
		medium["email"], medium["organic"], medium["social"], medium["referral"],
	} {
		if len(set) == 0 {
			panic("enrich: sources.json lacks a category or medium the channel rules need")
		}
	}
	empty := []string{""}
	return []ChannelRule{
		{Channel: ChannelAI, Sources: category["ai"]},
		{Channel: ChannelPaidSearch, Sources: category["search"], Mediums: medium["paid"]},
		{Channel: ChannelPaidSocial, Sources: category["social"], Mediums: medium["paid"]},
		{Channel: ChannelPaidSearch, Mediums: []string{"paid-search", "paid_search"}},
		{Channel: ChannelEmail, Mediums: medium["email"]},
		{Channel: ChannelOrganicSearch, Mediums: medium["organic"]},
		{Channel: ChannelOrganicSearch, Sources: category["search"], Mediums: empty},
		{Channel: ChannelOrganicSocial, Mediums: medium["social"]},
		{Channel: ChannelOrganicSocial, Sources: category["social"], Mediums: empty},
		{Channel: ChannelReferral, Mediums: medium["referral"]},
		{Channel: ChannelDirect, Sources: empty, Mediums: empty},
	}
}
