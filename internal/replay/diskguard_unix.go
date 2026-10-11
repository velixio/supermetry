//go:build !windows

package replay

import "syscall"

const minimumReplayFreeBytes = uint64(2 * 1024 * 1024 * 1024)

type statfsFunc func(string, *syscall.Statfs_t) error

// DiskLow reports whether the filesystem containing dir is too full to start
// another replay. Stat failures fail closed so uploads cannot fill an unknown
// volume.
func DiskLow(dir string) bool {
	return diskLow(dir, syscall.Statfs)
}

func diskLow(dir string, statfs statfsFunc) bool {
	var stat syscall.Statfs_t
	if err := statfs(dir, &stat); err != nil || stat.Blocks == 0 || stat.Bsize <= 0 {
		return true
	}
	belowFivePercent := stat.Bavail < (stat.Blocks+19)/20
	blocksForMinimum := (minimumReplayFreeBytes + uint64(stat.Bsize) - 1) / uint64(stat.Bsize)
	return belowFivePercent || stat.Bavail < blocksForMinimum
}
