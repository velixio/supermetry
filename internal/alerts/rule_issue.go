package alerts

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"supermetry/internal/sites"
)

// evalIssue produces the new, regressed and escalated candidates. New and
// regressed use the v1 keys, so a converted v1 alert never repeats a
// notification it already sent. Filters apply through the issue tag rollup.
func (e *RuleEvaluator) evalIssue(ctx context.Context, alert Alert, site sites.Site, now time.Time) ([]Candidate, error) {
	rule := alert.Rule
	filters, ok, err := e.resolveFilters(ctx, site.ID, rule.Filters, now)
	if err != nil || !ok {
		return nil, err
	}
	info, err := e.app.Site(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	since := now.Add(-appLookback)
	if alert.CreatedAt.After(since) {
		since = alert.CreatedAt
	}
	var out []Candidate
	for _, on := range rule.Trigger.Issue.On {
		switch on {
		case "new":
			issues, err := e.app.NewIssues(ctx, site.ID, since, filters.Kinds, appCandidateLimit)
			if err != nil {
				return nil, err
			}
			for _, issue := range issues {
				out = append(out, issueCandidate("issue:"+strconv.FormatInt(issue.ID, 10), info, issue, issue.Events, 1, issue.FirstSeen, now))
			}
		case "regressed":
			issues, err := e.app.RegressedIssues(ctx, site.ID, since, appCandidateLimit)
			if err != nil {
				return nil, err
			}
			for _, issue := range issues {
				key := "regress:" + strconv.FormatInt(issue.ID, 10) + ":" + strconv.FormatInt(issue.RegressedAt.Unix(), 10)
				out = append(out, issueCandidate(key, info, issue, issue.Events, 1, issue.RegressedAt, now))
			}
		case "escalated":
			issues, err := e.app.EscalatedIssues(ctx, site.ID, since, appCandidateLimit)
			if err != nil {
				return nil, err
			}
			for _, issue := range issues {
				key := "escalate:" + strconv.FormatInt(issue.ID, 10) + ":" + strconv.FormatInt(issue.ActivityID, 10)
				out = append(out, issueCandidate(key, info, issue, issue.Events, 1, issue.EscalatedAt, now))
			}
		}
	}
	return e.filterIssues(ctx, site.ID, filters, out)
}

// evalEvent evaluates the event-frequency trigger per issue: a count (or
// distinct users) strictly above a threshold within a sliding window, or a spike of the
// last closed hour over the issue's baseline. FrequencyMinutes spaces the
// repeats of one issue.
func (e *RuleEvaluator) evalEvent(ctx context.Context, alert Alert, site sites.Site, now time.Time) ([]Candidate, error) {
	rule := alert.Rule
	trigger := rule.Trigger.Event
	filters, ok, err := e.resolveFilters(ctx, site.ID, rule.Filters, now)
	if err != nil || !ok {
		return nil, err
	}
	info, err := e.app.Site(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	if trigger.Spike != nil {
		return e.eventSpike(ctx, site.ID, info, filters, *trigger.Spike, now)
	}
	from := now.Add(-time.Duration(trigger.WindowMinutes) * time.Minute)
	rows, err := e.app.IssueWindowCounts(ctx, site.ID, from, now, filters, trigger.Measure, trigger.Above+1, appCandidateLimit)
	if err != nil {
		return nil, err
	}
	spacing := time.Duration(rule.FrequencyMinutes) * time.Minute
	var out []Candidate
	for _, row := range rows {
		id := strconv.FormatInt(row.ID, 10)
		if spacing > 0 {
			last, found, err := e.dedup.LastFired(ctx, alert.ID, "freq:"+id+":")
			if err != nil {
				return nil, err
			}
			if found && now.Sub(last) < spacing {
				continue
			}
		}
		issue := row.AppIssue
		out = append(out, issueCandidate("freq:"+id+":"+strconv.FormatInt(from.Unix(), 10), info, issue, row.Count, float64(trigger.Above), from, now))
	}
	return out, nil
}

// eventSpike applies the v1 spike formula (the last closed UTC hour against
// the average of the same hour over the baseline days) with the rule's numbers.
func (e *RuleEvaluator) eventSpike(ctx context.Context, siteID int32, info AppSite, filters Filters, s Spike, now time.Time) ([]Candidate, error) {
	hour := now.UTC().Truncate(time.Hour).Add(-time.Hour)
	rows, err := e.app.IssueHours(ctx, siteID, hour, s.MinEvents, s.BaselineDays)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, row := range rows {
		average := float64(row.BaselineSum) / float64(s.BaselineDays)
		if row.Events < s.MinEvents || float64(row.Events) < s.Factor*average {
			continue
		}
		issue := row.AppIssue
		issue.Events = row.Events
		c := issueCandidate("spike:"+strconv.FormatInt(issue.ID, 10)+":"+strconv.FormatInt(hour.Unix(), 10), info, issue, row.Events, average*s.Factor, hour, hour.Add(time.Hour))
		c.Notification.App.Baseline = average
		out = append(out, c)
	}
	return e.filterIssues(ctx, siteID, filters, out)
}

func issueCandidate(key string, info AppSite, issue AppIssue, value int64, threshold float64, from, to time.Time) Candidate {
	return Candidate{
		Key: key, GroupKey: "issue:" + strconv.FormatInt(issue.ID, 10), IssueID: issue.ID, AssigneeUserID: issue.AssigneeUserID,
		Notification: Notification{
			Value: float64(value), Threshold: threshold, From: from, To: to,
			App: &AppNotification{
				AppName: info.Name, IssueID: issue.ID, IssueTitle: issue.Title, IssueKind: issue.Kind, Release: issue.Release, Events: issue.Events,
				IssueURL:   fmt.Sprintf("/app/%s/%s/issues/%d", info.OrgSlug, info.SiteSlug, issue.ID),
				IssueLevel: issue.Level, IssueState: issue.Status, Culprit: issue.Culprit,
				FirstSeen: formatIssueTime(issue.FirstSeen), LastSeen: formatIssueTime(issue.LastSeen),
			},
		},
	}
}

func formatIssueTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
