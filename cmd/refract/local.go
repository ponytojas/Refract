package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ponytojas/Refract/internal/localkey"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/store"
)

// localPort is where refract local listens unless the port is taken.
const localPort = 8080

// localKeyFile holds the secret key inside the data folder when the system
// credential store can't, or when it was already there before the store was
// used.
const localKeyFile = "secret.key"

// localStartTimeout bounds the wait for the server to answer before the
// address is printed and the browser opened.
var localStartTimeout = 30 * time.Second

// localHealthy waits for the server to answer; tests replace it.
var localHealthy = waitHealthy

// runLocal is `refract local`: Refract on this computer with nothing to
// configure. Data lives in the per-user data folder, the secret key is made
// on first run, there is no sign-in and the server listens on loopback only.
func runLocal(args []string) error {
	out := &jsonOut{w: os.Stdout}
	if wantsJSON(args) {
		defer func() {
			// A crash while starting still leaves the line, then crashes as before.
			if p := recover(); p != nil {
				if !out.wrote.Load() {
					printJSON(os.Stdout, errorStatus{State: stateError, Error: fmt.Sprint("refract crashed: ", p)})
				}
				panic(p)
			}
		}()
	}
	err := runLocalTo(args, out)
	var e errExit
	if err != nil && wantsJSON(args) && !out.wrote.Load() && !errors.As(err, &e) {
		// A program reading standard output always gets one line, even when
		// starting fails.
		printJSON(os.Stdout, errorStatus{State: stateError, Error: err.Error()})
	}
	return err
}

func runLocalTo(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("refract local", flag.ContinueOnError)
	port := fl.Int("port", 0, "port to listen on, on 127.0.0.1 (default 8080, or a free port when 8080 is taken)")
	noBrowser := fl.Bool("no-browser", false, "don't open the browser")
	embedded := fl.Bool("embedded", false, "for a program that embeds Refract: no browser, a free port, one JSON line on standard output when ready, stops when standard input closes; needs --token-fd or REFRACT_EMBED_TOKEN")
	tokenFD := fl.Int("token-fd", -1, "with --embedded: the inherited file descriptor to read the per-launch token from (default: REFRACT_EMBED_TOKEN)")
	status := fl.Bool("status", false, "print one JSON line telling whether a refract local is running on the data folder, and exit (0 when it is, 2 while it starts, 1 otherwise)")
	fl.Usage = func() {
		fmt.Fprint(fl.Output(), "Usage: refract local [--port N] [--no-browser] [--status] [--embedded [--token-fd N]]\n\n"+
			"Runs Refract on this computer with no configuration: data in your user\n"+
			"folder (REFRACT_DATA_DIR overrides it), no sign-in, 127.0.0.1 only.\n"+
			"REFRACT_ADDR may choose another address on 127.0.0.1 or ::1 instead of --port.\n"+
			"Only one refract local runs per data folder: a second start opens the first.\n"+
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

	if *embedded && *status {
		return errors.New("--embedded and --status can't be combined")
	}
	if !*embedded && *tokenFD >= 0 {
		return errors.New("--token-fd is only for --embedded")
	}
	slog.SetDefault(slog.New(localLogHandler(os.Stderr)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var token string
	if *embedded {
		// The parent going away (standard input closing) stops the server like
		// SIGTERM, also while the token is still awaited.
		var cancel context.CancelFunc
		ctx, cancel = watchStdin(ctx, os.Stdin)
		defer cancel()
		var err error
		if token, err = readEmbedToken(ctx, *tokenFD, os.Getenv, os.Unsetenv); err != nil {
			return err
		}
	}
	return startLocal(ctx, localOptions{
		embedded: *embedded,
		token:    token,
		getenv:   os.Getenv,
		keys:     localkey.System(),
		port:     *port,
		portSet:  portSet,
		status:   *status,
		out:      stdout,
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
	status  bool // report the instance on the data folder and return
	out     io.Writer
	// embedded: see runLocal. token is the per-launch token it requires.
	embedded bool
	token    string
	open     func(url string) // opens the browser
	// keys is the system credential store; nil means the key file only.
	keys localkey.Store
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
	if o.status {
		return reportLocalStatus(ctx, o.out, dataDir)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("creating the data folder: %w", err)
	}
	// The lock comes before the database and the key: a second start must
	// not touch either while the first one is using them.
	var lock *instanceLock
	for lock == nil {
		l, held, err := lockInstance(dataDir)
		if err != nil {
			return err
		}
		if !held {
			lock = l
			break
		}
		st := checkInstance(ctx, dataDir, 0)
		if st.State == stateStarting {
			fmt.Fprintf(o.out, "Another refract local (pid %d) is still starting on %s; waiting for it…\n", st.PID, dataDir)
			st = checkInstance(ctx, dataDir, instanceStartingWait)
		}
		switch st.State {
		case stateRunning:
			if o.embedded {
				printJSON(o.out, st)
				return nil
			}
			if st.Embedded {
				// Its pages need the app's token: a browser would only get 401.
				fmt.Fprintf(o.out, "Refract is already running in the desktop app (pid %d) on this data folder\nData folder: %s\nUse the app, or quit it and start refract local again.\n", st.PID, dataDir)
				return nil
			}
			fmt.Fprintf(o.out, "Refract is already running at %s\nData folder: %s\n", st.URL, dataDir)
			o.open(st.URL)
			return nil
		case stateStarting, stateUnresponsive:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if o.embedded {
				printJSON(o.out, st)
				return errExit{code: 1, msg: recoveryHelp(st)}
			}
			return errors.New(recoveryHelp(st))
		}
		// The other one stopped meanwhile: take the lock again.
	}
	defer lock.Release()
	lock.embedded = o.embedded
	var key []byte // nil: run reads REFRACT_SECRET_KEY, which wins when set
	if o.getenv("REFRACT_SECRET_KEY") == "" {
		if key, err = resolveLocalKey(o.keys, dataDir); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var readyErr error // set before cancel, read after run returns
	var readyPrinted atomic.Bool
	var wrap func(http.Handler) http.Handler
	if o.embedded {
		wrap = requireEmbedToken(o.token)
	}
	err = run(ctx, runOptions{
		wrap:     wrap,
		dataDir:  dataDir,
		key:      key,
		authKind: "none",
		texSeed:  true,
		listen: func() (net.Listener, error) {
			if o.embedded && !o.portSet && o.getenv("REFRACT_ADDR") == "" {
				return listenTCP(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}) // a free port
			}
			return listenLocal(o.getenv("REFRACT_ADDR"), o.port, o.portSet, listenTCP)
		},
		ready: func(ctx context.Context, addr net.Addr) {
			u := "http://" + addr.String()
			if err := lock.Record(u); err != nil {
				slog.Warn("can't record the address in the lock file; a second refract local won't find this one", "err", err)
			}
			waitCtx, stopWait := context.WithTimeout(ctx, localStartTimeout)
			defer stopWait()
			if !localHealthy(waitCtx, u+"/healthz") {
				if ctx.Err() == nil {
					slog.Error("Refract is listening but doesn't answer; the browser was not opened", "url", u, "waited", localStartTimeout.String())
					if o.embedded {
						readyErr = fmt.Errorf("Refract is listening on %s but doesn't answer after %s", u, localStartTimeout)
						cancel() // stop the server: the parent waits for this error line
					}
				}
				return
			}
			if o.embedded {
				printJSON(o.out, instanceStatus{State: stateReady, URL: u, PID: os.Getpid(), Version: version, DataDir: dataDir, Lock: lock.f.Name()})
				readyPrinted.Store(true)
				return
			}
			fmt.Fprintf(o.out, "\nRefract is running at %s\nData folder: %s\nThere is no sign-in: only this computer can open it. Press Ctrl+C to stop.\n\n", u, dataDir)
			o.open(u)
		},
	})
	if err == nil && readyErr != nil {
		return readyErr
	}
	if err == nil && o.embedded && !readyPrinted.Load() {
		// Stopped (standard input closed, SIGTERM) before it answered: the
		// parent still gets its one line.
		return errors.New("stopped before Refract was ready")
	}
	return err
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

// resolveLocalKey finds the secret key of a data folder. An existing
// secret.key is kept as it is (nothing migrates silently). Otherwise the key
// lives in the system credential store, and in an owner-only secret.key when
// the store can't be reached. A missing store item for a folder that already
// has a database means the old key is gone: a new one is made and the log
// says stored credentials can't be read. The key is never logged, but the log
// says where it is and names the store account even when it is in the file,
// so it can be moved between the two (see the README).
func resolveLocalKey(ks localkey.Store, dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, localKeyFile)
	if _, err := os.Lstat(path); err == nil {
		return readKeyFileLogged(path, dataDir)
	}
	if ks == nil {
		return loadOrCreateKey(path)
	}
	// Two first starts at once would each make a key and the last save would
	// win, leaving the other's credentials unreadable: one at a time decides.
	release, err := lockKeyCreation(dataDir)
	if err != nil {
		return nil, err
	}
	defer release()
	if _, err := os.Lstat(path); err == nil { // the other start fell back to the file
		return readKeyFileLogged(path, dataDir)
	}
	key, found, err := localkey.Load(ks, dataDir)
	switch {
	case found:
		slog.Info("using the secret key in the system credential store", "service", localkey.Service, "account", localkey.Account(dataDir))
		return key, nil
	case errors.Is(err, localkey.ErrInvalid):
		return nil, err
	case err != nil:
		if usedBefore(dataDir) {
			// The key of a folder used before, without a secret.key, is most
			// likely in the store that didn't answer (locked, or a prompt
			// left waiting). A new key would make the stored credentials
			// unreadable for good, so don't make one without being asked.
			return nil, fmt.Errorf("can't read the secret key of %s from the system credential store (%v). Unlock it and start Refract again. If the key is lost, create a new one as %s (openssl rand -base64 32 > %q; chmod 600 %q): stored ChatGPT sign-ins, API keys and git tokens then can't be read", dataDir, err, localKeyFile, path, path)
		}
		slog.Warn("the system credential store is not available; keeping the secret key in an owner-only file instead", "file", path, "err", err)
		return loadOrCreateKey(path)
	}
	warnIfCredentialsLost(dataDir, "the secret key is not in the system credential store")
	key = make([]byte, 32)
	rand.Read(key)
	if err := localkey.Save(ks, dataDir, key); err != nil {
		// Save the same key: a store call that timed out may still succeed
		// later, and its item must then match the file.
		slog.Warn("can't save the secret key in the system credential store; keeping it in an owner-only file instead", "file", path, "err", err)
		return loadOrSaveKey(path, key)
	}
	slog.Info("created the secret key in the system credential store", "service", localkey.Service, "account", localkey.Account(dataDir))
	return key, nil
}

// readKeyFileLogged reads an existing secret.key and logs that it is used,
// with the store account the key would have there.
func readKeyFileLogged(path, dataDir string) ([]byte, error) {
	key, err := readKeyFile(path)
	if err == nil {
		slog.Info("using the secret key file", "file", path, "account", localkey.Account(dataDir))
	}
	return key, err
}

// localKeyLockFile is locked while a start finds or creates the key of its
// data folder.
const localKeyLockFile = "secret.key.lock"

// keyLockWait bounds the wait for another start that holds the key lock; its
// credential store calls take up to 10 s each.
var keyLockWait = 30 * time.Second

// lockKeyCreation takes the key lock of dataDir, waiting while another
// process holds it. The kernel drops the lock if the process ends.
func lockKeyCreation(dataDir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(dataDir, localKeyLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the key lock: %w", err)
	}
	deadline := time.Now().Add(keyLockWait)
	for {
		held, err := lockFile(f)
		switch {
		case err != nil:
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", f.Name(), err)
		case !held:
			return func() { f.Close() }, nil
		case time.Now().After(deadline):
			f.Close()
			return nil, fmt.Errorf("another refract local has been finding the secret key of %s for %v; stop it and start again", dataDir, keyLockWait)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// warnIfCredentialsLost reports that a new key is about to be made for a data
// folder that has been used before, so credentials stored with the old one
// (ChatGPT sign-in, API keys, git tokens) can't be read back.
func warnIfCredentialsLost(dataDir, why string) {
	if usedBefore(dataDir) {
		slog.Warn("this data folder was used before: "+why+", so a new key is made and stored ChatGPT sign-ins, API keys and git tokens can't be read; add them again in Settings, or restore the old key (see the README)", "data", dataDir)
	}
}

// usedBefore reports whether dataDir already has a database.
func usedBefore(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "refract.db"))
	return err == nil
}

// loadOrCreateKey reads the secret key at path, or creates it with a new
// random key on first run. It never replaces an existing file, even an
// invalid one: credentials stored with that key would be lost.
func loadOrCreateKey(path string) ([]byte, error) { return loadOrSaveKey(path, nil) }

// loadOrSaveKey is loadOrCreateKey saving newKey, or a random key when it is
// nil, when there is no file yet.
func loadOrSaveKey(path string, newKey []byte) ([]byte, error) {
	for range 2 {
		key, err := readKeyFile(path)
		if !errors.Is(err, fs.ErrNotExist) {
			return key, err
		}
		key = newKey
		if key == nil {
			key = make([]byte, 32)
			rand.Read(key)
		}
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

// reportLocalStatus prints the instance status as one JSON line. It creates
// and changes nothing. It returns nil only when a refract local is running
// and answering, an errExit with statusExitStarting while one is starting,
// and with 1 otherwise.
func reportLocalStatus(ctx context.Context, out io.Writer, dataDir string) error {
	held, err := lockHeld(dataDir)
	if err != nil {
		return err
	}
	st := instanceStatus{State: stateStopped, DataDir: dataDir, Lock: filepath.Join(dataDir, localLockFile)}
	if held {
		st = checkInstance(ctx, dataDir, 0)
	}
	b, _ := json.Marshal(st)
	fmt.Fprintf(out, "%s\n", b)
	switch st.State {
	case stateRunning:
		return nil
	case stateStarting:
		return errExit{code: statusExitStarting}
	}
	return errExit{code: 1}
}
