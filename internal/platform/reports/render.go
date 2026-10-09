package reports

import (
	"fmt"
	"html"
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

// Render creates plain-text and HTML versions of an analytics report.
func Render(site sites.Site, current, previous Summary) mail.Message {
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

	var body strings.Builder
	body.WriteString("<html><body>")
	fmt.Fprintf(&body, "<h1>%s — %s report</h1>", html.EscapeString(name), html.EscapeString(cadence))
	fmt.Fprintf(&body, "<p>%d visitors (%s)<br>%d pageviews (%s)</p>", current.Visitors,
		html.EscapeString(percentChange(current.Visitors, previous.Visitors)), current.Pageviews,
		html.EscapeString(percentChange(current.Pageviews, previous.Pageviews)))
	writeHTMLBreakdowns(&body, "Top pages", current.Pages)
	writeHTMLBreakdowns(&body, "Top sources", current.Sources)
	body.WriteString("</body></html>")

	return mail.Message{Subject: name + " — " + cadence + " report", Text: text.String(), HTML: body.String()}
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

func writeHTMLBreakdowns(builder *strings.Builder, heading string, values []Breakdown) {
	fmt.Fprintf(builder, "<h2>%s</h2><ul>", html.EscapeString(heading))
	for _, value := range firstFive(values) {
		fmt.Fprintf(builder, "<li>%s: %d visitors, %d pageviews</li>", html.EscapeString(value.Value), value.Visitors, value.Pageviews)
	}
	builder.WriteString("</ul>")
}

func firstFive(values []Breakdown) []Breakdown {
	if len(values) > 5 {
		return values[:5]
	}
	return values
}
