//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// osHandle names the type so the route-table test doesn't mistake the
// conversion for http.Handle.
type osHandle = windows.Handle

// lockFile takes an exclusive LockFileEx lock on one byte far past the end of
// f, so the lock never stops other processes from reading the address the
// file holds. held is true when another process has it. Windows drops the
// lock when the process ends.
func lockFile(f *os.File) (held bool, err error) {
	ol := &windows.Overlapped{Offset: 0x7fffffff}
	err = windows.LockFileEx(osHandle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return true, nil
	}
	return false, err
}

// noFollow is 0 on Windows, which has no such flag: openLockFile checks the
// file with Lstat before opening it and compares the two afterwards.
const noFollow = 0
