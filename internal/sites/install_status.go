package sites

import "time"

const InstallReceivingWindow = 24 * time.Hour

type InstallStatus string

const (
	InstallWaiting   InstallStatus = "waiting"
	InstallReceiving InstallStatus = "receiving"
	InstallQuiet     InstallStatus = "quiet"
)

// InstallStatusAt derives tracker health from the most recent accepted event.
// It is elapsed-time based, so a site's display time zone cannot change it.
func InstallStatusAt(lastSeenAt *time.Time, now time.Time) InstallStatus {
	if lastSeenAt == nil {
		return InstallWaiting
	}
	if !lastSeenAt.Before(now.Add(-InstallReceivingWindow)) {
		return InstallReceiving
	}
	return InstallQuiet
}
