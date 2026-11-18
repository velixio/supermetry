package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"supermetry/internal/obslogs"
	"supermetry/internal/obslogs/search"
	"supermetry/internal/sites"
)

// appLogReader is the part of obslogs.Reader the log tools use.
type appLogReader interface {
	Search(ctx context.Context, siteID int32, q search.Query, from, to time.Time, limit int, cursor *obslogs.Cursor) (obslogs.Page, error)
	TraceLogs(ctx context.Context, siteID int32, traceID []byte, around time.Time, limit int) ([]obslogs.LogRow, error)
}

var _ appLogReader = (*obslogs.Reader)(nil)

var mcpLogToolNames = map[string]bool{"search_logs": true, "get_trace_logs": true}

const (
	mcpSearchLogsMax   = 50
	mcpSearchLogsDef   = 20
	mcpTraceLogsMax    = 100
	mcpTraceLogsDef    = 50
	mcpLogDefaultRange = 24 * time.Hour
)

type mcpLogInput struct {
	Site    int32  `json:"site"`
	Query   string `json:"query,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
	Around  string `json:"around,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func mcpTime(name, raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, invalidMCPParams{detail: name + " must be an RFC 3339 timestamp"}
	}
	return t.UTC(), nil
}

func (h *mcpHandler) runLogTool(ctx context.Context, r *http.Request, name string, arguments json.RawMessage) (any, error) {
	var in mcpLogInput
	if err := decodeMCPParams(arguments, &in); err != nil {
		return nil, invalidMCPParams{err.Error()}
	}
	if in.Site < 1 {
		return nil, invalidMCPParams{detail: "site must be a positive integer"}
	}
	max, def := mcpSearchLogsMax, mcpSearchLogsDef
	if name == "get_trace_logs" {
		max, def = mcpTraceLogsMax, mcpTraceLogsDef
	}
	if in.Limit < 0 || in.Limit > max {
		return nil, invalidMCPParams{detail: "limit must be between 1 and " + strconv.Itoa(max)}
	}
	if in.Limit == 0 {
		in.Limit = def
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
	now := time.Now()
	if h.deps.Now != nil {
		now = h.deps.Now()
	}
	var rows []obslogs.LogRow
	more := false
	switch name {
	case "search_logs":
		q, err := search.Parse(in.Query)
		if err != nil {
			return nil, invalidMCPParams{detail: err.Error()}
		}
		to, err := mcpTime("to", in.To, now)
		if err != nil {
			return nil, err
		}
		from, err := mcpTime("from", in.From, to.Add(-mcpLogDefaultRange))
		if err != nil {
			return nil, err
		}
		page, err := h.deps.AppLogs.Search(ctx, site.ID, q, from, to, in.Limit, nil)
		if err != nil {
			return nil, mcpLogError(err)
		}
		rows, more = page.Rows, page.Next != nil
	default:
		id, err := hex.DecodeString(in.TraceID)
		if err != nil || len(id) != 16 {
			return nil, invalidMCPParams{detail: "trace_id must be 32 hex characters"}
		}
		around, err := mcpTime("around", in.Around, now)
		if err != nil {
			return nil, err
		}
		if rows, err = h.deps.AppLogs.TraceLogs(ctx, site.ID, id, around, in.Limit); err != nil {
			return nil, mcpLogError(err)
		}
	}
	out := make([]obslogs.LogRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, scrubMCPLog(row))
	}
	return map[string]any{"logs": out, "more": more}, nil
}

// scrubMCPLog masks anything address-shaped in a row's text. Intake scrubs
// already; this is the tool-side backstop, like the issue timeline's.
func scrubMCPLog(row obslogs.LogRow) obslogs.LogRow {
	row.Body = mcpEmailLike.ReplaceAllString(row.Body, "[email]")
	if len(row.Attributes) > 0 {
		if raw, err := json.Marshal(row.Attributes); err == nil {
			var clean map[string]any
			if json.Unmarshal([]byte(mcpEmailLike.ReplaceAllString(string(raw), "[email]")), &clean) == nil {
				row.Attributes = clean
			}
		}
	}
	return row
}

func mcpLogError(err error) error {
	var parse *search.ParseError
	if errors.As(err, &parse) || errors.Is(err, obslogs.ErrInvalid) {
		return invalidMCPParams{detail: err.Error()}
	}
	return errors.New("log query failed")
}

func mcpLogTools() []MCPTool {
	site := map[string]any{"type": "integer", "minimum": 1}
	return []MCPTool{
		{Name: "search_logs", Description: "App projects only. Search application logs, newest first. query: space-separated terms; a bare word or \"quoted phrase\" matches the message (3+ characters); severity:error or severity:>=warn; service:, env:, release:, trace:<32 hex>, span:<16 hex>, attr.<key>:<value>; prefix a term with ! to exclude it. Defaults to the last 24 hours; the window is at most 30 days and never reaches past the project's log retention. Returns at most 50 logs; more=true means the window holds more. Never includes user identity.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"site"}, "properties": map[string]any{
				"site": site, "query": map[string]any{"type": "string", "maxLength": search.MaxInput},
				"from": map[string]any{"type": "string", "format": "date-time"}, "to": map[string]any{"type": "string", "format": "date-time"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": mcpSearchLogsMax},
			}}},
		{Name: "get_trace_logs", Description: "App projects only. The logs of one trace, oldest first, within a day of around (default now) and the project's log retention. Returns at most 100 logs. Never includes user identity.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"site", "trace_id"}, "properties": map[string]any{
				"site": site, "trace_id": map[string]any{"type": "string", "pattern": "^[0-9a-fA-F]{32}$"},
				"around": map[string]any{"type": "string", "format": "date-time"},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": mcpTraceLogsMax},
			}}},
	}
}
