package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testBundleURL = "https://example.invalid/bundle.tar"

var testDigest = strings.Repeat("ab", 32)

// newSeed writes a seed folder with the given files (cache-relative path ->
// content) and returns it as FindSeed would.
func newSeed(t *testing.T, id string, files map[string]string) *Seed {
	t.Helper()
	dir := t.TempDir()
	sums := map[string]string{}
	for rel, content := range files {
		p := filepath.Join(dir, "cache", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		sums["cache/"+rel] = hex.EncodeToString(sum[:])
	}
	m := map[string]any{
		"schema": 1, "id": strings.Repeat(id, 64)[:64], "tectonic": "0.17.0",
		"bundle": map[string]string{"url": testBundleURL, "digest": testDigest},
		"files":  sums,
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, why := FindSeed(func(k string) string {
		if k == "REFRACT_TEX_SEED" {
			return dir
		}
		return ""
	})
	if s == nil {
		t.Fatalf("seed not loaded: %s", why)
	}
	return s
}

func baseFiles() map[string]string {
	return map[string]string{
		"bundles/hashes/https,58,,47,,47,example.invalid,47,bundle.tar": testDigest,
		"bundles/data/" + testDigest + ".index":                         "index",
		"bundles/data/" + testDigest + "/article.cls":                   "class",
		"bundles/data/" + testDigest + "/amsmath.sty":                   "math",
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSeedApplyToEmptyCache(t *testing.T) {
	s := newSeed(t, "a", baseFiles())
	cache := filepath.Join(t.TempDir(), "deep", "cache")
	res, err := s.Apply(cache)
	if err != nil || res.Copied != 4 || res.Present != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, filepath.Join(cache, "bundles", "data", testDigest, "article.cls")); got != "class" {
		t.Errorf("content %q", got)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(read(t, filepath.Join(cache, SeedMarker))), &m); err != nil || m["seed"] != s.ID || m["bundle_digest"] != testDigest {
		t.Errorf("marker %v %v", m, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(cache, "bundles", "data", testDigest, "article.cls"))
		if fi.Mode().Perm() != 0o644 {
			t.Errorf("file mode %v", fi.Mode())
		}
		fi, _ = os.Stat(filepath.Join(cache, "bundles", "data"))
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("dir mode %v", fi.Mode())
		}
	}
	entries, _ := os.ReadDir(filepath.Join(cache, "bundles", "data", testDigest))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}

func TestSeedApplyIsIdempotentAndEnsureRestores(t *testing.T) {
	s := newSeed(t, "a", baseFiles())
	cache := t.TempDir()
	if _, err := s.Apply(cache); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(cache, "bundles", "data", testDigest, "article.cls")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(f, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := s.Apply(cache)
	if err != nil || res.Copied != 0 || res.Present != 4 {
		t.Fatalf("second apply %+v %v", res, err)
	}
	if fi, _ := os.Stat(f); !fi.ModTime().Equal(old) {
		t.Error("an up-to-date file was rewritten")
	}
	// With the marker present Ensure does nothing, even if a file is gone.
	os.Remove(f)
	if err := s.Ensure(cache); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Ensure re-applied despite the marker")
	}
	// The cache was emptied: the marker is gone and the seed comes back.
	os.RemoveAll(cache)
	if err := s.Ensure(cache); err != nil {
		t.Fatal(err)
	}
	if read(t, f) != "class" {
		t.Error("Ensure did not restore the seed")
	}
	var nilSeed *Seed
	if err := nilSeed.Ensure(cache); err != nil {
		t.Fatal(err)
	}
}

func TestSeedRefreshKeepsDownloadsAndUnchangedFiles(t *testing.T) {
	cache := t.TempDir()
	v1 := newSeed(t, "a", baseFiles())
	if _, err := v1.Apply(cache); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(cache, "bundles", "data", testDigest)
	downloaded := filepath.Join(data, "forest.sty")
	if err := os.WriteFile(downloaded, []byte("downloaded by the user"), 0o644); err != nil {
		t.Fatal(err)
	}
	otherBundle := filepath.Join(cache, "bundles", "data", "other", "x.sty")
	os.MkdirAll(filepath.Dir(otherBundle), 0o755)
	os.WriteFile(otherBundle, []byte("old bundle"), 0o644)
	keep := filepath.Join(data, "article.cls")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(keep, old, old)

	files := baseFiles()
	files["bundles/data/"+testDigest+"/amsmath.sty"] = "math, revised" // changed size
	files["bundles/data/"+testDigest+"/tikz.sty"] = "new"
	v2 := newSeed(t, "b", files)
	res, err := v2.Apply(cache)
	if err != nil || res.Copied != 2 || res.Present != 3 {
		t.Fatalf("refresh %+v %v", res, err)
	}
	if read(t, downloaded) != "downloaded by the user" || read(t, otherBundle) != "old bundle" {
		t.Error("a file the seed does not own was touched")
	}
	if fi, _ := os.Stat(keep); !fi.ModTime().Equal(old) {
		t.Error("an unchanged file was rewritten")
	}
	if read(t, filepath.Join(data, "amsmath.sty")) != "math, revised" || read(t, filepath.Join(data, "tikz.sty")) != "new" {
		t.Error("changed files were not refreshed")
	}
	if !strings.Contains(read(t, filepath.Join(cache, SeedMarker)), v2.ID) {
		t.Error("marker still names the old seed")
	}
}

func TestSeedReadOnlySource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	s := newSeed(t, "a", baseFiles())
	filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			t.Cleanup(func() { os.Chmod(p, 0o755) })
		}
		return nil
	})
	filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			os.Chmod(p, 0o444)
		}
		return nil
	})
	var dirs []string
	filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Chmod(dirs[i], 0o555)
	}
	cache := t.TempDir()
	if _, err := s.Apply(cache); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(cache, "bundles", "data", testDigest, "article.cls")
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o644 {
		t.Errorf("a read-only seed made %v cache files", fi.Mode().Perm())
	}
	if _, err := s.Apply(cache); err != nil {
		t.Fatal(err)
	}
}

func TestSeedChecksumMismatchAborts(t *testing.T) {
	s := newSeed(t, "a", baseFiles())
	bad := filepath.Join(s.Dir, "cache", "bundles", "data", testDigest, "article.cls")
	if err := os.WriteFile(bad, []byte("CLASS"), 0o644); err != nil { // same size, other content
		t.Fatal(err)
	}
	cache := t.TempDir()
	_, err := s.Apply(cache)
	if err == nil || !strings.Contains(err.Error(), "reinstall") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, SeedMarker)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("marker written after a failed seed")
	}
	if _, err := os.Stat(filepath.Join(cache, "bundles", "data", testDigest, "article.cls")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a file that failed its checksum was installed")
	}
	entries, _ := os.ReadDir(filepath.Join(cache, "bundles", "data", testDigest))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left: %s", e.Name())
		}
	}
}

func TestSeedRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	s := newSeed(t, "a", baseFiles())
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	os.WriteFile(secret, []byte("keep me"), 0o600)

	t.Run("file", func(t *testing.T) {
		cache := t.TempDir()
		dir := filepath.Join(cache, "bundles", "data", testDigest)
		os.MkdirAll(dir, 0o755)
		os.Symlink(secret, filepath.Join(dir, "article.cls"))
		if _, err := s.Apply(cache); err == nil {
			t.Fatal("a symlinked cache file was accepted")
		}
		if read(t, secret) != "keep me" {
			t.Error("wrote through a symlink")
		}
		if _, err := os.Stat(filepath.Join(cache, SeedMarker)); err == nil {
			t.Error("marker written")
		}
	})
	t.Run("folder", func(t *testing.T) {
		cache := t.TempDir()
		os.Symlink(outside, filepath.Join(cache, "bundles"))
		if _, err := s.Apply(cache); err == nil {
			t.Fatal("a symlinked cache folder was accepted")
		}
		entries, _ := os.ReadDir(outside)
		if len(entries) != 1 {
			t.Errorf("wrote through a symlinked folder: %v", entries)
		}
	})
}

func TestSeedLeavesTheDataFolderAlone(t *testing.T) {
	data := t.TempDir()
	type snap struct {
		sum   [32]byte
		mtime time.Time
	}
	before := map[string]snap{}
	old := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	for rel, content := range map[string]string{"refract.db": "db", "secret.key": "key\n", "projects/p1/main.tex": "\\documentclass{article}"} {
		p := filepath.Join(data, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o600)
		os.Chtimes(p, old, old)
		before[p] = snap{sha256.Sum256([]byte(content)), old}
	}
	cache := filepath.Join(data, "tectonic-cache")
	if _, err := newSeed(t, "a", baseFiles()).Apply(cache); err != nil {
		t.Fatal(err)
	}
	files := baseFiles()
	files["bundles/data/"+testDigest+"/article.cls"] = "class, newer"
	if _, err := newSeed(t, "b", files).Apply(cache); err != nil {
		t.Fatal(err)
	}
	for p, b := range before {
		fi, err := os.Stat(p)
		if err != nil || !fi.ModTime().Equal(b.mtime) || sha256.Sum256([]byte(read(t, p))) != b.sum {
			t.Errorf("%s changed (%v)", p, err)
		}
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 4 {
		t.Errorf("data folder has %d entries, want 4 (db, key, projects, cache)", len(entries))
	}
}

func TestFindSeedSelection(t *testing.T) {
	good := newSeed(t, "a", baseFiles())
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "REFRACT_TEX_SEED" {
				return v
			}
			return ""
		}
	}
	if s, why := FindSeed(env("none")); s != nil || why == "" {
		t.Errorf("none: %v %q", s, why)
	}
	if s, _ := FindSeed(env(good.Dir)); s == nil || s.BundleURL != testBundleURL || s.BundleDigest != testDigest {
		t.Errorf("explicit folder: %+v", s)
	}
	if s, why := FindSeed(env(filepath.Join(t.TempDir(), "missing"))); s != nil || why == "" {
		t.Errorf("missing folder: %v %q", s, why)
	}
	// The default is the installation's tex-seed folder: a test binary has none.
	if s, why := FindSeed(env("")); s != nil || why == "" {
		t.Errorf("default: %v %q", s, why)
	}
	for name, edit := range map[string]func(m map[string]any){
		"schema":      func(m map[string]any) { m["schema"] = 2 },
		"path escape": func(m map[string]any) { m["files"] = map[string]string{"cache/../../x": strings.Repeat("a", 64)} },
		"outside":     func(m map[string]any) { m["files"] = map[string]string{"other/x": strings.Repeat("a", 64)} },
		"no bundle":   func(m map[string]any) { m["bundle"] = map[string]string{} },
	} {
		var m map[string]any
		b, _ := os.ReadFile(filepath.Join(good.Dir, "seed.json"))
		json.Unmarshal(b, &m)
		edit(m)
		dir := t.TempDir()
		b, _ = json.Marshal(m)
		os.WriteFile(filepath.Join(dir, "seed.json"), b, 0o644)
		if s, _ := FindSeed(env(dir)); s != nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSeedReplacesDamagedSameSizeFileUnlessMarked(t *testing.T) {
	cache := t.TempDir()
	v1 := newSeed(t, "a", baseFiles())
	if _, err := v1.Apply(cache); err != nil {
		t.Fatal(err)
	}
	cls := filepath.Join(cache, "bundles", "data", testDigest, "article.cls")
	if err := os.WriteFile(cls, []byte("CLASS"), 0o644); err != nil { // same size, other bytes
		t.Fatal(err)
	}
	// The marker names this seed: sizes are trusted, nothing is read.
	if res, err := v1.Apply(cache); err != nil || res.Copied != 0 {
		t.Fatalf("marked cache %+v %v", res, err)
	}
	// An update (another seed id) checks the contents and repairs the file.
	v2 := newSeed(t, "b", baseFiles())
	if res, err := v2.Apply(cache); err != nil || res.Copied != 1 || res.Present != 3 {
		t.Fatalf("update %+v %v", res, err)
	}
	if read(t, cls) != "class" {
		t.Error("the damaged file was kept")
	}
	// So does a cache that lost its marker.
	os.WriteFile(cls, []byte("CLASS"), 0o644)
	os.Remove(filepath.Join(cache, SeedMarker))
	if err := v2.Ensure(cache); err != nil {
		t.Fatal(err)
	}
	if read(t, cls) != "class" {
		t.Error("Ensure kept the damaged file")
	}
}
