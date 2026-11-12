package netguard

import (
	"errors"
	"net/netip"
	"testing"
)

func TestAllowed(t *testing.T) {
	refused := []string{
		"0.0.0.0", "0.1.2.3", "127.0.0.1", "127.255.255.254",
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.127.255.255",
		"192.0.0.8", "198.18.0.1", "198.19.255.255",
		"224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fd00::1", "fc00::1", "fe80::1", "fe80::1%eth0", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "64:ff9b::808:808", "64:ff9b:1::1",
		"2002:7f00:1::1", "2001:0:4136:e378::1",
	}
	for _, s := range refused {
		if Allowed(netip.MustParseAddr(s)) {
			t.Errorf("Allowed(%s) = true, want false", s)
		}
	}
	if Allowed(netip.Addr{}) {
		t.Error("the zero Addr is allowed")
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "172.32.0.1", "172.15.255.255", "11.0.0.1",
		"100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1",
		"192.0.1.1", "223.255.255.254",
		"2606:4700:4700::1111", "2001:4860:4860::8888", "::ffff:8.8.8.8",
	}
	for _, s := range allowed {
		if !Allowed(netip.MustParseAddr(s)) {
			t.Errorf("Allowed(%s) = false, want true", s)
		}
	}
}

func TestControl(t *testing.T) {
	self := Control(true)
	for _, a := range []string{"169.254.169.254:80", "[fe80::1%en0]:80", "[::ffff:169.254.169.254]:80"} {
		if err := self("tcp", a, nil); err == nil {
			t.Errorf("Control(true) allowed %s", a)
		}
	}
	for _, a := range []string{"127.0.0.1:80", "10.0.0.1:80", "[::1]:11434", "8.8.8.8:443"} {
		if err := self("tcp", a, nil); err != nil {
			t.Errorf("Control(true) refused %s: %v", a, err)
		}
	}

	hosted := Control(false)
	for _, a := range []string{"127.0.0.1:80", "[::1]:80", "10.0.0.1:443", "169.254.169.254:80", "[::ffff:127.0.0.1]:80"} {
		if err := hosted("tcp", a, nil); !errors.Is(err, ErrPrivate) {
			t.Errorf("Control(false)(%s) = %v, want ErrPrivate", a, err)
		}
	}
	if err := hosted("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("Control(false) refused a public address: %v", err)
	}

	if err := self("tcp", "nonsense:80", nil); err == nil {
		t.Error("an address that isn't an IP was allowed")
	}
}
