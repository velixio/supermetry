//go:build windows

package updatebinary

import "errors"

// OSFreeBytes is unavailable on Windows; the binary updater supports Linux only.
func OSFreeBytes(string) (uint64, error) {
	return 0, errors.New("free space is not available on this platform")
}
