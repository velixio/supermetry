package lifecycle

import (
	"testing"
	"time"
)

func TestPurgeAt(t *testing.T) {
	deletedAt := time.Date(2026, time.October, 1, 12, 30, 0, 0, time.FixedZone("test", 5*60*60+30*60))
	want := deletedAt.Add(7 * 24 * time.Hour)
	if got := PurgeAt(deletedAt); !got.Equal(want) {
		t.Fatalf("PurgeAt() = %s, want %s", got, want)
	}
}

func TestRestorable(t *testing.T) {
	deletedAt := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		erased bool
		now    time.Time
		want   bool
	}{
		{name: "one second before purge", now: deletedAt.Add(DeletionGrace - time.Second), want: true},
		{name: "exactly at purge", now: deletedAt.Add(DeletionGrace), want: false},
		{name: "already erased", erased: true, now: deletedAt.Add(DeletionGrace - time.Second), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Restorable(deletedAt, test.erased, test.now); got != test.want {
				t.Fatalf("Restorable() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestReminderDue(t *testing.T) {
	deletedAt := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	reminderAt := deletedAt.Add(DeletionGrace - ReminderLead)
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "one second before reminder window", now: reminderAt.Add(-time.Second), want: false},
		{name: "exactly 72 hours before purge", now: reminderAt, want: true},
		{name: "after reminder window starts", now: reminderAt.Add(time.Second), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ReminderDue(deletedAt, test.now); got != test.want {
				t.Fatalf("ReminderDue() = %t, want %t", got, test.want)
			}
		})
	}
}
