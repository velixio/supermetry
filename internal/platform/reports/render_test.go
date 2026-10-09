package reports

import (
	"strings"
	"testing"
	"time"

	"supermetry/internal/sites"
)

func TestRenderIncludesMetricsBreakdownsAndChanges(t *testing.T) {
	t.Parallel()

	message := Render(sites.Site{PublicID: "example-site", TZ: time.UTC}, Summary{
		Cadence:   Weekly,
		Visitors:  120,
		Pageviews: 300,
		Pages: []Breakdown{
			{Value: "/one", Visitors: 50, Pageviews: 80},
			{Value: "/two", Visitors: 40, Pageviews: 70},
			{Value: "/three", Visitors: 30, Pageviews: 60},
			{Value: "/four", Visitors: 20, Pageviews: 50},
			{Value: "/five", Visitors: 10, Pageviews: 40},
			{Value: "/six", Visitors: 5, Pageviews: 10},
		},
		Sources: []Breakdown{
			{Value: "Google", Visitors: 70, Pageviews: 140},
			{Value: "Direct", Visitors: 50, Pageviews: 160},
		},
	}, Summary{Visitors: 100, Pageviews: 250})

	if message.Subject != "example-site — weekly report" {
		t.Fatalf("Subject = %q", message.Subject)
	}
	for _, want := range []string{"120 visitors", "+20.0%", "300 pageviews", "/one", "/five", "Google", "Direct"} {
		if !strings.Contains(message.Text, want) {
			t.Errorf("text does not contain %q:\n%s", want, message.Text)
		}
		if !strings.Contains(message.HTML, want) {
			t.Errorf("HTML does not contain %q:\n%s", want, message.HTML)
		}
	}
	if strings.Contains(message.Text, "/six") || strings.Contains(message.HTML, "/six") {
		t.Fatal("rendered more than five pages")
	}
}
