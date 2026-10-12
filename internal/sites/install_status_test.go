package sites

import (
	"testing"
	"time"
)

func TestInstallStatusAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	under := now.Add(-InstallReceivingWindow + time.Nanosecond)
	exactly := now.Add(-InstallReceivingWindow)
	over := now.Add(-InstallReceivingWindow - time.Nanosecond)
	future := now.Add(5 * time.Minute)
	inKathmandu := under.In(time.FixedZone("Asia/Kathmandu", 5*60*60+45*60))

	tests := []struct {
		name     string
		lastSeen *time.Time
		want     InstallStatus
	}{
		{name: "never seen", want: InstallWaiting},
		{name: "just under twenty four hours", lastSeen: &under, want: InstallReceiving},
		{name: "exactly twenty four hours", lastSeen: &exactly, want: InstallReceiving},
		{name: "just over twenty four hours", lastSeen: &over, want: InstallQuiet},
		{name: "future clock skew", lastSeen: &future, want: InstallReceiving},
		{name: "site timezone does not change elapsed time", lastSeen: &inKathmandu, want: InstallReceiving},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := InstallStatusAt(test.lastSeen, now); got != test.want {
				t.Fatalf("InstallStatusAt(%v, %v) = %q, want %q", test.lastSeen, now, got, test.want)
			}
		})
	}
}
