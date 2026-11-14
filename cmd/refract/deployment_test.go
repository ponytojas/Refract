package main

import (
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/mode"
)

func TestCheckDeployment(t *testing.T) {
	for _, tc := range []struct {
		mode      mode.Mode
		auth      string
		publicURL string
		want      string // part of the refusal; empty means the start is allowed
	}{
		// A self-hosted install is never refused, whatever the rest says.
		{mode.SelfHost, "none", "", ""},
		{mode.SelfHost, "builtin", "not a url", ""},
		{mode.SelfHost, "none", "http://r.example.com/app", ""},

		{mode.SaaS, "none", "https://r.example.com", "REFRACT_AUTH=none"},
		{mode.SaaS, "builtin", "", "REFRACT_PUBLIC_URL"},
		{mode.SaaS, "builtin", "http://r.example.com", "https address"},
		{mode.SaaS, "builtin", "not a url", "https address"},
		{mode.SaaS, "builtin", "https://", "https address"},
		{mode.SaaS, "builtin", "r.example.com", "https address"},
		{mode.SaaS, "builtin", "https://r.example.com/app", "https address"},
		{mode.SaaS, "builtin", "http://localhost.example.com", "https address"},
		{mode.SaaS, "builtin", "https://r.example.com", ""},
		{mode.SaaS, "builtin", "https://r.example.com/", ""},
		{mode.SaaS, "builtin", "http://localhost:8080", ""},
		{mode.SaaS, "builtin", "http://127.0.0.1:8080", ""},
		{mode.SaaS, "builtin", "http://[::1]:8080", ""},
		{mode.SaaS, "cloudflare", "https://r.example.com", ""},
	} {
		err := checkDeployment(tc.mode, tc.auth, tc.publicURL)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("checkDeployment(%s, %q, %q) = %v; want it allowed", tc.mode, tc.auth, tc.publicURL, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("checkDeployment(%s, %q, %q) = %v; want a refusal naming %q", tc.mode, tc.auth, tc.publicURL, err, tc.want)
		}
	}
}
