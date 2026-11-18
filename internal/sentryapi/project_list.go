package sentryapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

// projectList is a "projects" request field. Sentry clients send project
// slugs as strings, but sentry-cli sends numeric project ids as JSON numbers
// when SENTRY_PROJECT is a number, so both forms are accepted and every entry
// is kept as its string form for resolveProjects.
type projectList []string

func (p *projectList) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*p = nil
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(projectList, 0, len(raw))
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			out = append(out, s)
			continue
		}
		var n float64
		if err := json.Unmarshal(item, &n); err != nil || n != math.Trunc(n) || math.Abs(n) > 1<<53 {
			return errors.New("projects must be strings or integers")
		}
		out = append(out, strconv.FormatInt(int64(n), 10))
	}
	*p = out
	return nil
}
