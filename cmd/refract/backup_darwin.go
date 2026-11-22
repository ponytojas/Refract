package main

import (
	"log/slog"

	"golang.org/x/sys/unix"
)

// excludeFromBackup keeps the cache out of Time Machine: it is rebuilt from
// the seed and the package server. It sets the attribute `tmutil addexclusion`
// sets (the property list "com.apple.backupd"), without tmutil's seconds-long
// subprocess. Best effort.
func excludeFromBackup(dir string) {
	value := []byte("bplist00_\x10\x11com.apple.backupd\x08\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x1c")
	if err := unix.Setxattr(dir, "com.apple.metadata:com_apple_backup_excludeItem", value, 0); err != nil {
		slog.Debug("couldn't exclude the TeX cache from Time Machine", "err", err)
	}
}
