//go:build windows

package replay

import "golang.org/x/sys/windows"

const minimumReplayFreeBytes = uint64(2 * 1024 * 1024 * 1024)

// DiskLow reports whether the filesystem containing dir is too full to start
// another replay. Stat failures fail closed so uploads cannot fill an unknown
// volume.
func DiskLow(dir string) bool {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return true
	}

	var freeBytes uint64
	var totalBytes uint64
	if err := windows.GetDiskFreeSpaceEx(path, &freeBytes, &totalBytes, nil); err != nil || totalBytes == 0 {
		return true
	}
	return freeBytes < (totalBytes+19)/20 || freeBytes < minimumReplayFreeBytes
}
