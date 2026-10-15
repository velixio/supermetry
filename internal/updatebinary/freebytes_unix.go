//go:build !windows

package updatebinary

import "golang.org/x/sys/unix"

// OSFreeBytes reports the bytes available to unprivileged writers on the filesystem holding path.
func OSFreeBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
