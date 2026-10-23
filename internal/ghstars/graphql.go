package ghstars

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	graphqlTimeout   = 30 * time.Second
	graphqlRetries   = 2
	graphqlRetryWait = 500 * time.Millisecond
	maxGraphQLError  = 200
)

// stargazersQuery lists stargazers newest first. databaseId is the numeric user
// id that the REST API calls user.id.
const stargazersQuery = `query($owner:String!,$name:String!,$first:Int!,$after:String){
  repository(owner:$owner,name:$name){
    stargazers(first:$first,after:$after,orderBy:{field:STARRED_AT,direction:DESC}){
      pageInfo{hasNextPage endCursor}
      edges{starredAt node{databaseId login}}
    }
  }
}`

// StargazerPage is one page of the GraphQL stargazer listing, newest first.
type StargazerPage struct {
	Stargazers []Stargazer
	HasNext    bool
	EndCursor  string
}

// StargazerPage fetches one page after the given cursor (empty for the first
// page). GraphQL needs a token, so without one it fails with ErrTokenRequired
// before any request. Timeouts and 5xx answers are retried twice with a short
// backoff.
func (c *Client) StargazerPage(ctx context.Context, repo, after string) (StargazerPage, error) {
	if c.Token == "" {
		return StargazerPage{}, ErrTokenRequired
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return StargazerPage{}, fmt.Errorf("github graphql: repository %q is not owner/name", repo)
	}
	size := c.GraphQLPageSize
	if size <= 0 {
		size = defaultPageSize
	}
	vars := map[string]any{"owner": owner, "name": name, "first": size, "after": nil}
	if after != "" {
		vars["after"] = after
	}
	payload, err := json.Marshal(map[string]any{"query": stargazersQuery, "variables": vars})
	if err != nil {
		return StargazerPage{}, err
	}
	wait := c.retryDelay
	if wait <= 0 {
		wait = graphqlRetryWait
	}
	var lastErr error
	for attempt := 0; attempt <= graphqlRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return StargazerPage{}, ctx.Err()
			case <-time.After(wait * time.Duration(attempt)):
			}
		}
		page, retry, err := c.graphqlOnce(ctx, payload)
		if err == nil {
			return page, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return StargazerPage{}, lastErr
}

// graphqlOnce makes one request and reports whether a failure is worth retrying.
func (c *Client) graphqlOnce(ctx context.Context, payload []byte) (StargazerPage, bool, error) {
	base := c.BaseURL
	if base == "" {
		base = defaultAPIBase
	}
	timeout := c.graphqlTimeout
	if timeout <= 0 {
		timeout = graphqlTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return StargazerPage{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "supermetry")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return StargazerPage{}, true, fmt.Errorf("github graphql request: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return StargazerPage{}, false, fmt.Errorf("%w (POST /graphql)", ErrRateLimited)
	case resp.StatusCode == http.StatusUnauthorized:
		return StargazerPage{}, false, fmt.Errorf("%w (POST /graphql)", ErrBadToken)
	case resp.StatusCode >= 500:
		return StargazerPage{}, true, fmt.Errorf("github graphql request: status %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return StargazerPage{}, false, fmt.Errorf("github graphql request: status %d%s", resp.StatusCode, githubMessage(resp))
	}
	var body struct {
		Data struct {
			Repository *struct {
				Stargazers struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Edges []struct {
						StarredAt time.Time `json:"starredAt"`
						Node      struct {
							DatabaseID int64  `json:"databaseId"`
							Login      string `json:"login"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"stargazers"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body); err != nil {
		return StargazerPage{}, true, fmt.Errorf("decode github graphql response: %w", err)
	}
	if len(body.Errors) > 0 {
		first := body.Errors[0]
		switch first.Type {
		case "NOT_FOUND":
			return StargazerPage{}, false, fmt.Errorf("%w (POST /graphql)", ErrRepoNotFound)
		case "RATE_LIMITED":
			return StargazerPage{}, false, fmt.Errorf("%w (POST /graphql)", ErrRateLimited)
		}
		message := first.Message
		if len(message) > maxGraphQLError {
			message = message[:maxGraphQLError]
		}
		return StargazerPage{}, false, errors.New("github graphql: " + message)
	}
	if body.Data.Repository == nil {
		return StargazerPage{}, false, fmt.Errorf("%w (POST /graphql)", ErrRepoNotFound)
	}
	list := body.Data.Repository.Stargazers
	page := StargazerPage{HasNext: list.PageInfo.HasNextPage, EndCursor: list.PageInfo.EndCursor}
	for _, edge := range list.Edges {
		page.Stargazers = append(page.Stargazers, Stargazer{UserID: edge.Node.DatabaseID, Login: edge.Node.Login, StarredAt: edge.StarredAt})
	}
	return page, false, nil
}
