//go:build unix

package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// unsafe says why p can't be trusted to hold programs Refract runs: it is
// writable by its group or by others, or owned by someone other than the
// current user or root. Inspection failures are treated as unsafe.
func unsafe(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Sprintf("cannot inspect %s: %v", p, err)
	}
	return unsafeInfo(p, fi)
}

func unsafeInfo(p string, fi os.FileInfo) string {
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Sprintf("%s is writable by group or others", p)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if uid := uint32(os.Geteuid()); st.Uid != uid && st.Uid != 0 {
			return fmt.Sprintf("%s is owned by another user", p)
		}
	}
	return ""
}

// gitTreeUnsafe checks Git's executable helpers and support files as well as
// the main executable. Internal file symlinks are used by Git installations.
func gitTreeUnsafe(dir string) string {
	root := filepath.Join(dir, "git")
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("cannot inspect Git directory %s", root)
	}
	helpers := filepath.Join(root, "libexec", "git-core")
	info, err = os.Lstat(helpers)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("Git helper directory %s is missing or invalid", helpers)
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Sprintf("cannot open Git directory %s: %v", root, err)
	}
	defer rootHandle.Close()
	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if st, ok := fi.Sys().(*syscall.Stat_t); !ok || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) {
				return fmt.Errorf("%s is owned by another user", p)
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			targetInfo, err := rootHandle.Stat(rel)
			if err != nil {
				return fmt.Errorf("cannot resolve Git file %s: %w", p, err)
			}
			if !targetInfo.Mode().IsRegular() {
				return fmt.Errorf("%s does not point to a Git file", p)
			}
			return nil
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file or directory", p)
		}
		if why := unsafeInfo(p, fi); why != "" {
			return fmt.Errorf("%s", why)
		}
		return nil
	})
	if err != nil {
		return err.Error()
	}
	return ""
}

// treeUnsafe checks everything inside the resource folder root (the folder
// itself is checked by trusted): Refract reads these files as they are, so
// each one must be as private as the folder, and none may be a symlink that
// leads out of it.
func treeUnsafe(root string) string {
	why := ""
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			why = fmt.Sprintf("cannot inspect %s: %v", p, walkErr)
			return filepath.SkipAll
		}
		if p == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			why = fmt.Sprintf("%s is a symbolic link", p)
			return filepath.SkipAll
		}
		fi, err := entry.Info()
		if err != nil {
			why = fmt.Sprintf("cannot inspect %s: %v", p, err)
			return filepath.SkipAll
		}
		if why = unsafeInfo(p, fi); why != "" {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && why == "" {
		why = err.Error()
	}
	return why
}
