package reports

import (
	"fmt"
	"strings"

	"supermetry/internal/platform/mail"
	"supermetry/internal/sites"
)

// Breakdown is one row in a top-pages or top-sources list.
type Breakdown struct {
	Value     string
	Visitors  int64
	Pageviews int64
}

// Summary contains the report metrics for one comparison period.
type Summary struct {
	Cadence   string
	Visitors  int64
	Pageviews int64
	Pages     []Breakdown
	Sources   []Breakdown
}

// Render creates plain-text and HTML versions of an analytics report without
// a public base URL (text wordmark, no dashboard link).
func Render(site sites.Site, current, previous Summary) mail.Message {
	return RenderWithBase("", site, current, previous)
}

// RenderWithBase is Render using the shared branded layout. baseURL is the
// public Supermetry URL used for the logo and the dashboard link.
func RenderWithBase(baseURL string, site sites.Site, current, previous Summary) mail.Message {
	cadence := current.Cadence
	if cadence == "" {
		cadence = Weekly
	}
	name := site.PublicID
	var text strings.Builder
	fmt.Fprintf(&text, "%d visitors (%s)\n", current.Visitors, percentChange(current.Visitors, previous.Visitors))
	fmt.Fprintf(&text, "%d pageviews (%s)\n\n", current.Pageviews, percentChange(current.Pageviews, previous.Pageviews))
	writeTextBreakdowns(&text, "Top pages", current.Pages)
	writeTextBreakdowns(&text, "Top sources", current.Sources)

	notice := mail.Notice{
		Subject:   name + " — " + cadence + " report",
		Preheader: fmt.Sprintf("%d visitors, %d pageviews", current.Visitors, current.Pageviews),
		Eyebrow:   cadence + " report",
		Heading:   name,
		Sections: []mail.Section{
			{Heading: "Overview", Rows: []mail.Row{
				{Label: "Visitors", Value: fmt.Sprintf("%d (%s)", current.Visitors, percentChange(current.Visitors, previous.Visitors))},
				{Label: "Pageviews", Value: fmt.Sprintf("%d (%s)", current.Pageviews, percentChange(current.Pageviews, previous.Pageviews))},
			}},
			breakdownSection("Top pages", current.Pages),
			breakdownSection("Top sources", current.Sources),
		},
		ActionLabel: "Open dashboard",
		ActionURL:   strings.TrimRight(baseURL, "/") + "/app",
		Reason:      "You are receiving this because you subscribed to " + cadence + " reports for this site.",
		BaseURL:     baseURL,
	}
	message := notice.Message("")
	// The text part keeps the report body; the layout adds the link and footer.
	message.Text = text.String() + tailOf(notice)
	return message
}

// tailOf returns the plain-text link and footer lines the layout would add.
func tailOf(n mail.Notice) string {
	full := n.Message("").Text
	if index := strings.Index(full, n.ActionLabel+":"); index >= 0 {
		return full[index:]
	}
	return full[strings.LastIndex(full, "--\n"):]
}

func breakdownSection(heading string, values []Breakdown) mail.Section {
	section := mail.Section{Heading: heading}
	for _, value := range firstFive(values) {
		section.Rows = append(section.Rows, mail.Row{Label: value.Value, Value: fmt.Sprintf("%d visitors, %d pageviews", value.Visitors, value.Pageviews)})
	}
	return section
}

func percentChange(current, previous int64) string {
	if previous == 0 {
		if current == 0 {
			return "0.0%"
		}
		return "+100.0%"
	}
	change := float64(current-previous) / float64(previous) * 100
	return fmt.Sprintf("%+.1f%%", change)
}

func writeTextBreakdowns(builder *strings.Builder, heading string, values []Breakdown) {
	builder.WriteString(heading + ":\n")
	for _, value := range firstFive(values) {
		fmt.Fprintf(builder, "- %s: %d visitors, %d pageviews\n", value.Value, value.Visitors, value.Pageviews)
	}
	builder.WriteByte('\n')
}

func firstFive(values []Breakdown) []Breakdown {
	if len(values) > 5 {
		return values[:5]
	}
	return values
}
