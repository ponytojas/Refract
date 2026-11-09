package projects

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

// fakeDNS makes lookupIP answer from names for the test's duration.
func fakeDNS(t *testing.T, names map[string][]string) {
	t.Helper()
	old := lookupIP
	t.Cleanup(func() { lookupIP = old })
	lookupIP = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		if s, ok := names[host]; ok {
			var out []netip.Addr
			for _, a := range s {
				out = append(out, netip.MustParseAddr(a))
			}
			return out, nil
		}
		return old(ctx, network, host)
	}
}

func TestPublicRemotes(t *testing.T) {
	fakeDNS(t, map[string][]string{"mixed.example.com": {"93.184.215.14", "10.0.0.5"}})
	s, id := newTurnProject(t)
	s.Key = make([]byte, 32)
	urls := []string{
		"https://127.0.0.1/a.git",
		"https://[::1]/a.git",
		"https://localhost:8443/a.git",
		"https://169.254.169.254/a.git",
		"https://mixed.example.com/a.git",
	}
	for _, u := range urls {
		if err := s.SetRemote(ownerCtx, id, Remote{URL: u}, true); err != nil {
			t.Errorf("self-hosted refused %s: %v", u, err)
		}
	}
	s.PublicRemotes = true
	for _, u := range urls {
		if err := s.SetRemote(ownerCtx, id, Remote{URL: u}, true); !errors.Is(err, ErrPrivateRemote) {
			t.Errorf("hosted saved %s: %v", u, err)
		}
	}
	if err := s.SetRemote(ownerCtx, id, Remote{URL: "https://github.com./a.git"}, true); !errors.Is(err, ErrBadRemote) {
		t.Errorf("a host with a trailing dot: %v", err)
	}
}

func TestPinRemote(t *testing.T) {
	fakeDNS(t, map[string][]string{"git.example.com": {"93.184.215.14", "::ffff:93.184.215.14", "2606:2800::1"}})
	s := &Service{PublicRemotes: true}
	ctx := context.Background()
	for _, tc := range []struct {
		url  string
		want []string
	}{
		{"https://git.example.com/a.git", []string{"-c", "http.followRedirects=false", "-c", "http.curloptResolve=git.example.com:443:93.184.215.14,[2606:2800::1]"}},
		{"https://git.example.com:8443/a.git", []string{"-c", "http.followRedirects=false", "-c", "http.curloptResolve=git.example.com:8443:93.184.215.14,[2606:2800::1]"}},
		{"https://93.184.215.14/a.git", []string{"-c", "http.followRedirects=false"}},
	} {
		got, err := s.pinRemote(ctx, tc.url)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("pinRemote(%s) = %q, %v; want %q", tc.url, got, err, tc.want)
		}
	}
	if _, err := s.pinRemote(ctx, "https://nowhere.invalid/a.git"); err == nil || errors.Is(err, ErrPrivateRemote) {
		t.Errorf("an unknown host: %v", err)
	}
	if got, err := (&Service{}).pinRemote(ctx, "https://127.0.0.1/a.git"); got != nil || err != nil {
		t.Errorf("self-hosted pins %q, %v", got, err)
	}
}

func TestPushRefusesPrivateRemote(t *testing.T) {
	s, id := newTurnProject(t)
	s.Key = make([]byte, 32)
	if err := s.SetRemote(ownerCtx, id, Remote{URL: "https://127.0.0.1:1/a.git"}, true); err != nil {
		t.Fatal(err) // saved while self-hosted
	}
	s.PublicRemotes = true
	if err := s.Push(ownerCtx, id); !errors.Is(err, ErrPrivateRemote) {
		t.Errorf("Push: %v", err)
	}
	if _, err := s.Pull(ownerCtx, id); !errors.Is(err, ErrPrivateRemote) {
		t.Errorf("Pull: %v", err)
	}
}
