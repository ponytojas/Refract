package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalDataDir(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	// Absolute paths in the syntax of the system running the test, which is
	// what filepath.IsAbs and filepath.Join use: /… here, C:\… on Windows.
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	abs := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	home, xdg := abs("home", "ana"), abs("xdg")
	appData := abs("Users", "ana", "AppData", "Local")
	for _, tc := range []struct {
		goos    string
		env     func(string) string
		home    string
		want    string // empty means an error
		wantErr string
	}{
		{"darwin", env(), home, filepath.Join(home, "Library", "Application Support", "Refract"), ""},
		{"darwin", env("XDG_DATA_HOME", xdg), home, filepath.Join(home, "Library", "Application Support", "Refract"), ""},
		{"linux", env(), home, filepath.Join(home, ".local", "share", "refract"), ""},
		{"linux", env("XDG_DATA_HOME", xdg), home, filepath.Join(xdg, "refract"), ""},
		{"freebsd", env("XDG_DATA_HOME", xdg), home, filepath.Join(xdg, "refract"), ""},
		// A relative XDG_DATA_HOME is invalid and ignored.
		{"linux", env("XDG_DATA_HOME", filepath.Join("rel", "xdg")), home, filepath.Join(home, ".local", "share", "refract"), ""},
		{"linux", env(), "", "", "home folder"},
		{"darwin", env(), "", "", "home folder"},
		{"windows", env("LocalAppData", appData), "", filepath.Join(appData, "Refract"), ""},
		{"windows", env(), home, "", "LocalAppData"},
	} {
		got, err := localDataDir(tc.goos, tc.env, tc.home)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "REFRACT_DATA_DIR") {
				t.Errorf("%s: err = %v, want one mentioning %q and REFRACT_DATA_DIR", tc.goos, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.goos, got, err, tc.want)
		}
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), localKeyFile)
	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("key is %d bytes", len(first))
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
		}
	}
	again, err := loadOrCreateKey(path)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatalf("second load: %v, same key = %v", err, bytes.Equal(first, again))
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".tmp-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// A key file Refract can't use is reported and left exactly as it is.
func TestLoadOrCreateKeyRefuses(t *testing.T) {
	unchanged := func(t *testing.T, path, want string) {
		t.Helper()
		if b, _ := os.ReadFile(path); string(b) != want {
			t.Fatalf("the key file was changed: %q", b)
		}
	}
	t.Run("invalid", func(t *testing.T) {
		for _, content := range []string{"", "not a key\n"} {
			path := filepath.Join(t.TempDir(), localKeyFile)
			os.WriteFile(path, []byte(content), 0o600)
			if _, err := loadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("%q: err = %v, want one naming the file", content, err)
			}
			unchanged(t, path, content)
		}
	})
	t.Run("readable by others", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows has no permission bits")
		}
		path := filepath.Join(t.TempDir(), localKeyFile)
		content := "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=\n"
		os.WriteFile(path, []byte(content), 0o644)
		os.Chmod(path, 0o644) // past the umask
		if _, err := loadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("err = %v, want one asking for chmod 600", err)
		}
		unchanged(t, path, content)
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
			t.Fatalf("mode changed to %v", fi.Mode().Perm())
		}
	})
	t.Run("not a regular file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), localKeyFile)
		os.Mkdir(dir, 0o700)
		if _, err := loadOrCreateKey(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("directory: err = %v", err)
		}
	})
}

func TestLoadOrCreateKeyWithoutHardLinks(t *testing.T) {
	linkFile = func(string, string) error { return &os.LinkError{Op: "link", Err: errors.ErrUnsupported} }
	t.Cleanup(func() { linkFile = os.Link })
	path := filepath.Join(t.TempDir(), localKeyFile)
	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v", fi.Mode().Perm())
		}
	}
	again, err := loadOrCreateKey(path)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatalf("second load: %v", err)
	}
}

func TestLoadOrCreateKeyConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), localKeyFile)
	keys := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			k, err := loadOrCreateKey(path)
			if err != nil {
				t.Error(err)
			}
			keys[i] = k
		})
	}
	wg.Wait()
	for _, k := range keys[1:] {
		if !bytes.Equal(k, keys[0]) {
			t.Fatal("concurrent first runs ended with different keys")
		}
	}
}

func TestListenLocal(t *testing.T) {
	// asked records the addresses listenLocal tries to listen on.
	var asked []*net.TCPAddr
	record := func(a *net.TCPAddr) (net.Listener, error) {
		asked = append(asked, a)
		return listenTCP(a)
	}
	port := func(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }

	t.Run("default port, or a free one when taken", func(t *testing.T) {
		ln, err := listenLocal("", 0, false, listenTCP)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		if port(ln) != localPort {
			// Something else on this machine has 8080: that already is the fallback.
			t.Logf("port %d was taken; got %d", localPort, port(ln))
			return
		}
		other, err := listenLocal("", 0, false, listenTCP)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if p := port(other); p == localPort || p == 0 || !other.Addr().(*net.TCPAddr).IP.Equal(net.IPv4(127, 0, 0, 1)) {
			t.Fatalf("fallback listens on %s", other.Addr())
		}
	})

	t.Run("--port listens on 127.0.0.1 at that port", func(t *testing.T) {
		asked = nil
		taken := errors.New("taken")
		_, err := listenLocal("", 4321, true, func(a *net.TCPAddr) (net.Listener, error) {
			asked = append(asked, a)
			return nil, taken
		})
		if !errors.Is(err, taken) {
			t.Fatalf("err = %v", err)
		}
		// Asked once, for exactly that address: a taken --port isn't replaced.
		if len(asked) != 1 || asked[0].String() != "127.0.0.1:4321" {
			t.Fatalf("asked for %v", asked)
		}
		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Close()
		if ln, err := listenLocal("", port(busy), true, listenTCP); err == nil {
			ln.Close()
			t.Fatal("a taken --port was accepted")
		}
	})

	t.Run("REFRACT_ADDR is refused before listening unless 127.0.0.1 or ::1", func(t *testing.T) {
		for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0", "127.0.0.2:0", "192.168.1.20:0", "not a host:0"} {
			asked = nil
			ln, err := listenLocal(addr, 0, false, record)
			if err == nil {
				ln.Close()
			}
			if err == nil || len(asked) > 0 || !strings.Contains(err.Error(), "REFRACT_ADDR") {
				t.Errorf("REFRACT_ADDR=%s: err = %v, listened on %v", addr, err, asked)
			}
		}
		for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
			asked = nil
			ln, err := listenLocal(addr, 0, false, record)
			if err != nil {
				if strings.Contains(addr, "::1") {
					t.Logf("no IPv6 loopback here: %v", err)
					continue
				}
				t.Fatalf("REFRACT_ADDR=%s: %v", addr, err)
			}
			ln.Close()
			if ip := asked[0].IP; !ip.Equal(net.IPv4(127, 0, 0, 1)) && !ip.Equal(net.IPv6loopback) {
				t.Errorf("REFRACT_ADDR=%s listened on %v", addr, asked[0])
			}
		}
		asked = nil
		if _, err := listenLocal("127.0.0.1:0", 9000, true, record); err == nil || len(asked) > 0 {
			t.Error("REFRACT_ADDR and --port together were accepted")
		}
	})
}

func TestLocalRefusesTeamProfile(t *testing.T) {
	err := startLocal(context.Background(), localOptions{
		getenv:     func(k string) string { return map[string]string{"REFRACT_PROFILE": "team"}[k] },
		defaultDir: t.TempDir(),
		out:        &bytes.Buffer{},
		open:       func(string) {},
	})
	if err == nil || !strings.Contains(err.Error(), "REFRACT_PROFILE") {
		t.Errorf("REFRACT_PROFILE=team: err = %v", err)
	}
}

// TestLocalStartsAndReopens runs refract local twice on the same data folder
// with no REFRACT_* variables. An API key saved through the first server is
// read back through the second, so the second uses the key the first made.
func TestLocalStartsAndReopens(t *testing.T) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "REFRACT_") {
			t.Setenv(name, "")
		}
	}
	dataDir := filepath.Join(t.TempDir(), "Refract")

	call := func(method, u, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, u, strings.NewReader(body))
		req.Header.Set("X-Refract", "1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("%s %s: %s", method, u, resp.Status)
		}
		return resp
	}

	// start runs refract local until the returned stop is called.
	start := func() (u string, stop func()) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		opened := make(chan string, 1)
		var out bytes.Buffer
		done := make(chan error, 1)
		go func() {
			done <- startLocal(ctx, localOptions{
				getenv:     os.Getenv,
				out:        &out,
				open:       func(u string) { opened <- u },
				defaultDir: dataDir,
			})
		}()
		select {
		case u = <-opened:
		case err := <-done:
			cancel()
			t.Fatalf("refract local stopped: %v", err)
		case <-time.After(20 * time.Second):
			cancel()
			t.Fatal("the browser was never opened")
		}
		if host, _, _ := net.SplitHostPort(strings.TrimPrefix(u, "http://")); host != "127.0.0.1" {
			t.Errorf("opened %s, want a 127.0.0.1 address", u)
		}
		if !strings.Contains(out.String(), u) || !strings.Contains(out.String(), dataDir) {
			t.Errorf("printed %q, want the address and the data folder", out.String())
		}
		return u, func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("stopping: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("refract local didn't stop")
			}
		}
	}

	u, stop := start()
	call(http.MethodPut, u+"/api/providers/p1", `{"kind":"openai","name":"OpenAI","api_key":"sk-test"}`).Body.Close()
	stop()
	keyFile, err := os.ReadFile(filepath.Join(dataDir, localKeyFile))
	if err != nil {
		t.Fatal(err)
	}

	u, stop = start()
	defer stop()
	resp := call(http.MethodGet, u+"/api/providers", "")
	defer resp.Body.Close()
	var got struct {
		Configured []struct {
			ID     string `json:"id"`
			HasKey bool   `json:"has_key"`
		} `json:"configured"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Configured) != 1 || got.Configured[0].ID != "p1" || !got.Configured[0].HasKey {
		t.Fatalf("providers after restart: %+v", got.Configured)
	}
	if again, _ := os.ReadFile(filepath.Join(dataDir, localKeyFile)); !bytes.Equal(again, keyFile) {
		t.Fatal("the key file changed between runs")
	}
}
