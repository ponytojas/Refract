package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ponytojas/Refract/internal/compile"
)

// texSetup is what Tectonic runs with: the bundle to pass (empty = Tectonic's
// default), the cache folder (empty = Tectonic's default) and the seed that
// filled it.
type texSetup struct {
	bundle, cache string
	seed          *compile.Seed
}

// useSeed decides whether the TeX seed applies. It doesn't when another
// bundle was asked for (the seed holds files of one bundle) or when the bwrap
// sandbox is on (it needs a local bundle folder: the seed is a cache).
func useSeed(seed *compile.Seed, envBundle, sandbox string) (bool, string) {
	switch {
	case seed == nil:
		return false, "no seed"
	case sandbox == "bwrap":
		return false, "REFRACT_SANDBOX=bwrap runs Tectonic with a local bundle folder"
	case envBundle != "" && envBundle != seed.BundleURL:
		return false, "REFRACT_TECTONIC_BUNDLE names another bundle than the seed's"
	}
	return true, ""
}

// setupTeXSeed (refract local only) puts the TeX seed into the cache, so the
// first compile works offline, and tells Tectonic to use its bundle. The
// cache is TECTONIC_CACHE_DIR or <dataDir>/tectonic-cache. Without a usable
// seed it changes nothing: Tectonic keeps its default bundle and cache.
func setupTeXSeed(getenv func(string) string, dataDir, sandbox string) texSetup {
	envBundle, envCache := getenv("REFRACT_TECTONIC_BUNDLE"), getenv("TECTONIC_CACHE_DIR")
	plain := texSetup{bundle: envBundle, cache: envCache}
	seed, why := compile.FindSeed(getenv)
	if ok, reason := useSeed(seed, envBundle, sandbox); !ok {
		if seed == nil {
			reason = why
		}
		slog.Info("TeX seed not used; Tectonic downloads what documents need", "why", reason)
		return plain
	}
	cache := envCache
	if cache == "" {
		cache = filepath.Join(dataDir, "tectonic-cache")
		if old := previousCache(getenv, runtime.GOOS); old != "" {
			slog.Info("Tectonic's own cache from before the TeX seed is left untouched and no longer used: the seed's bundle differs from Tectonic's default, so its packages can't be shared; set TECTONIC_CACHE_DIR and REFRACT_TECTONIC_BUNDLE to keep using it", "old", old, "new", cache)
		}
	}
	cache, err := filepath.Abs(cache)
	if err != nil {
		slog.Warn("TeX seed not used", "err", err)
		return plain
	}
	start := time.Now()
	res, err := seed.Apply(cache)
	if err != nil {
		slog.Error("TeX seed not applied; Tectonic downloads what documents need", "seed", seed.Dir, "cache", cache, "err", err)
		return plain
	}
	slog.Info("TeX seed ready", "seed", seed.Dir, "cache", cache, "copied", res.Copied, "present", res.Present, "mb", res.Bytes>>20, "ms", time.Since(start).Milliseconds())
	if res.Copied > 0 {
		excludeFromBackup(cache)
	}
	return texSetup{bundle: seed.BundleURL, cache: cache, seed: seed}
}

// previousCache is Tectonic's default cache folder when it holds downloaded
// bundle files (what `refract local` used before the TeX seed), or "". Those
// files belong to Tectonic's default bundle, which isn't the seed's, so they
// can't be reused: the folder is only reported, never changed or deleted.
func previousCache(getenv func(string) string, goos string) string {
	var dir string
	switch goos {
	case "darwin":
		if h := getenv("HOME"); h != "" {
			dir = filepath.Join(h, "Library", "Caches", "Tectonic")
		}
	case "windows":
		if l := getenv("LOCALAPPDATA"); l != "" {
			dir = filepath.Join(l, "TectonicProject", "Tectonic", "cache")
		}
	default:
		if x := getenv("XDG_CACHE_HOME"); x != "" {
			dir = filepath.Join(x, "Tectonic")
		} else if h := getenv("HOME"); h != "" {
			dir = filepath.Join(h, ".cache", "Tectonic")
		}
	}
	if dir == "" {
		return ""
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "bundles", "data")); err != nil || len(entries) == 0 {
		return ""
	}
	return dir
}
