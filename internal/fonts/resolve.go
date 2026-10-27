package fonts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Resolver caches static Google Fonts files. Only family names are sent to
// Google; documents and their contents stay on the Refract server.
type Resolver struct {
	CacheDir string
	HTTP     *http.Client
	Offline  bool // use uploaded, bundled and cached fonts without contacting Google
	locks    sync.Map
	missing  sync.Map
}

func NewResolver(cacheDir string) *Resolver {
	return &Resolver{CacheDir: cacheDir, HTTP: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type family struct {
	Regular    string `json:"regular"`
	Bold       string `json:"bold,omitempty"`
	Italic     string `json:"italic,omitempty"`
	BoldItalic string `json:"bold_italic,omitempty"`
}

func (f family) files() []string { return []string{f.Regular, f.Bold, f.Italic, f.BoldItalic} }

// These fonts are supplied by Tectonic's TeX bundle, including offline builds
// after its usual first-use download. File names bypass OS font discovery.
func bundled(name string) (family, bool) {
	base := ""
	switch strings.ToLower(name) {
	case "latin modern roman":
		base = "lmroman10"
	case "latin modern sans":
		return family{"lmsans10-regular.otf", "lmsans10-bold.otf", "lmsans10-oblique.otf", "lmsans10-boldoblique.otf"}, true
	case "latin modern mono":
		return family{"lmmono10-regular.otf", "lmmonolt10-bold.otf", "lmmono10-italic.otf", "lmmonolt10-boldoblique.otf"}, true
	case "tex gyre chorus":
		return family{Regular: "texgyrechorus-mediumitalic.otf"}, true
	default:
		for _, n := range []string{"adventor", "bonum", "chorus", "cursor", "heros", "pagella", "schola", "termes"} {
			if strings.EqualFold(name, "TeX Gyre "+n) {
				base = "texgyre" + n
				break
			}
		}
	}
	if base == "" {
		return family{}, false
	}
	return family{base + "-regular.otf", base + "-bold.otf", base + "-italic.otf", base + "-bolditalic.otf"}, true
}

var (
	faceBlock = regexp.MustCompile(`(?s)@font-face\s*\{([^}]+)\}`)
	cssURL    = regexp.MustCompile(`src:\s*url\((https://fonts\.gstatic\.com/[^\s()'";]+\.ttf)\)`)
	cssWeight = regexp.MustCompile(`font-weight:\s*(400|700)\s*;`)
	cssItalic = regexp.MustCompile(`font-style:\s*italic\s*;`)
)

func (r *Resolver) get(ctx context.Context, address string, limit int64) ([]byte, int, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" ||
		(u.Host != "fonts.googleapis.com" && u.Host != "fonts.gstatic.com") {
		return nil, 0, fmt.Errorf("invalid font URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, err
	}
	// A non-browser UA requests full, static TTFs, not variable fonts or WOFF
	// subsets, neither of which XeTeX can use as a complete font family.
	req.Header.Set("User-Agent", "Refract/1.0")
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("font service returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("font response too large")
	}
	return b, resp.StatusCode, err
}

func key(name string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(name)))
	return hex.EncodeToString(sum[:16])
}

func (r *Resolver) download(ctx context.Context, name string) (family, error) {
	id := key(name)
	v, _ := r.locks.LoadOrStore(id, make(chan struct{}, 1))
	lock := v.(chan struct{})
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return family{}, ctx.Err()
	}
	defer func() { <-lock }()
	if until, ok := r.missing.Load(id); ok && time.Now().Before(until.(time.Time)) {
		return family{}, fmt.Errorf("font family unavailable: %s", name)
	}
	dir := filepath.Join(r.CacheDir, id)
	if raw, err := os.ReadFile(filepath.Join(dir, "family.json")); err == nil {
		var f family
		if json.Unmarshal(raw, &f) == nil && f.Regular != "" {
			ok := true
			for _, file := range f.files() {
				if file != "" {
					if filepath.Base(file) != file {
						ok = false
						break
					}
					if stat, err := os.Stat(filepath.Join(dir, file)); err != nil || !stat.Mode().IsRegular() || stat.Size() < 12 {
						ok = false
						break
					}
				}
			}
			if ok {
				return f, nil
			}
		}
	}
	if r.Offline {
		return family{}, fmt.Errorf("font is not cached: %s", name)
	}
	address := "https://fonts.googleapis.com/css?family=" + url.QueryEscape(name+":400,400i,700,700i")
	css, status, err := r.get(ctx, address, 256<<10)
	if status == http.StatusBadRequest {
		// Some families only have a regular face.
		css, status, err = r.get(ctx, "https://fonts.googleapis.com/css?family="+url.QueryEscape(name), 256<<10)
	}
	if status == http.StatusBadRequest || status == http.StatusNotFound {
		r.missing.Store(id, time.Now().Add(time.Hour))
	}
	if err != nil {
		return family{}, err
	}
	urls := map[string]string{}
	for _, block := range faceBlock.FindAllSubmatch(css, -1) {
		u := cssURL.FindSubmatch(block[1])
		w := cssWeight.FindSubmatch(block[1])
		if u == nil || w == nil {
			continue
		}
		style := "regular"
		if string(w[1]) == "700" {
			style = "bold"
		}
		if cssItalic.Match(block[1]) {
			if style == "bold" {
				style = "bold-italic"
			} else {
				style = "italic"
			}
		}
		urls[style] = string(u[1])
	}
	if urls["regular"] == "" {
		return family{}, fmt.Errorf("no static regular face for %s", name)
	}
	if err := os.MkdirAll(r.CacheDir, 0o700); err != nil {
		return family{}, err
	}
	tmp, err := os.MkdirTemp(r.CacheDir, ".font-")
	if err != nil {
		return family{}, err
	}
	defer os.RemoveAll(tmp)
	var f family
	for _, face := range []struct {
		style string
		file  *string
	}{
		{"regular", &f.Regular}, {"bold", &f.Bold}, {"italic", &f.Italic}, {"bold-italic", &f.BoldItalic},
	} {
		if urls[face.style] == "" {
			continue
		}
		b, _, err := r.get(ctx, urls[face.style], 16<<20)
		if err != nil {
			return family{}, err
		}
		if len(b) < 12 || (string(b[:4]) != "\x00\x01\x00\x00" && string(b[:4]) != "OTTO") {
			return family{}, fmt.Errorf("font service did not return an OpenType font")
		}
		*face.file = face.style + ".ttf"
		if err := os.WriteFile(filepath.Join(tmp, *face.file), b, 0o600); err != nil {
			return family{}, err
		}
	}
	raw, _ := json.Marshal(f)
	if err := os.WriteFile(filepath.Join(tmp, "family.json"), raw, 0o600); err != nil {
		return family{}, err
	}
	// Remove an incomplete cache entry before replacing it with the complete set.
	if err := os.RemoveAll(dir); err != nil {
		return family{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return family{}, err
	}
	return f, nil
}
