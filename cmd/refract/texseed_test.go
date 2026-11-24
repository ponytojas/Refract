package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/compile"
)

const seedURL = "https://example.invalid/bundle.tar"

var seedDigest = strings.Repeat("cd", 32)

func writeSeed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"bundles/data/" + seedDigest + ".index":       "index",
		"bundles/data/" + seedDigest + "/article.cls": "class",
	}
	sums := map[string]string{}
	for rel, content := range files {
		p := filepath.Join(dir, "cache", filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(content))
		sums["cache/"+rel] = hex.EncodeToString(sum[:])
	}
	b, _ := json.Marshal(map[string]any{
		"schema": 1, "id": strings.Repeat("e", 64),
		"bundle": map[string]string{"url": seedURL, "digest": seedDigest}, "files": sums,
	})
	os.WriteFile(filepath.Join(dir, "seed.json"), b, 0o644)
	return dir
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestUseSeedMatrix(t *testing.T) {
	seed, _ := compile.FindSeed(envOf(map[string]string{"REFRACT_TEX_SEED": writeSeed(t)}))
	if seed == nil {
		t.Fatal("no seed")
	}
	for _, c := range []struct {
		name            string
		seed            *compile.Seed
		bundle, sandbox string
		want            bool
	}{
		{"default", seed, "", "", true},
		{"same bundle named", seed, seedURL, "", true},
		{"another bundle", seed, "https://example.invalid/other.tar", "", false},
		{"a local bundle folder", seed, "/opt/tex-bundle", "", false},
		{"bwrap sandbox", seed, "", "bwrap", false},
		{"sandbox off", seed, "", "none", true},
		{"no seed", nil, "", "", false},
	} {
		if got, why := useSeed(c.seed, c.bundle, c.sandbox); got != c.want || (!got && why == "") {
			t.Errorf("%s: use = %v (%q), want %v", c.name, got, why, c.want)
		}
	}
}

func TestSetupTeXSeed(t *testing.T) {
	seedDir := writeSeed(t)
	t.Run("default cache is under the data folder", func(t *testing.T) {
		data := t.TempDir()
		got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": seedDir}), data, "")
		want := filepath.Join(data, "tectonic-cache")
		if got.bundle != seedURL || got.cache != want || got.seed == nil {
			t.Fatalf("%+v", got)
		}
		if _, err := os.Stat(filepath.Join(want, compile.SeedMarker)); err != nil {
			t.Error("seed not applied:", err)
		}
	})
	t.Run("TECTONIC_CACHE_DIR wins", func(t *testing.T) {
		cache := filepath.Join(t.TempDir(), "mine")
		got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": seedDir, "TECTONIC_CACHE_DIR": cache}), t.TempDir(), "")
		if got.cache != cache {
			t.Fatalf("%+v", got)
		}
		if _, err := os.Stat(filepath.Join(cache, "bundles", "data", seedDigest, "article.cls")); err != nil {
			t.Error(err)
		}
	})
	t.Run("skipped keeps Tectonic's own settings", func(t *testing.T) {
		data := t.TempDir()
		for name, env := range map[string]map[string]string{
			"none":    {"REFRACT_TEX_SEED": "none", "TECTONIC_CACHE_DIR": "/x"},
			"bundle":  {"REFRACT_TEX_SEED": seedDir, "REFRACT_TECTONIC_BUNDLE": "/opt/bundle", "TECTONIC_CACHE_DIR": "/x"},
			"missing": {"REFRACT_TEX_SEED": filepath.Join(data, "nope"), "TECTONIC_CACHE_DIR": "/x"},
		} {
			got := setupTeXSeed(envOf(env), data, "")
			if got.seed != nil || got.bundle != env["REFRACT_TECTONIC_BUNDLE"] || got.cache != "/x" {
				t.Errorf("%s: %+v", name, got)
			}
		}
		if got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": seedDir}), data, "bwrap"); got.seed != nil || got.bundle != "" || got.cache != "" {
			t.Errorf("bwrap: %+v", got)
		}
		if _, err := os.Stat(filepath.Join(data, "tectonic-cache")); err == nil {
			t.Error("a cache folder was made although the seed was skipped")
		}
	})
	t.Run("a damaged seed is not used", func(t *testing.T) {
		bad := writeSeed(t)
		os.WriteFile(filepath.Join(bad, "cache", "bundles", "data", seedDigest, "article.cls"), []byte("CLASS"), 0o644)
		data := t.TempDir()
		got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": bad}), data, "")
		if got.seed != nil || got.bundle != "" {
			t.Fatalf("%+v", got)
		}
		if _, err := os.Stat(filepath.Join(data, "tectonic-cache", compile.SeedMarker)); err == nil {
			t.Error("marker written")
		}
	})
}

func TestPreviousCache(t *testing.T) {
	home := t.TempDir()
	env := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	if got := previousCache(env, "linux"); got != "" {
		t.Fatalf("no cache: %q", got)
	}
	old := filepath.Join(home, ".cache", "Tectonic")
	os.MkdirAll(filepath.Join(old, "bundles", "data", "abc"), 0o755)
	if got := previousCache(env, "linux"); got != old {
		t.Fatalf("linux: %q, want %q", got, old)
	}
	if got := previousCache(env, "darwin"); got != "" {
		t.Fatalf("darwin has no cache here: %q", got)
	}
	mac := filepath.Join(home, "Library", "Caches", "Tectonic")
	os.MkdirAll(filepath.Join(mac, "bundles", "data", "abc"), 0o755)
	if got := previousCache(env, "darwin"); got != mac {
		t.Fatalf("darwin: %q", got)
	}
}

func TestBackupExclusionOnlyForManagedCache(t *testing.T) {
	var excluded []string
	old := backupExclude
	backupExclude = func(dir string) { excluded = append(excluded, dir) }
	defer func() { backupExclude = old }()
	seedDir := writeSeed(t)

	data := t.TempDir()
	got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": seedDir}), data, "")
	if len(excluded) != 1 || excluded[0] != got.cache {
		t.Fatalf("the managed cache %q should be excluded: %v", got.cache, excluded)
	}

	excluded = nil
	mine := filepath.Join(t.TempDir(), "mine")
	setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": seedDir, "TECTONIC_CACHE_DIR": mine}), t.TempDir(), "")
	if len(excluded) != 0 {
		t.Errorf("TECTONIC_CACHE_DIR was excluded from backups: %v", excluded)
	}
}
