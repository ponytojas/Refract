package main

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ponytojas/Refract/internal/siwc"
)

// listenAddr is REFRACT_ADDR, or 127.0.0.1:8080 without it: this computer
// only. The container image sets REFRACT_ADDR=:8080; there, publishing the
// port with -p 127.0.0.1:… keeps the host-side mapping on loopback.
func listenAddr(getenv func(string) string) string {
	return cmp.Or(getenv("REFRACT_ADDR"), "127.0.0.1:8080")
}

// hostPolicy says which Host headers the server answers. A page on another
// domain can make that domain resolve to 127.0.0.1 (DNS rebinding); the
// browser then treats the local server as that domain's own and lets the page
// send X-Refract. Checking Host closes that: the attacker's name isn't allowed.
type hostPolicy struct {
	names      map[string]bool // lowercased, without port, brackets or trailing dot
	configured bool            // REFRACT_PUBLIC_URL or REFRACT_ALLOWED_HOSTS named hosts
	// any accepts every Host: a listener other computers can reach with
	// nothing configured, where refusing would break installs reached by
	// names Refract can't know. Set by forListener.
	any bool
}

// newHostPolicy reads REFRACT_PUBLIC_URL and REFRACT_ALLOWED_HOSTS. A value
// that is set but invalid is an error, so a typo can't loosen the check.
func newHostPolicy(publicURL, allowed string) (hostPolicy, error) {
	p := hostPolicy{names: map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}}
	if publicURL != "" {
		base, err := publicBase(publicURL)
		if err != nil {
			return hostPolicy{}, err
		}
		u, _ := url.Parse(base)
		name, ok := parseAuthority(u.Host)
		if !ok || u.User != nil {
			return hostPolicy{}, errors.New("REFRACT_PUBLIC_URL must be the address users open Refract at (e.g. https://refract.example.com)")
		}
		p.names[name] = true
		p.configured = true
	}
	for entry := range strings.SplitSeq(allowed, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, ok := parseAuthority(entry)
		if !ok {
			return hostPolicy{}, fmt.Errorf("REFRACT_ALLOWED_HOSTS: %q is not a host name or IP address (list names like refract.lan or 192.168.1.20, separated by commas)", entry)
		}
		p.names[name] = true
		p.configured = true
	}
	return p, nil
}

// forListener finishes the policy once the server listens on addr, judged by
// the address actually bound, not by how REFRACT_ADDR spells it. authKind is
// REFRACT_AUTH after authMode. warning, when not empty, is for the log.
func (p *hostPolicy) forListener(addr net.Addr, authKind string) (warning string, err error) {
	if p.configured || loopbackListener(addr) {
		return "", nil
	}
	if authKind == "none" {
		return "", fmt.Errorf("REFRACT_AUTH=none listens on %s, which other computers can reach, and there is no sign-in to stop them. Listen on 127.0.0.1 only (REFRACT_ADDR=127.0.0.1:8080), or, if the address is already limited to this computer (docker run -p 127.0.0.1:8080:8080), list the names you open it at in REFRACT_ALLOWED_HOSTS (e.g. localhost)", addr)
	}
	p.any = true
	return "Refract accepts requests for any host name because it listens on " + addr.String() + " without REFRACT_PUBLIC_URL or REFRACT_ALLOWED_HOSTS. Set one of them to the names people open Refract at, to protect against DNS rebinding.", nil
}

func loopbackListener(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// parseAuthority reads a host with an optional port, as in a Host header or a
// REFRACT_ALLOWED_HOSTS entry, and returns the normalized name without the
// port. Brackets are only for IPv6 addresses; anything malformed is refused.
func parseAuthority(s string) (string, bool) {
	if ip := net.ParseIP(s); ip != nil { // an IP address without port; IPv6 needs brackets for one
		return ip.String(), true
	}
	if strings.HasPrefix(s, "[") {
		inside, rest, ok := strings.Cut(s[1:], "]")
		if !ok || (rest != "" && !(rest[0] == ':' && validPort(rest[1:]))) {
			return "", false
		}
		ip := net.ParseIP(inside)
		if ip == nil || ip.To4() != nil {
			return "", false
		}
		return ip.String(), true
	}
	name := s
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		if !validPort(s[i+1:]) {
			return "", false
		}
		name = s[:i]
	}
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if ip := net.ParseIP(name); ip != nil {
		return ip.String(), true
	}
	return name, validHostName(name)
}

func validPort(p string) bool {
	_, err := strconv.ParseUint(p, 10, 16)
	return err == nil
}

func validHostName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (p hostPolicy) allows(name string) bool {
	return p.names[name] || strings.HasSuffix(name, ".localhost")
}

const foreignOrigin = "Refract refuses changes requested by a page on another site."

// check returns the status and message for a request the policy refuses, or 0.
func (p hostPolicy) check(r *http.Request) (int, string) {
	host, ok := parseAuthority(r.Host)
	if !ok {
		return http.StatusBadRequest, "Malformed Host header."
	}
	if !p.any && !p.allows(host) {
		return http.StatusMisdirectedRequest, "Refract doesn't answer to the host name " + host + ". If this is the address you use, add it to REFRACT_ALLOWED_HOSTS or set REFRACT_PUBLIC_URL."
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return 0, ""
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return 0, ""
	}
	origin, ok := parseOrigin(origins)
	// With any host accepted, the origin must at least be the host asked for.
	if ok && (origin == host || (!p.any && p.allows(origin))) {
		return 0, ""
	}
	return http.StatusForbidden, foreignOrigin
}

// parseOrigin returns the host name of the only Origin value when it is a
// well-formed http or https origin: scheme and authority, nothing else.
func parseOrigin(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	u, err := url.Parse(values[0])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Host == "" {
		return "", false
	}
	return parseAuthority(u.Host)
}

// middleware refuses requests for host names the policy doesn't allow, and
// changes from pages on other sites.
func (p hostPolicy) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status, msg := p.check(r); status != 0 {
			http.Error(w, msg, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newServer wraps every route, including the ones served before sign-in, in
// the host check and the security headers. Go's own answer to "OPTIONS *" is
// off, so no request skips the check.
func newServer(mux http.Handler, hosts hostPolicy) *http.Server {
	return &http.Server{
		Handler:                      securityHeaders(hosts.middleware(mux)),
		ReadHeaderTimeout:            10 * time.Second,
		DisableGeneralOptionsHandler: true,
	}
}

// loginsFor is the in-server ChatGPT sign-in for a server listening on addr:
// only on loopback, where the browser is on this computer. A container that
// listens on all its interfaces gets none: OpenAI's redirect to the host's
// 127.0.0.1 can't reach it.
func loginsFor(addr net.Addr) *siwc.Logins {
	if tcp, ok := addr.(*net.TCPAddr); ok && tcp.IP.IsLoopback() {
		// The server opens the browser itself: a page's window.open after a
		// request is a popup that browsers and the desktop app's web view may drop.
		return &siwc.Logins{OpenBrowser: openBrowser}
	}
	return nil
}

// fromThisComputer reports whether a request comes straight from a browser on
// the computer Refract runs on: a loopback peer, a loopback host name and no
// proxy headers. A loopback listener can also sit behind a reverse proxy for
// people elsewhere, and their browser can't reach this computer's callback.
func fromThisComputer(r *http.Request) bool {
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Real-Ip"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(peer); err != nil || ip == nil || !ip.IsLoopback() {
		return false
	}
	host, ok := parseAuthority(r.Host)
	if !ok {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}
