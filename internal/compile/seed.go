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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/tools"
)

// The TeX seed is a read-only Tectonic cache fragment that ships with the
// tools (packaging/README.md, "TeX seed"): the bundle files the built-in
// templates need. Apply copies it into the person's writable cache, so the
// first compile needs no network. It only ever adds or replaces files that
// belong to the seed: whatever else Tectonic downloaded stays.

// SeedMarker is the file in the cache that says which seed it was filled from.
const SeedMarker = "refract-seed.json"

// seedLockMaxAge is how old the last-check stamp may be before Ensure writes
// a new one. Tectonic 0.17 asks the bundle server for the bundle's hash again
// once the stamp is a few days old (more than one day, less than eight).
const seedLockMaxAge = 24 * time.Hour

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
	Present int // already in the cache (right size; right SHA-256 unless the marker named this seed)
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

// Apply fills cacheDir from the seed. When the marker already names this seed,
// a file there with the size of the seed's is left alone; otherwise (a first
// run, an update, a cache that lost its marker) a file already there is kept
// only when its SHA-256 matches, so a damaged copy is not trusted forever.
// Anything else is copied through a temporary file whose SHA-256 is checked,
// and renamed into place. Nothing is deleted. The marker is written last, so a
// stopped or failed Apply is repeated in full.
// The cache is opened as a root, so a symlink in it can't send a write
// elsewhere; a symlink where a seed file belongs is an error.
func (s *Seed) Apply(cacheDir string) (SeedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(cacheDir)
}

// Ensure applies the seed again when the cache has lost the marker (the person
// emptied the cache) or part of the bundle metadata Tectonic needs (a partial
// cleanup), or when the last-check stamp is old. It costs a few stats when
// nothing is wrong.
func (s *Seed) Ensure(cacheDir string) error {
	if s == nil {
		return nil
	}
	if s.intact(cacheDir) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intact(cacheDir) {
		return nil
	}
	res, err := s.apply(cacheDir)
	if err == nil {
		slog.Info("TeX seed restored", "cache", cacheDir, "copied", res.Copied)
	}
	return err
}

// hashRel and lockRel are Tectonic's files for the bundle URL under the
// cache: the bundle's hash, and the time it last asked the server whether the
// hash changed (decimal Unix seconds). Without a recent stamp Tectonic asks
// before using the local copy, which hangs when the server stalls.
func (s *Seed) hashRel() string {
	return "bundles/hashes/" + strings.NewReplacer(":", ",58,", "/", ",47,").Replace(s.BundleURL)
}

func (s *Seed) lockRel() string {
	h := s.hashRel()
	if i := strings.LastIndex(h, "."); i > strings.LastIndex(h, "/") {
		h = h[:i]
	}
	return h + ".lock"
}

func (s *Seed) indexRel() string { return "bundles/data/" + s.BundleDigest + ".index" }

// intact reports whether the marker, the hash, the index and a recent stamp
// are all in the cache.
func (s *Seed) intact(cacheDir string) bool {
	for _, rel := range []string{SeedMarker, s.hashRel(), s.indexRel()} {
		if fi, err := os.Lstat(filepath.Join(cacheDir, filepath.FromSlash(rel))); err != nil || !fi.Mode().IsRegular() {
			return false
		}
	}
	b, err := os.ReadFile(filepath.Join(cacheDir, filepath.FromSlash(s.lockRel())))
	if err != nil {
		return false
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(sec, 0))
	return age < seedLockMaxAge && age > -time.Hour
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
	verify := !s.markedBy(root)
	rels := make([]string, 0, len(s.files))
	for rel := range s.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		copied, n, err := s.install(root, rel, verify)
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
	if err := writeAtomic(root, s.lockRel(), []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n")); err != nil {
		return res, err
	}
	marker, _ := json.Marshal(map[string]string{"seed": s.ID, "bundle_digest": s.BundleDigest})
	if err := writeAtomic(root, SeedMarker, append(marker, '\n')); err != nil {
		return res, err
	}
	return res, nil
}

// markedBy reports whether the cache's marker names this seed.
func (s *Seed) markedBy(root *os.Root) bool {
	f, err := root.Open(SeedMarker)
	if err != nil {
		return false
	}
	defer f.Close()
	var m struct {
		Seed string `json:"seed"`
	}
	return json.NewDecoder(io.LimitReader(f, 4096)).Decode(&m) == nil && m.Seed == s.ID
}

// matches reports whether the cache file rel has the seed's SHA-256.
func (s *Seed) matches(root *os.Root, rel string) bool {
	f, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == s.files[rel]
}

func (s *Seed) install(root *os.Root, rel string, verify bool) (copied bool, n int64, err error) {
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
	case err == nil && have.Mode().IsRegular() && have.Size() == fi.Size() && (!verify || s.matches(root, rel)):
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
	tmpName := path.Join(path.Dir(name), fmt.Sprintf(".%s-%d.tmp", path.Base(name), os.Getpid()))
	if err := mkdirAll(root, path.Dir(name)); err != nil {
		return err
	}
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
