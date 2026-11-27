package projects

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// StorageBytes measures regular files in a project, including Git history
// and excluding generated build data. It never follows symbolic links.
func (s *Service) StorageBytes(ctx context.Context, id string) (int64, error) {
	var bytes int64
	err := filepath.WalkDir(s.Path(id), func(_ string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".refract" {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			bytes += info.Size()
		}
		return nil
	})
	return bytes, err
}
