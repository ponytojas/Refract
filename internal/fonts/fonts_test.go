package fonts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf16"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func reply(status int, b []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}
}

func writeFile(t *testing.T, dir, name string, b []byte) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFamilyReferences(t *testing.T) {
	src := `% \setmainfont{Commented}
\setmainfont[Scale=0.95]{Open Sans}[Ligatures=TeX]
\newfontfamily{\heading}[Scale=1]{Inter}
\newfontface\face{Inter}
\IfFontExistsTF{Latin Modern Sans}{a}{b}
\setsansfont{Explicit}[Path = fonts/, Extension = .otf]
\setmainfont[UprightFont = file.otf]{ExplicitBefore}
\setmonofont{lmmono10-regular.otf}
\setmainfont{\selectedFont}
\setmainfont{https://attacker.invalid/font}
50\% \fontspec{TeX Gyre Pagella}
`
	want := []string{"Inter", "Latin Modern Sans", "Open Sans", "TeX Gyre Pagella"}
	if got := families(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("families = %v, want %v", got, want)
	}
}

// A small SFNT with UTF-16 naming records, enough to test metadata discovery.
func fontFixture(name, style string, weight uint16, italic bool) []byte {
	records := []struct {
		id    uint16
		value string
	}{{1, name}, {2, style}, {4, name + " " + style}, {16, name}}
	table := make([]byte, 6+len(records)*12)
	binary.BigEndian.PutUint16(table[2:], uint16(len(records)))
	binary.BigEndian.PutUint16(table[4:], uint16(len(table)))
	var storage []byte
	for i, record := range records {
		r := table[6+i*12:]
		binary.BigEndian.PutUint16(r, 3)
		binary.BigEndian.PutUint16(r[2:], 1)
		binary.BigEndian.PutUint16(r[4:], 0x0409)
		binary.BigEndian.PutUint16(r[6:], record.id)
		units := utf16.Encode([]rune(record.value))
		binary.BigEndian.PutUint16(r[8:], uint16(len(units)*2))
		binary.BigEndian.PutUint16(r[10:], uint16(len(storage)))
		for _, u := range units {
			storage = binary.BigEndian.AppendUint16(storage, u)
		}
	}
	table = append(table, storage...)
	b := make([]byte, 12+32+64)
	copy(b, "\x00\x01\x00\x00")
	binary.BigEndian.PutUint16(b[4:], 2)
	copy(b[12:], "name")
	binary.BigEndian.PutUint32(b[20:], uint32(len(b)))
	binary.BigEndian.PutUint32(b[24:], uint32(len(table)))
	copy(b[28:], "OS/2")
	binary.BigEndian.PutUint32(b[36:], 44)
	binary.BigEndian.PutUint32(b[40:], 64)
	binary.BigEndian.PutUint16(b[48:], weight)
	if italic {
		binary.BigEndian.PutUint16(b[106:], 1)
	}
	return append(b, table...)
}

func TestLocalFontsTakePriorityAndStayPrivate(t *testing.T) {
	dir, cache := t.TempDir(), t.TempDir()
	source := []byte("\\setmainfont{Prívate Font}\n")
	writeFile(t, dir, "sub/main.tex", source)
	for _, face := range []struct {
		name, style string
		weight      uint16
		italic      bool
	}{
		{"regular.ttf", "Regular", 400, false}, {"bold.ttf", "Bold", 700, false},
		{"italic.ttf", "Italic", 400, true}, {"bolditalic.ttf", "Bold Italic", 700, true},
	} {
		writeFile(t, dir, "assets/"+face.name, fontFixture("Prívate Font", face.style, face.weight, face.italic))
	}
	r := NewResolver(cache)
	r.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Error("uploaded font caused network request")
		return reply(500, nil), nil
	})
	driver, err := r.Prepare(context.Background(), dir, "sub/main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(driver, "Prívate~Font") || !strings.Contains(driver, "Path={../.refract/fonts/") {
		t.Fatalf("bad driver: %s", driver)
	}
	for _, file := range []string{"regular.ttf", "bold.ttf", "italic.ttf", "bold-italic.ttf"} {
		if _, err := os.Stat(filepath.Join(dir, ".refract/fonts", key("Prívate Font"), file)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "sub/main.tex"))
	if !bytes.Equal(got, source) {
		t.Fatal("source was changed")
	}
	entries, _ := os.ReadDir(cache)
	if len(entries) != 0 {
		t.Fatal("private project font entered shared cache")
	}
}

func TestGoogleDownloadIsCachedAndCoalesced(t *testing.T) {
	r := NewResolver(t.TempDir())
	var calls atomic.Int32
	r.HTTP.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.Header.Get("User-Agent") != "Refract/1.0" {
			t.Error("missing static-font user agent")
		}
		if req.URL.Host == "fonts.googleapis.com" {
			if req.URL.Query().Get("family") != "Open Sans:400,400i,700,700i" {
				t.Errorf("bad request: %s", req.URL)
			}
			var css strings.Builder
			for _, face := range []struct {
				style  string
				weight int
				file   string
			}{{"normal", 400, "regular"}, {"normal", 700, "bold"}, {"italic", 400, "italic"}, {"italic", 700, "bolditalic"}} {
				fmt.Fprintf(&css, "@font-face {font-family: 'Open Sans';font-style: %s;font-weight: %d;src: url(https://fonts.gstatic.com/s/open/%s.ttf) format('truetype');}\n", face.style, face.weight, face.file)
			}
			return reply(200, []byte(css.String())), nil
		}
		return reply(200, fontFixture("Open Sans", "Regular", 400, false)), nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := r.download(context.Background(), "Open Sans")
			if err != nil || f.Regular == "" || f.BoldItalic == "" {
				t.Errorf("download: %+v %v", f, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 5 {
		t.Fatalf("%d requests, want one CSS plus four font files", calls.Load())
	}
	// A new resolver/process also reuses the persistent cache without a network.
	r2 := NewResolver(r.CacheDir)
	r2.Offline = true
	r2.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("cache needed network")
		return reply(500, nil), nil
	})
	if _, err := r2.download(context.Background(), "Open Sans"); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableFallbackDoesNotBlockCompilation(t *testing.T) {
	r := NewResolver(t.TempDir())
	var calls int
	r.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return reply(400, nil), nil })
	dir := t.TempDir()
	writeFile(t, dir, "main.tex", []byte(`\IfFontExistsTF{Unavailable Font}{\setmainfont{Unavailable Font}}{\setmainfont{Latin Modern Sans}}`))
	for i := 0; i < 2; i++ {
		driver, err := r.Prepare(context.Background(), dir, "main.tex")
		if err != nil || !strings.Contains(driver, "lmsans10-regular.otf") {
			t.Fatalf("prepare: %s %v", driver, err)
		}
		if strings.Contains(driver, "{Unavailable~Font}") {
			t.Fatal("missing font was declared available")
		}
	}
	if calls != 2 {
		t.Fatalf("missing family retried %d times", calls)
	}
}

func TestUntrustedURLsAndResponses(t *testing.T) {
	r := NewResolver(t.TempDir())
	r.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("untrusted URL requested")
		return reply(200, nil), nil
	})
	for _, u := range []string{"http://fonts.gstatic.com/a.ttf", "https://attacker.invalid/a.ttf", "https://fonts.gstatic.com:443/a.ttf", "https://user@fonts.gstatic.com/a.ttf", "https://fonts.gstatic.com.attacker.invalid/a.ttf"} {
		if _, _, err := r.get(context.Background(), u, 10); err == nil {
			t.Errorf("accepted %s", u)
		}
	}
	r.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return reply(200, []byte("too large")), nil })
	if _, _, err := r.get(context.Background(), "https://fonts.gstatic.com/a.ttf", 2); err == nil {
		t.Fatal("unbounded response")
	}
	r.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return reply(302, nil), nil })
	if _, _, err := r.get(context.Background(), "https://fonts.gstatic.com/a.ttf", 10); err == nil {
		t.Fatal("accepted redirect")
	}
}

func TestPrepareIgnoresBuildOutputAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tex", []byte(`\setmainfont[Path=fonts/]{Explicit}`))
	writeFile(t, dir, ".refract/stale.tex", []byte(`\setmainfont{Stale}`))
	writeFile(t, dir, ".git/stale.tex", []byte(`\setmainfont{Stale}`))
	outside := t.TempDir()
	writeFile(t, outside, "outside.tex", []byte(`\setmainfont{Private}`))
	if err := os.Symlink(filepath.Join(outside, "outside.tex"), filepath.Join(dir, "linked.tex")); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(t.TempDir())
	r.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected network")
		return reply(500, nil), nil
	})
	if driver, err := r.Prepare(context.Background(), dir, "main.tex"); err != nil || driver != "" {
		t.Fatalf("prepare: %q %v", driver, err)
	}
}

func FuzzFontNames(f *testing.F) {
	f.Add(fontFixture("Open Sans", "Bold", 700, false))
	f.Add([]byte("OTTO"))
	f.Fuzz(func(t *testing.T, b []byte) { fontNames(b) })
}
