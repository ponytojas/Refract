package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/store"
)

// localPort is where refract local listens unless the port is taken.
const localPort = 8080

// localKeyFile holds the generated secret key inside the data folder, until
// the system keychain takes over (#51).
const localKeyFile = "secret.key"

// localStartTimeout bounds the wait for the server to answer before the
// address is printed and the browser opened.
const localStartTimeout = 30 * time.Second

// runLocal is `refract local`: Refract on this computer with nothing to
// configure. Data lives in the per-user data folder, the secret key is made
// on first run, there is no sign-in and the server listens on loopback only.
func runLocal(args []string) error {
	fl := flag.NewFlagSet("refract local", flag.ContinueOnError)
	port := fl.Int("port", 0, "port to listen on, on 127.0.0.1 (default 8080, or a free port when 8080 is taken)")
	noBrowser := fl.Bool("no-browser", false, "don't open the browser")
	fl.Usage = func() {
		fmt.Fprint(fl.Output(), "Usage: refract local [--port N] [--no-browser]\n\n"+
			"Runs Refract on this computer with no configuration: data in your user\n"+
			"folder (REFRACT_DATA_DIR overrides it), no sign-in, 127.0.0.1 only.\n"+
			"REFRACT_ADDR may choose another address on 127.0.0.1 or ::1 instead of --port.\n"+
			"The TeX seed shipped with the tools is copied into the compile cache at startup\n"+
			"(REFRACT_TEX_SEED=none turns it off), so the first compile works offline.\n\n")
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see refract local -h)", fl.Arg(0))
	}
	portSet := false
	fl.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	if portSet && (*port < 1 || *port > 65535) {
		return fmt.Errorf("--port %d: want a port from 1 to 65535", *port)
	}

	slog.SetDefault(slog.New(localLogHandler(os.Stderr)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return startLocal(ctx, localOptions{
		getenv:  os.Getenv,
		port:    *port,
		portSet: portSet,
		out:     os.Stdout,
		open: func(u string) {
			if !*noBrowser {
				openBrowser(u)
			}
		},
	})
}

type localOptions struct {
	getenv  func(string) string
	port    int  // with portSet, the port asked for with --port
	portSet bool // false: localPort, or a free one when it is taken
	out     io.Writer
	open    func(url string) // opens the browser
	// defaultDir replaces localDataDir's result, for tests.
	defaultDir string
}

// startLocal resolves the data folder, key and listener, then runs the server
// until ctx ends.
func startLocal(ctx context.Context, o localOptions) error {
	if m, err := mode.Parse(o.getenv("REFRACT_MODE")); err != nil {
		return err
	} else if m == mode.SaaS {
		return errors.New("refract local runs Refract for yourself, not as the hosted service: unset REFRACT_MODE, or run the server with plain `refract`")
	}
	if p, err := parseProfile(o.getenv("REFRACT_PROFILE")); err != nil {
		return err
	} else if p == profileTeam {
		return errors.New("refract local runs Refract for yourself, without sign-in, not for a team: unset REFRACT_PROFILE, or run the server with plain `refract`")
	}
	dataDir := o.getenv("REFRACT_DATA_DIR")
	if dataDir == "" {
		dataDir = o.defaultDir
	}
	if dataDir == "" {
		home, _ := os.UserHomeDir() // an empty home is reported by localDataDir
		var err error
		if dataDir, err = localDataDir(runtime.GOOS, o.getenv, home); err != nil {
			return err
		}
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if v := o.getenv("REFRACT_AUTH"); v != "" && authMode(v) != "none" {
		slog.Warn("refract local runs without sign-in; REFRACT_AUTH is ignored", "REFRACT_AUTH", v)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("creating the data folder: %w", err)
	}
	var key []byte // nil: run reads REFRACT_SECRET_KEY, which wins when set
	if o.getenv("REFRACT_SECRET_KEY") == "" {
		if key, err = loadOrCreateKey(filepath.Join(dataDir, localKeyFile)); err != nil {
			return err
		}
	}
	return run(ctx, runOptions{
		dataDir:  dataDir,
		key:      key,
		authKind: "none",
		texSeed:  true,
		listen: func() (net.Listener, error) {
			return listenLocal(o.getenv("REFRACT_ADDR"), o.port, o.portSet, listenTCP)
		},
		ready: func(ctx context.Context, addr net.Addr) {
			u := "http://" + addr.String()
			waitCtx, cancel := context.WithTimeout(ctx, localStartTimeout)
			defer cancel()
			if !waitHealthy(waitCtx, u+"/healthz") {
				if ctx.Err() == nil {
					slog.Error("Refract is listening but doesn't answer; the browser was not opened", "url", u, "waited", localStartTimeout.String())
				}
				return
			}
			fmt.Fprintf(o.out, "\nRefract is running at %s\nData folder: %s\nThere is no sign-in: only this computer can open it. Press Ctrl+C to stop.\n\n", u, dataDir)
			o.open(u)
		},
	})
}

// localDataDir is the per-user folder where refract local keeps its data:
// ~/Library/Application Support/Refract on macOS, %LocalAppData%\Refract on
// Windows, and $XDG_DATA_HOME/refract or ~/.local/share/refract elsewhere.
func localDataDir(goos string, getenv func(string) string, home string) (string, error) {
	if goos == "windows" {
		if dir := getenv("LocalAppData"); dir != "" {
			return filepath.Join(dir, "Refract"), nil
		}
		return "", errors.New("%LocalAppData% is not set: set REFRACT_DATA_DIR to the folder Refract should use")
	}
	// XDG requires an absolute path and says to ignore a relative one.
	if dir := getenv("XDG_DATA_HOME"); goos != "darwin" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "refract"), nil
	}
	if home == "" {
		return "", errors.New("can't find your home folder: set REFRACT_DATA_DIR to the folder Refract should use")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Refract"), nil
	}
	return filepath.Join(home, ".local", "share", "refract"), nil
}

// loadOrCreateKey reads the secret key at path, or creates it with a new
// random key on first run. It never replaces an existing file, even an
// invalid one: credentials stored with that key would be lost.
func loadOrCreateKey(path string) ([]byte, error) {
	for range 2 {
		key, err := readKeyFile(path)
		if !errors.Is(err, fs.ErrNotExist) {
			return key, err
		}
		key = make([]byte, 32)
		rand.Read(key)
		switch err := createExclusive(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n")); {
		case err == nil:
			return key, nil
		case !errors.Is(err, fs.ErrExist):
			return nil, fmt.Errorf("saving the secret key: %w", err)
		}
		// Another refract local created it first: read theirs.
	}
	return nil, fmt.Errorf("can't read or create the secret key at %s", path)
}

// readKeyFile reads an existing key file. It must be a regular file (a pipe
// or device would block or never end) that only its owner can read.
func readKeyFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err // fs.ErrNotExist on first run
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file; move it away and Refract creates a new key (stored passwords and API keys then become unreadable)", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users (mode %v): run chmod 600 %q", path, fi.Mode().Perm(), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<10))
	if err != nil {
		return nil, err
	}
	key, err := store.ParseKey(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid secret key. Restore the original file: replacing it makes stored passwords and API keys unreadable", path)
	}
	return key, nil
}

// linkFile is os.Link, replaced in tests.
var linkFile = os.Link

// createExclusive writes an owner-only file at path only if nothing is there
// yet. It writes a temporary file and links it into place, so the key is
// complete before it appears under its name. Where the file system has no
// hard links (exFAT, some network shares) it creates the file exclusively
// and writes it directly instead. On Windows the file keeps the folder's
// permissions: %LocalAppData% is private to its user.
func createExclusive(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := writeAndSync(tmp, data); err != nil {
		return err
	}
	err = linkFile(tmp.Name(), path)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err // fs.ErrExist when another start won the race
	}
	if err := writeAndSync(f, data); err != nil {
		os.Remove(path) // ours, and incomplete
		return err
	}
	return nil
}

func writeAndSync(f *os.File, data []byte) error {
	err := f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func listenTCP(a *net.TCPAddr) (net.Listener, error) { return net.ListenTCP("tcp", a) }

// listenLocal opens refract local's listener. REFRACT_ADDR and --port choose
// the address; without either it is 127.0.0.1:8080, or a free port when 8080
// is taken. The address is checked before listen is called, so nothing ever
// listens where other computers could connect.
func listenLocal(envAddr string, port int, portSet bool, listen func(*net.TCPAddr) (net.Listener, error)) (net.Listener, error) {
	loopback := net.IPv4(127, 0, 0, 1)
	switch {
	case envAddr != "" && portSet:
		return nil, errors.New("set either REFRACT_ADDR or --port, not both")
	case envAddr != "":
		a, err := localAddr(envAddr)
		if err != nil {
			return nil, err
		}
		ln, err := listen(a)
		if err != nil {
			return nil, fmt.Errorf("REFRACT_ADDR=%s: %w", envAddr, err)
		}
		return ln, nil
	case portSet:
		ln, err := listen(&net.TCPAddr{IP: loopback, Port: port})
		if err != nil {
			return nil, fmt.Errorf("--port %d: %w", port, err)
		}
		return ln, nil
	}
	ln, err := listen(&net.TCPAddr{IP: loopback, Port: localPort})
	if err == nil {
		return ln, nil
	}
	slog.Info("port taken, using a free one", "port", localPort, "err", err)
	return listen(&net.TCPAddr{IP: loopback, Port: 0})
}

// localAddr resolves REFRACT_ADDR once and accepts it only on 127.0.0.1 or
// ::1: without sign-in, other computers must not reach Refract, and those are
// the loopback addresses the Host check answers to. The resolved address is
// what gets listened on, so a name can't resolve differently in between.
func localAddr(addr string) (*net.TCPAddr, error) {
	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("REFRACT_ADDR=%s: %w", addr, err)
	}
	if !a.IP.Equal(net.IPv4(127, 0, 0, 1)) && !a.IP.Equal(net.IPv6loopback) {
		return nil, fmt.Errorf("REFRACT_ADDR=%s: refract local has no sign-in, so it only listens on 127.0.0.1 or ::1, where other computers can't reach it", addr)
	}
	a.Zone = ""
	return a, nil
}

// waitHealthy polls url until it answers 200 or ctx ends, and reports which.
func waitHealthy(ctx context.Context, url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// localLogHandler writes readable lines to a terminal and JSON otherwise
// (for instance when a desktop app or a service manager collects the log).
func localLogHandler(w *os.File) slog.Handler {
	if fi, err := w.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return slog.NewTextHandler(w, nil)
	}
	return slog.NewJSONHandler(w, nil)
}
