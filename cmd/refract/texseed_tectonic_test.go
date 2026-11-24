package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/compile"
)

// TestSeededCompileDoesNotWaitForTheBundleServer runs the real Tectonic from a
// seeded cache whose bundle server accepts connections and never answers. The
// seed must carry what Tectonic needs to skip its remote check (the last-check
// stamp), or the first compile hangs. It needs a real TeX seed (the installed
// one, or REFRACT_TEX_SEED) and Tectonic; it is skipped without them.
func TestSeededCompileDoesNotWaitForTheBundleServer(t *testing.T) {
	tectonic, err := exec.LookPath("tectonic")
	if err != nil {
		t.Skip("tectonic is not installed")
	}
	real, why := compile.FindSeed(os.Getenv)
	if real == nil {
		t.Skip("no TeX seed to test with: " + why)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	url := "http://" + ln.Addr().String() + "/bundle.tar"

	// The real seed with the loopback address as its bundle URL: same data,
	// the hash file named after the new URL.
	raw, err := os.ReadFile(filepath.Join(real.Dir, "seed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["bundle"].(map[string]any)["url"] = url
	files := m["files"].(map[string]any)
	hashName := strings.NewReplacer(":", ",58,", "/", ",47,").Replace(url)
	var hashSum any
	for k, v := range files {
		if strings.HasPrefix(k, "cache/bundles/hashes/") {
			hashSum = v
			delete(files, k)
		}
	}
	derived := t.TempDir()
	hashes := filepath.Join(derived, "cache", "bundles", "hashes")
	if err := os.MkdirAll(hashes, 0o755); err != nil {
		t.Fatal(err)
	}
	digest := m["bundle"].(map[string]any)["digest"].(string)
	if err := os.WriteFile(filepath.Join(hashes, hashName), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}
	if hashSum == nil {
		t.Skip("the seed has no hash file")
	}
	files["cache/bundles/hashes/"+hashName] = sha256Of(digest)
	if err := os.Symlink(filepath.Join(real.Dir, "cache", "bundles", "data"), filepath.Join(derived, "cache", "bundles", "data")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	out, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(derived, "seed.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}

	got := setupTeXSeed(envOf(map[string]string{"REFRACT_TEX_SEED": derived}), t.TempDir(), "")
	if got.seed == nil {
		t.Fatal("the seed was not applied")
	}
	doc := t.TempDir()
	if err := os.WriteFile(filepath.Join(doc, "a.tex"), []byte("\\documentclass{article}\\begin{document}Hello\\end{document}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without the stamp Tectonic waits about 30 s on the silent server, then
	// falls back to the cache; with it the compile takes a second or two.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tectonic, "-X", "compile", "--bundle", got.bundle, "a.tex")
	cmd.Dir = doc
	cmd.Env = append(os.Environ(), "TECTONIC_CACHE_DIR="+got.cache)
	start := time.Now()
	b, err := cmd.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		t.Fatalf("compile did not finish (%v, %v): %s", err, time.Since(start), b)
	}
	if _, err := os.Stat(filepath.Join(doc, "a.pdf")); err != nil {
		t.Fatal(err)
	}
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
