package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/ponytojas/Refract/internal/tools"
)

// parseOutbound reads REFRACT_OUTBOUND: whether addresses people supply (AI
// provider base URLs, git remotes) may reach loopback and private networks.
// "any" (the default) allows them; "public" refuses them. It reports whether
// only public addresses are allowed.
func parseOutbound(v string) (publicOnly bool, err error) {
	switch v {
	case "", "any":
		return false, nil
	case "public":
		return true, nil
	default:
		return false, fmt.Errorf("REFRACT_OUTBOUND=%q: want any or public", v)
	}
}

var gitVersionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// gitAtLeast237 reports whether `git --version` output names git 2.37 or
// later, which pinning git remotes to checked addresses needs.
func gitAtLeast237(out string) bool {
	m := gitVersionRe.FindStringSubmatch(out)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 2 || (major == 2 && minor >= 37)
}

// checkGitForPinning refuses to start with REFRACT_OUTBOUND=public on a git
// older than 2.37. A git that is missing is reported by logTools instead.
func checkGitForPinning() error {
	bin, err := tools.Path("git")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("REFRACT_OUTBOUND=public: can't run git --version: %w", err)
	}
	if !gitAtLeast237(string(out)) {
		return fmt.Errorf("REFRACT_OUTBOUND=public needs git 2.37 or later, found %q", string(out))
	}
	return nil
}
