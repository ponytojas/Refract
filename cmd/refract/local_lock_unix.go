//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive flock on f. held is true when
// another open file description holds it. The kernel drops the lock when the
// process ends, however it ends.
func lockFile(f *os.File) (held bool, err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, syscall.EINTR):
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true, nil
		default:
			return false, err
		}
	}
}

// noFollow makes opening the lock file fail on a symbolic link.
const noFollow = syscall.O_NOFOLLOW
