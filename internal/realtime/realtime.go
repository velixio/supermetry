// Package realtime reports activity from the process-local session store.
package realtime

import (
	"strconv"
	"time"

	"panel/internal/session"
)

const window = 5 * time.Minute

// Snapshot returns active visitors and ranked path IDs seen during the last
// five minutes. Path IDs are rendered as decimal strings for the API layer.
func Snapshot(store session.Store, siteID int32, now time.Time) (visitors int, topPages []string) {
	if store == nil {
		return 0, nil
	}
	visitors, pathIDs := store.ActiveSince(siteID, now.Add(-window))
	topPages = make([]string, len(pathIDs))
	for i, pathID := range pathIDs {
		topPages[i] = strconv.FormatInt(int64(pathID), 10)
	}
	return visitors, topPages
}
