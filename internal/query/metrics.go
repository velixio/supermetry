package query

// Metric identifies a value that can be requested from the query engine.
type Metric string

const (
	Visitors       Metric = "visitors"
	Visits         Metric = "visits"
	Pageviews      Metric = "pageviews"
	ViewsPerVisit  Metric = "views_per_visit"
	BounceRate     Metric = "bounce_rate"
	VisitDuration  Metric = "visit_duration"
	EngagementTime Metric = "engagement_time"
	ScrollDepth    Metric = "scroll_depth"
	Events         Metric = "events"
	Conversions    Metric = "conversions"
	ConversionRate Metric = "conversion_rate"
)

// Sums contains the additive values used to calculate query metrics.
type Sums struct {
	Visitors     int64
	Visits       int64
	Pageviews    int64
	Bounces      int64
	DurationMS   int64
	EngagementMS int64
	ScrollSum    int64
	ScrollN      int64
	Events       int64
	Converted    int64
}

// Final calculates the requested metric from additive sums. Rates are
// percentages and durations are seconds.
func (m Metric) Final(sums Sums) float64 {
	switch m {
	case Visitors:
		return float64(sums.Visitors)
	case Visits:
		return float64(sums.Visits)
	case Pageviews:
		return float64(sums.Pageviews)
	case ViewsPerVisit:
		return ratio(sums.Pageviews, sums.Visits)
	case BounceRate:
		return percentage(sums.Bounces, sums.Visits)
	case VisitDuration:
		return ratio(sums.DurationMS, sums.Visits) / 1000
	case EngagementTime:
		return ratio(sums.EngagementMS, sums.Visits) / 1000
	case ScrollDepth:
		return ratio(sums.ScrollSum, sums.ScrollN)
	case Events:
		return float64(sums.Events)
	case Conversions:
		return float64(sums.Converted)
	case ConversionRate:
		return percentage(sums.Converted, sums.Visitors)
	default:
		return 0
	}
}

func ratio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func percentage(numerator, denominator int64) float64 {
	return ratio(numerator, denominator) * 100
}
