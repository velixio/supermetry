// Package lifecycle defines shared deletion-grace rules.
package lifecycle

import "time"

const (
	DeletionGrace = 7 * 24 * time.Hour
	ReminderLead  = 72 * time.Hour
)

func PurgeAt(deletedAt time.Time) time.Time {
	return deletedAt.Add(DeletionGrace)
}

func Restorable(deletedAt time.Time, erased bool, now time.Time) bool {
	return !erased && now.Before(PurgeAt(deletedAt))
}

func ReminderDue(deletedAt, now time.Time) bool {
	return !now.Before(PurgeAt(deletedAt).Add(-ReminderLead))
}
