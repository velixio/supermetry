package api

import (
	"context"

	"supermetry/internal/appquery"
)

// appHealthReader is the part of appquery.Querier the MCP App Health tools use.
type appHealthReader interface {
	Overview(ctx context.Context, scope appquery.Scope, f appquery.Filter) (appquery.Overview, error)
	Issues(ctx context.Context, scope appquery.Scope, in appquery.IssueQuery) (appquery.IssueList, error)
	Issue(ctx context.Context, scope appquery.Scope, id int64, f appquery.Filter) (appquery.IssueDetail, error)
	Compatibility(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.CompatRow, error)
	Releases(ctx context.Context, scope appquery.Scope, f appquery.Filter) ([]appquery.ReleaseRow, error)
}

var _ appHealthReader = (*appquery.Querier)(nil)
