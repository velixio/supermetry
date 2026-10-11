//go:build !windows

package replay

import (
	"errors"
	"syscall"
	"testing"
)

func TestDiskLowThresholds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		blocks uint64
		free   uint64
		bsize  int64
		err    error
		want   bool
	}{
		{name: "healthy", blocks: 100, free: 20, bsize: 1 << 30, want: false},
		{name: "below five percent", blocks: 100, free: 4, bsize: 1 << 30, want: true},
		{name: "exactly five percent", blocks: 100, free: 5, bsize: 1 << 30, want: false},
		{name: "below two gibibytes", blocks: 100, free: 19, bsize: 100 << 20, want: true},
		{name: "exactly two gibibytes", blocks: 20, free: 2, bsize: 1 << 30, want: false},
		{name: "stat failure fails closed", err: errors.New("statfs failed"), want: true},
		{name: "zero-sized filesystem fails closed", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := diskLow("/replay", func(path string, stat *syscall.Statfs_t) error {
				if path != "/replay" {
					t.Fatalf("statfs path = %q", path)
				}
				stat.Blocks = tt.blocks
				stat.Bavail = tt.free
				stat.Bsize = tt.bsize
				return tt.err
			})
			if got != tt.want {
				t.Fatalf("diskLow() = %t, want %t", got, tt.want)
			}
		})
	}
}
