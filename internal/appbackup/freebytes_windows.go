//go:build windows

package appbackup

import "golang.org/x/sys/windows"

// FreeBytes is the space available to unprivileged writers on the filesystem holding path.
func FreeBytes(path string) (uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil); err != nil {
		return 0, err
	}
	return available, nil
}
