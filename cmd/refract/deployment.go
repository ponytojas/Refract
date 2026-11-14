package main

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ponytojas/Refract/internal/mode"
)

// checkDeployment refuses configurations that are unsafe for the hosted
// service. A self-hosted install is never refused here. authMode is the
// REFRACT_AUTH value after its default is applied.
func checkDeployment(m mode.Mode, authMode, publicURL, sandbox string, maxWait, compileTimeout time.Duration) error {
	if m != mode.SaaS {
		return nil
	}
	if authMode == "none" {
		return errors.New("REFRACT_MODE=saas can't run with REFRACT_AUTH=none: every visitor would be the same account. Set REFRACT_AUTH=password")
	}
	if publicURL == "" {
		return errors.New("REFRACT_MODE=saas needs REFRACT_PUBLIC_URL, the address users open Refract at (e.g. https://refract.example.com)")
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || !secureOrigin(u) {
		return fmt.Errorf("REFRACT_PUBLIC_URL=%q: the hosted service needs an https address", publicURL)
	}
	if sandbox != "bwrap" {
		return errors.New("REFRACT_MODE=saas needs REFRACT_SANDBOX=bwrap: without it a document can read other accounts' files")
	}
	// A compile is one request, and Cloudflare cuts a request with no response at about 100 s.
	if maxWait+compileTimeout > proxyLimit {
		hint := "Lower REFRACT_COMPILE_MAX_WAIT or REFRACT_COMPILE_TIMEOUT"
		if maxWait+60*time.Second <= proxyLimit {
			hint = "Set REFRACT_COMPILE_TIMEOUT=60"
		}
		return fmt.Errorf("REFRACT_COMPILE_MAX_WAIT (%ds) plus REFRACT_COMPILE_TIMEOUT (%ds) is over %ds: a proxy would cut the request first. %s", int(maxWait.Seconds()), int(compileTimeout.Seconds()), int(proxyLimit.Seconds()), hint)
	}
	return nil
}

// proxyLimit is the longest a compile request may take (its wait plus the
// compile), under the roughly 100 s after which a proxy gives up on it.
const proxyLimit = 95 * time.Second

// secureOrigin reports whether u is https, or http on this machine, so the
// hosted mode can be tried locally.
func secureOrigin(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
			return true
		}
	}
	return false
}
