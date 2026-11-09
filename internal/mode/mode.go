// Package mode says whether Refract runs as the hosted service or as a
// self-hosted install. It is read once at startup (REFRACT_MODE) and passed
// to whatever behaves differently; nothing else reads the environment for it.
package mode

import "fmt"

type Mode string

const (
	SelfHost Mode = "selfhost" // the default: one person's or one team's install
	SaaS     Mode = "saas"     // the hosted service
)

// Parse reads a REFRACT_MODE value; empty means SelfHost.
func Parse(s string) (Mode, error) {
	switch m := Mode(s); m {
	case "":
		return SelfHost, nil
	case SelfHost, SaaS:
		return m, nil
	default:
		return "", fmt.Errorf("REFRACT_MODE=%q: want selfhost or saas", s)
	}
}

// ChatGPT reports whether people may use their ChatGPT plan. OpenAI's plan
// usage flow covers open-source and self-hosted apps; a hosted service needs
// OpenAI's approval first, so it is self-hosted only.
func (m Mode) ChatGPT() bool { return m == SelfHost }
