package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"supermetry/internal/appissues"
	"supermetry/internal/sites"
)

// appIssueReader is the part of appissues.Service the issue tools use.
type appIssueReader interface {
	Timeline(ctx context.Context, siteID int32, issueID int64, limit int, before time.Time) ([]appissues.Activity, error)
	Impact(ctx context.Context, siteID int32, issueID int64) (appissues.Impact, error)
}

var _ appIssueReader = (*appissues.Service)(nil)

var mcpIssueToolNames = map[string]bool{"get_issue_timeline": true, "get_issue_impact": true}

const mcpTimelineMax = 100

type mcpIssueInput struct {
	Site  int32 `json:"site"`
	Issue int64 `json:"issue"`
	Limit int   `json:"limit,omitempty"`
}

var mcpEmailLike = regexp.MustCompile(`[^\s"@\\]+@[^\s"@\\]+`)

type mcpActivity struct {
	At    time.Time       `json:"at"`
	Kind  string          `json:"kind"`
	Actor mcpActivityUser `json:"actor"`
	Data  json.RawMessage `json:"data"`
}

type mcpActivityUser struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

func (h *mcpHandler) runIssueTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	var in mcpIssueInput
	if err := decodeMCPParams(arguments, &in); err != nil {
		return nil, invalidMCPParams{err.Error()}
	}
	if in.Site < 1 {
		return nil, invalidMCPParams{detail: "site must be a positive integer"}
	}
	if in.Issue < 1 {
		return nil, invalidMCPParams{detail: "issue must be a positive integer"}
	}
	if in.Limit < 0 || in.Limit > mcpTimelineMax {
		return nil, invalidMCPParams{detail: "limit must be between 1 and 100"}
	}
	if err := requireSite(r, in.Site); err != nil {
		return nil, err
	}
	site, err := mcpSite(r, in.Site)
	if err != nil {
		return nil, err
	}
	if site.Kind != sites.KindApp {
		return nil, invalidMCPParams{detail: "site is not an app; use get_summary and the other analytics tools"}
	}
	switch name {
	case "get_issue_timeline":
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		rows, err := h.deps.AppIssues.Timeline(ctx, site.ID, in.Issue, limit, time.Time{})
		if err != nil {
			return nil, mcpIssueError(err)
		}
		out := make([]mcpActivity, 0, len(rows))
		for _, a := range rows {
			item := mcpActivity{At: a.At, Kind: a.Kind, Actor: mcpActivityUser{Kind: "system"}, Data: scrubMCPData(a.Data)}
			if a.ActorUserID != nil {
				item.Actor.Kind = "user"
				// A display name that looks like an address is withheld.
				if !strings.ContainsAny(a.ActorName, "@<>") {
					item.Actor.Name = a.ActorName
				}
			}
			out = append(out, item)
		}
		return map[string]any{"activity": out}, nil
	default:
		impact, err := h.deps.AppIssues.Impact(ctx, site.ID, in.Issue)
		if err != nil {
			return nil, mcpIssueError(err)
		}
		return impact, nil
	}
}

// scrubMCPData masks anything address-shaped in an activity row's details.
func scrubMCPData(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(mcpEmailLike.ReplaceAllString(string(raw), "[email]"))
}

func mcpIssueError(err error) error {
	if errors.Is(err, appissues.ErrNotFound) {
		return invalidMCPParams{detail: "issue not found"}
	}
	return errors.New("app issue query failed")
}

func mcpIssueTools() []MCPTool {
	props := func(extra map[string]any) map[string]any {
		p := map[string]any{"site": map[string]any{"type": "integer", "minimum": 1}, "issue": map[string]any{"type": "integer", "minimum": 1}}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	schema := func(p map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"site", "issue"}, "properties": p}
	}
	return []MCPTool{
		{Name: "get_issue_timeline", Description: "App projects only. An issue's activity timeline, newest first: status changes, assignment, priority, snoozes, merges, regressions. Actors are shown by display name only, never by email.",
			InputSchema: schema(props(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": mcpTimelineMax}}))},
		{Name: "get_issue_impact", Description: "App projects only. How many users, sessions and events an issue affected over the last 30 days, and the releases and platforms they came from. Counts only.",
			InputSchema: schema(props(nil))},
	}
}
