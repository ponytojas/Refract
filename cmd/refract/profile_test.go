package main

import (
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	for v, want := range map[string]profile{"": profilePersonal, "personal": profilePersonal, "team": profileTeam} {
		if got, err := parseProfile(v); err != nil || got != want {
			t.Errorf("parseProfile(%q) = %q, %v", v, got, err)
		}
	}
	for _, bad := range []string{"Team", "teams"} {
		if _, err := parseProfile(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestTeamProfile(t *testing.T) {
	// The team profile turns the sandbox on when it isn't set.
	cfg := compileConfig{sandbox: "none"}
	if err := applyProfile(profileTeam, "builtin", &cfg, false); err != nil || cfg.sandbox != "bwrap" {
		t.Errorf("team, sandbox unset: %q, %v", cfg.sandbox, err)
	}
	cfg = compileConfig{sandbox: "bwrap"}
	if err := applyProfile(profileTeam, "cloudflare", &cfg, true); err != nil {
		t.Errorf("team with bwrap: %v", err)
	}
	// It refuses what wouldn't isolate accounts, saying what to change.
	cfg = compileConfig{sandbox: "none"}
	if err := applyProfile(profileTeam, "builtin", &cfg, true); err == nil || !strings.Contains(err.Error(), "REFRACT_SANDBOX=none") {
		t.Errorf("team with REFRACT_SANDBOX=none: %v", err)
	}
	cfg = compileConfig{sandbox: "bwrap"}
	if err := applyProfile(profileTeam, "none", &cfg, true); err == nil || !strings.Contains(err.Error(), "REFRACT_AUTH") {
		t.Errorf("team with REFRACT_AUTH=none: %v", err)
	}
	// The personal profile changes nothing.
	cfg = compileConfig{sandbox: "none"}
	if err := applyProfile(profilePersonal, "builtin", &cfg, false); err != nil || cfg.sandbox != "none" {
		t.Errorf("personal: %q, %v", cfg.sandbox, err)
	}
}

func TestUnisolatedWarning(t *testing.T) {
	if unisolatedWarning(profilePersonal, "builtin", "none") == "" {
		t.Error("several accounts without the sandbox: no warning")
	}
	for _, c := range []struct {
		p             profile
		auth, sandbox string
	}{{profilePersonal, "none", "none"}, {profilePersonal, "builtin", "bwrap"}, {profileTeam, "builtin", "bwrap"}} {
		if w := unisolatedWarning(c.p, c.auth, c.sandbox); w != "" {
			t.Errorf("%+v: %q", c, w)
		}
	}
}
