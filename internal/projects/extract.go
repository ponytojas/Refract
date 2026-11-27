package projects

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ErrUnsafePath rejects a whole import that tries to write outside the project (zip-slip).
var ErrUnsafePath = errors.New("unsafe path in import")

// ErrTooLarge rejects an import over Limits.
var ErrTooLarge = errors.New("import too large")

var drivePrefix = regexp.MustCompile(`^[A-Za-z]:`)

// cleanName validates an import entry name. skip=true drops the entry
// (OS junk, git internals, build output); report says whether to tell the user.
func cleanName(name string) (clean string, skip, report bool, err error) {
	if name == "" {
		return "", true, false, nil
	}
	if strings.HasPrefix(name, "/") || drivePrefix.MatchString(name) {
		return "", false, false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	parts := strings.Split(name, "/")
	if slices.Contains(parts, "..") {
		return "", false, false, fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	if isJunk(name) {
		return "", true, false, nil
	}
	// A planted .git could carry hooks or config that make git run code.
	if slices.Contains(parts, ".git") || slices.Contains(parts, ".refract") {
		return "", true, true, nil
	}
	clean = path.Clean(name)
	if clean == "." {
		return "", true, false, nil
	}
	return clean, false, false, nil
}

// extract writes src into dir (which must exist) within limits. os.Root
// confines every write to dir, symlinks included.
func extract(dir string, src Source, lim Limits) (skipped []string, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files, budget := 0, lim.MaxBytes
	err = src(func(name string, r io.Reader) error {
		clean, skip, report, err := cleanName(name)
		if err != nil {
			return err
		}
		if skip {
			if report {
				skipped = append(skipped, name)
			}
			return nil
		}
		if files++; files > lim.MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrTooLarge, lim.MaxFiles)
		}
		if d := path.Dir(clean); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		f, err := root.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		// Count real bytes: zip headers can lie about uncompressed size.
		n, err := io.Copy(f, io.LimitReader(r, budget+1))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", clean, err)
		}
		if budget -= n; budget < 0 {
			return fmt.Errorf("%w: more than %d MB uncompressed", ErrTooLarge, lim.MaxBytes>>20)
		}
		return nil
	})
	return skipped, err
}

const saveTempPrefix = ".refract-tmp-"

// walkFiles calls fn for each regular project file, skipping Git metadata,
// build output, and temporary files that a save has yet to rename.
func walkFiles(dir string, fn func(rel string, info fs.FileInfo) error) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".refract") && p != dir {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if suffix, ok := strings.CutPrefix(d.Name(), saveTempPrefix); ok && len(suffix) == 12 {
			if _, err := hex.DecodeString(suffix); err == nil {
				return nil
			}
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), info)
	})
}
