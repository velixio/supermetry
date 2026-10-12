package shares

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"supermetry/internal/api"
	"supermetry/internal/query"
)

type Section string

const (
	SectionKPIs         Section = "kpis"
	SectionChart        Section = "chart"
	SectionPages        Section = "pages"
	SectionSources      Section = "sources"
	SectionLocations    Section = "locations"
	SectionDevices      Section = "devices"
	SectionWhere        Section = "where"
	SectionWeeklyRhythm Section = "weekly_rhythm"
	SectionGoals        Section = "goals"
)

var allSections = []Section{
	SectionKPIs, SectionChart, SectionPages, SectionSources, SectionLocations,
	SectionDevices, SectionWhere, SectionWeeklyRhythm, SectionGoals,
}

type DateMode string

const (
	DateRolling DateMode = "rolling"
	DateFixed   DateMode = "fixed"
)

type Theme string

const (
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
	ThemeSystem Theme = "system"
)

type DateConfig struct {
	Mode    DateMode `json:"mode"`
	Days    int      `json:"days,omitempty"`
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	MaxDays int      `json:"max_days"`
}

type EmbedConfig struct {
	Enabled         bool     `json:"enabled"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
	ConfirmWildcard bool     `json:"confirm_wildcard,omitempty"`
}

type Config struct {
	Sections  []Section    `json:"sections"`
	Filters   []api.Filter `json:"filters,omitempty"`
	SegmentID *int64       `json:"segment_id,omitempty"`
	Date      DateConfig   `json:"date"`
	Theme     Theme        `json:"theme"`
	Embed     EmbedConfig  `json:"embed"`
}

func LegacyConfig() Config {
	return Config{
		Sections: []Section{SectionKPIs},
		Date:     DateConfig{Mode: DateRolling, Days: 7, MaxDays: 7},
		Theme:    ThemeSystem,
	}
}

func NormalizeConfig(input Config) (Config, error) {
	if len(input.Sections) == 0 {
		input.Sections = []Section{SectionKPIs}
	}
	seenSections := make(map[Section]bool, len(input.Sections))
	for _, section := range input.Sections {
		if !slices.Contains(allSections, section) || seenSections[section] {
			return Config{}, ErrInvalid
		}
		seenSections[section] = true
	}
	if len(input.Filters) > 10 || query.ValidateFilters(input.Filters) != nil {
		return Config{}, ErrInvalid
	}
	if input.SegmentID != nil && (*input.SegmentID <= 0 || len(input.Filters) != 0) {
		return Config{}, ErrInvalid
	}
	if input.Theme == "" {
		input.Theme = ThemeSystem
	}
	if input.Theme != ThemeLight && input.Theme != ThemeDark && input.Theme != ThemeSystem {
		return Config{}, ErrInvalid
	}
	if err := normalizeDate(&input.Date); err != nil {
		return Config{}, err
	}
	if err := normalizeEmbed(&input.Embed); err != nil {
		return Config{}, err
	}
	return input, nil
}

func normalizeDate(date *DateConfig) error {
	if date.Mode == "" {
		date.Mode, date.Days = DateRolling, 7
	}
	switch date.Mode {
	case DateRolling:
		if date.Days < 1 || date.Days > 366 || date.From != "" || date.To != "" {
			return ErrInvalid
		}
		if date.MaxDays == 0 {
			date.MaxDays = date.Days
		}
		if date.MaxDays < date.Days || date.MaxDays > 366 {
			return ErrInvalid
		}
	case DateFixed:
		if date.Days != 0 || date.From == "" || date.To == "" {
			return ErrInvalid
		}
		from, fromErr := time.Parse(time.DateOnly, date.From)
		to, toErr := time.Parse(time.DateOnly, date.To)
		span := int(to.Sub(from).Hours()/24) + 1
		if fromErr != nil || toErr != nil || span < 1 || span > 366 {
			return ErrInvalid
		}
		if date.MaxDays == 0 {
			date.MaxDays = span
		}
		if date.MaxDays < span || date.MaxDays > 366 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func normalizeEmbed(embed *EmbedConfig) error {
	if !embed.Enabled {
		if len(embed.AllowedOrigins) != 0 || embed.ConfirmWildcard {
			return ErrInvalid
		}
		return nil
	}
	if len(embed.AllowedOrigins) == 0 {
		if !embed.ConfirmWildcard {
			return ErrInvalid
		}
		return nil
	}
	if embed.ConfirmWildcard || len(embed.AllowedOrigins) > 20 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(embed.AllowedOrigins))
	for i, raw := range embed.AllowedOrigins {
		origin := strings.TrimSpace(raw)
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return ErrInvalid
		}
		origin = parsed.Scheme + "://" + parsed.Host
		if seen[origin] {
			return ErrInvalid
		}
		seen[origin] = true
		embed.AllowedOrigins[i] = origin
	}
	return nil
}

func HasSection(config Config, section Section) bool {
	return slices.Contains(config.Sections, section)
}

func FrameAncestors(config Config) (string, bool) {
	if !config.Embed.Enabled {
		return "'none'", false
	}
	if len(config.Embed.AllowedOrigins) == 0 && config.Embed.ConfirmWildcard {
		return "*", true
	}
	if len(config.Embed.AllowedOrigins) == 0 {
		return "'none'", false
	}
	return strings.Join(config.Embed.AllowedOrigins, " "), true
}
