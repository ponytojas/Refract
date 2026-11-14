package main

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/ponytojas/Refract/internal/mode"
)

// checkDeployment refuses configurations that are unsafe for the hosted
// service. A self-hosted install is never refused here. authMode is the
// REFRACT_AUTH value after its default is applied.
func checkDeployment(m mode.Mode, authMode, publicURL string) error {
	if m != mode.SaaS {
		return nil
	}
	if authMode == "none" {
		return errors.New("REFRACT_MODE=saas can't run with REFRACT_AUTH=none: every visitor would be the same account. Set REFRACT_AUTH=builtin")
	}
	if publicURL == "" {
		return errors.New("REFRACT_MODE=saas needs REFRACT_PUBLIC_URL, the address users open Refract at (e.g. https://refract.example.com)")
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || !secureOrigin(u) {
		return fmt.Errorf("REFRACT_PUBLIC_URL=%q: the hosted service needs an https address", publicURL)
	}
	return nil
}

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
