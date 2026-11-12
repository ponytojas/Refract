package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/netguard"
)

func get(hc *http.Client, u string) error {
	resp, err := hc.Get(u)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func TestHTTPClientPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	if err := get(HTTPClient(true), srv.URL); err != nil {
		t.Fatalf("self-hosted can't reach loopback: %v", err)
	}
	if err := get(HTTPClient(false), srv.URL); !errors.Is(err, netguard.ErrPrivate) {
		t.Fatalf("hosted reached loopback: %v", err)
	}
	u, _ := url.Parse(srv.URL)
	u.Host = "localhost:" + u.Port()
	if err := get(HTTPClient(false), u.String()); !errors.Is(err, netguard.ErrPrivate) {
		t.Fatalf("hosted reached localhost: %v", err)
	}
}

func TestHTTPClientChecksRedirects(t *testing.T) {
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer refused.Close()
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, refused.URL, http.StatusFound)
		}
	}))
	defer allowed.Close()
	port := netip.MustParseAddrPort(strings.TrimPrefix(allowed.URL, "http://")).Port()
	hc := httpClient(netguard.ControlWith(func(ap netip.AddrPort) bool { return ap.Port() == port }), false)

	if err := get(hc, allowed.URL); err != nil {
		t.Fatalf("the allowed server is refused: %v", err)
	}
	if err := get(hc, allowed.URL+"/redirect"); !errors.Is(err, netguard.ErrPrivate) {
		t.Fatalf("a redirect reached a refused address: %v", err)
	}
}

func TestPrivateNetworkErrorIsReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	_, err := ListModels(context.Background(), HTTPClient(false), Config{ID: "c", Kind: "compat", BaseURL: srv.URL})
	var ae *ai.Error
	if !errors.As(err, &ae) || ae.Kind != ai.ErrOther || !strings.Contains(ae.Message, "private network") || strings.Contains(ae.Message, "127.0.0.1") {
		t.Fatalf("ListModels: %#v", err)
	}
	ae = mapError(errors.Join(errors.New("post"), netguard.ErrPrivate), Config{Kind: "compat"})
	if ae.Kind != ai.ErrOther || !strings.Contains(ae.Message, "private network") {
		t.Fatalf("mapError: %#v", ae)
	}
}

func TestCheckPublic(t *testing.T) {
	for _, c := range []Config{
		{Kind: "ollama"},
		{Kind: "compat", BaseURL: "http://api.example.com/v1"},
		{Kind: "compat", BaseURL: "https://10.0.0.5/v1"},
		{Kind: "compat", BaseURL: "https://[::1]/v1"},
		{Kind: "compat", BaseURL: "https://169.254.169.254/latest"},
		{Kind: "openai", BaseURL: "http://api.openai.com/v1"},
	} {
		if c.CheckPublic() == nil {
			t.Errorf("CheckPublic accepted %+v", c)
		}
	}
	for _, c := range []Config{
		{Kind: "openai"},
		{Kind: "anthropic"},
		{Kind: "compat", BaseURL: "https://api.example.com/v1"},
		{Kind: "compat", BaseURL: "https://8.8.8.8/v1"},
	} {
		if err := c.CheckPublic(); err != nil {
			t.Errorf("CheckPublic(%+v): %v", c, err)
		}
	}
	if err := (Config{Kind: "compat", BaseURL: "https://10.0.0.5"}).CheckPublic(); !errors.Is(err, netguard.ErrPrivate) {
		t.Errorf("a private literal IP: %v", err)
	}
}

func TestKindsFor(t *testing.T) {
	if !slices.EqualFunc(KindsFor(true), Kinds, func(a, b Kind) bool { return a.ID == b.ID }) {
		t.Error("self-hosted must offer every kind")
	}
	hosted := KindsFor(false)
	if len(hosted) != len(Kinds)-1 || slices.ContainsFunc(hosted, func(k Kind) bool { return k.ID == "ollama" }) {
		t.Errorf("the hosted service offers %v", hosted)
	}
}
