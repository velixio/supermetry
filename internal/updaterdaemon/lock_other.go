//go:build !linux

package updaterdaemon

import "errors"

type ProcessLock struct{}

func AcquireProcessLock(string) (*ProcessLock, error) {
	return nil, errors.New("the updater helper is supported on Unix only")
}

func (*ProcessLock) Close() error { return nil }
