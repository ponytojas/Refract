package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/ponytojas/Refract/internal/tools"
)

// The TeX seed is a read-only Tectonic cache fragment that ships with the
// tools (packaging/README.md, "TeX seed"): the bundle files the built-in
// templates need. Apply copies it into the person's writable cache, so the
// first compile needs no network. It only ever adds or replaces files that
// belong to the seed: whatever else Tectonic downloaded stays.

// SeedMarker is the file in the cache that says which seed it was filled from.
const SeedMarker = "refract-seed.json"

const (
	seedManifest = "seed.json"
	seedSchema   = 1
	seedMaxFiles = 20000
)

// Seed is a TeX seed found on disk.
type Seed struct {
	Dir          string // the folder holding seed.json and cache/
	ID           string
	BundleURL    string
	BundleDigest string
	files        map[string]string // path under the cache -> sha256

	mu sync.Mutex
}

type seedJSON struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
	Bundle struct {
		URL    string `json:"url"`
		Digest string `json:"digest"`
	} `json:"bundle"`
	Files map[string]string `json:"files"`
}

// SeedResult says what Apply did.
type SeedResult struct {
	Copied  int
	Present int // already in the cache with the right size
	Bytes   int64
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// FindSeed locates the TeX seed: REFRACT_TEX_SEED names its folder, "none"
// turns it off, and by default it is tex-seed/ in the installation's tools
// folder (ignored, with a log line, when that folder isn't private to the
// user). It returns a nil seed and the reason when there is none to use.
func FindSeed(getenv func(string) string) (*Seed, string) {
	dir := getenv("REFRACT_TEX_SEED")
	switch dir {
	case "none":
		return nil, "REFRACT_TEX_SEED=none"
	case "":
		if dir = tools.Resource("tex-seed"); dir == "" {
			return nil, "no TeX seed is installed"
		}
	}
	s, err := loadSeed(dir)
	if err != nil {
		slog.Warn("ignoring the TeX seed", "dir", dir, "err", err)
		return nil, err.Error()
	}
	return s, ""
}

func loadSeed(dir string) (*Seed, error) {
	b, err := os.ReadFile(filepath.Join(dir, seedManifest))
	if err != nil {
		return nil, err
	}
	var m seedJSON
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", seedManifest, err)
	}
	if m.Schema != seedSchema {
		return nil, fmt.Errorf("%s: unknown schema %d", seedManifest, m.Schema)
	}
	if !sha256Hex.MatchString(m.ID) || m.Bundle.URL == "" || !sha256Hex.MatchString(m.Bundle.Digest) {
		return nil, fmt.Errorf("%s: incomplete", seedManifest)
	}
	if len(m.Files) == 0 || len(m.Files) > seedMaxFiles {
		return nil, fmt.Errorf("%s: %d files", seedManifest, len(m.Files))
	}
	files := make(map[string]string, len(m.Files))
	for rel, sum := range m.Files {
		if !sha256Hex.MatchString(sum) {
			return nil, fmt.Errorf("%s: bad checksum for %s", seedManifest, rel)
		}
		sub, ok := strings.CutPrefix(rel, "cache/")
		if !ok || !fs.ValidPath(sub) || sub == "." || sub == SeedMarker || strings.ContainsAny(sub, `\:`) {
			return nil, fmt.Errorf("%s: unexpected path %q", seedManifest, rel)
		}
		files[sub] = sum
	}
	return &Seed{Dir: dir, ID: m.ID, BundleURL: m.Bundle.URL, BundleDigest: m.Bundle.Digest, files: files}, nil
}

// Apply fills cacheDir from the seed. A file already there with the size of
// the seed's is left alone; anything else is copied through a temporary file
// whose SHA-256 is checked, and renamed into place. Nothing is deleted. The
// marker is written last, so a stopped or failed Apply is repeated in full.
// The cache is opened as a root, so a symlink in it can't send a write
// elsewhere; a symlink where a seed file belongs is an error.
func (s *Seed) Apply(cacheDir string) (SeedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(cacheDir)
}

// Ensure applies the seed again when the cache has lost the marker (the person
// emptied the cache). It costs one stat when nothing is wrong.
func (s *Seed) Ensure(cacheDir string) error {
	if s == nil {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, SeedMarker)); err == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Lstat(filepath.Join(cacheDir, SeedMarker)); err == nil {
		return nil
	}
	res, err := s.apply(cacheDir)
	if err == nil {
		slog.Info("TeX seed restored", "cache", cacheDir, "copied", res.Copied)
	}
	return err
}

func (s *Seed) apply(cacheDir string) (SeedResult, error) {
	var res SeedResult
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return res, err
	}
	root, err := os.OpenRoot(cacheDir)
	if err != nil {
		return res, err
	}
	defer root.Close()
	rels := make([]string, 0, len(s.files))
	for rel := range s.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		copied, n, err := s.install(root, rel)
		if err != nil {
			return res, err
		}
		if copied {
			res.Copied++
			res.Bytes += n
		} else {
			res.Present++
		}
	}
	marker, _ := json.Marshal(map[string]string{"seed": s.ID, "bundle_digest": s.BundleDigest})
	if err := writeAtomic(root, SeedMarker, append(marker, '\n')); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Seed) install(root *os.Root, rel string) (copied bool, n int64, err error) {
	src, err := os.Open(filepath.Join(s.Dir, filepath.FromSlash(path.Join("cache", rel))))
	if err != nil {
		return false, 0, err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return false, 0, err
	}
	if !fi.Mode().IsRegular() {
		return false, 0, fmt.Errorf("seed file %s is not a regular file", rel)
	}
	if err := mkdirAll(root, path.Dir(rel)); err != nil {
		return false, 0, err
	}
	switch have, err := root.Lstat(rel); {
	case err == nil && have.Mode().IsRegular() && have.Size() == fi.Size():
		return false, 0, nil
	case err == nil && !have.Mode().IsRegular():
		return false, 0, fmt.Errorf("the cache has %s, which is not a regular file: move it away", rel)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, 0, err
	}
	dir := path.Dir(rel)
	var tmpName string
	var tmp *os.File
	for i := 0; ; i++ {
		tmpName = path.Join(dir, fmt.Sprintf(".seed-%d-%d.tmp", os.Getpid(), i))
		tmp, err = root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || i > 100 {
			return false, 0, err
		}
	}
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(tmp, h), src)
	if err == nil {
		err = tmp.Chmod(0o644) // a seed in a read-only install must not make read-only cache files
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != s.files[rel] {
		err = fmt.Errorf("seed file %s does not match its checksum: the installation is damaged, reinstall Refract", rel)
	}
	if err == nil {
		err = root.Rename(tmpName, rel)
	}
	if err != nil {
		root.Remove(tmpName)
		return false, 0, err
	}
	return true, n, nil
}

// mkdirAll creates dir (slash-separated, relative to root) one folder at a
// time, refusing a symlink or a file where a folder belongs.
func mkdirAll(root *os.Root, dir string) error {
	if dir == "." {
		return nil
	}
	cur := ""
	for _, part := range strings.Split(dir, "/") {
		cur = path.Join(cur, part)
		fi, err := root.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := root.Mkdir(cur, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case err != nil:
			return err
		case !fi.IsDir():
			return fmt.Errorf("the cache has %s, which is not a folder: move it away", cur)
		}
	}
	return nil
}

func writeAtomic(root *os.Root, name string, data []byte) error {
	tmpName := fmt.Sprintf(".%s-%d.tmp", name, os.Getpid())
	f, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmpName, name)
	}
	if err != nil {
		root.Remove(tmpName)
	}
	return err
}
