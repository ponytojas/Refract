package compile

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fontSource keeps the real main file's name: Tectonic uses that name to
// discover BibTeX's .aux and Biber's .run.xml. Redefining TeX's jobname when
// compiling stdin leaves those files invisible to Tectonic's bibliography pass.
// Hard links make a cheap source snapshot; editor saves use atomic renames.
func fontSource(projectDir, mainFile, setup string) (string, error) {
	stage, err := os.MkdirTemp(filepath.Join(projectDir, ".refract"), "compile-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(stage)
		}
	}()
	err = filepath.WalkDir(projectDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(projectDir, p)
		if d.IsDir() {
			if rel != "." && (d.Name() == ".git" || d.Name() == ".refract") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(stage, rel), 0o700)
		}
		if !d.Type().IsRegular() || d.Name() == ".git" {
			return nil
		}
		dest := filepath.Join(stage, rel)
		if filepath.ToSlash(rel) == mainFile {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			// A leading UTF-8 BOM must not move into the middle of the line
			// when setup is prepended. Leave the original file untouched.
			b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
			// No newline is inserted: the original source's line numbers stay
			// unchanged, including diagnostics and SyncTeX.
			prefix := strings.ReplaceAll(setup, "\n", " ") + " "
			return os.WriteFile(dest, append([]byte(prefix), b...), 0o600)
		}
		return linkSource(p, dest)
	})
	if err != nil {
		return "", err
	}
	// Prepared fonts are internal assets, not part of the source tree above.
	fontDir := filepath.Join(projectDir, ".refract", "fonts")
	if _, err := os.Stat(fontDir); err == nil {
		err = filepath.WalkDir(fontDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(projectDir, p)
			dest := filepath.Join(stage, rel)
			if d.IsDir() {
				return os.MkdirAll(dest, 0o700)
			}
			if !d.Type().IsRegular() {
				return nil
			}
			return linkSource(p, dest)
		})
		if err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	ok = true
	return stage, nil
}

func linkSource(source, dest string) error {
	if err := os.Link(source, dest); err == nil {
		return nil
	}
	// A project can include files on another filesystem, or run on a filesystem
	// without hard-link support. Copy those files without loading them into RAM.
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Restore original source paths before deleting the temporary snapshot.
func restoreSyncPaths(file, stage, projectDir string) error {
	in, err := os.Open(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer zr.Close()
	physicalStage, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return err
	}
	physicalProject, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(file), ".synctex-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	zw := gzip.NewWriter(out)
	defer zw.Close()
	scan := bufio.NewScanner(zr)
	scan.Buffer(make([]byte, 64<<10), 1<<20)
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "Input:") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 {
				for _, root := range []string{physicalStage, stage} {
					rel, err := filepath.Rel(root, parts[2])
					if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						line = parts[0] + ":" + parts[1] + ":" + filepath.Join(physicalProject, rel)
						break
					}
				}
			}
		}
		if _, err := fmt.Fprintln(zw, line); err != nil {
			return err
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	zr.Close()
	in.Close()
	return os.Rename(out.Name(), file)
}
