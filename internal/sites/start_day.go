package sites

import "time"

// StartDay returns the civil date, as a UTC midnight, on which a site created at
// createdAt began, in the site's own time zone. The zone name is resolved by Go,
// not PostgreSQL, because a database built without every tz alias (for example
// Asia/Calcutta) rejects names the application accepts. An unknown name falls
// back to UTC.
func StartDay(createdAt time.Time, timezone string) time.Time {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	local := createdAt.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}
