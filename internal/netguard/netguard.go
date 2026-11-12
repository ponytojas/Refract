// Package netguard decides which addresses the server may connect to on
// someone's behalf. When a user supplies an address (an AI provider's base URL,
// a git remote), the hosted service must not reach its own network: loopback,
// private ranges, cloud metadata services and the like (SSRF).
//
// The check runs at dial time (Control), on the address actually being
// connected to, so DNS answers, DNS rebinding and redirects are all covered.
package netguard

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

// ErrPrivate is returned when a connection to a refused address is attempted.
var ErrPrivate = errors.New("this address is on a private network, which the hosted service can't reach")

// refused lists ranges that are neither private nor caught by the netip
// predicates, but still aren't public destinations.
var refused = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, includes broadcast
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64, can carry any IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4, can carry any IPv4 address
	netip.MustParsePrefix("2001::/32"),      // Teredo, can carry any IPv4 address
}

// Allowed reports whether a is a public unicast address.
func Allowed(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range refused {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Control returns a net.Dialer Control function. Link-local addresses (cloud
// metadata services such as 169.254.169.254) are always refused; with
// private=false, so is everything Allowed rejects.
func Control(private bool) func(network, address string, c syscall.RawConn) error {
	if private {
		return ControlWith(nil)
	}
	return ControlWith(func(ap netip.AddrPort) bool { return Allowed(ap.Addr()) })
}

// ControlWith is Control with a custom rule, for tests: allow, when not nil,
// decides which addresses may be dialled.
func ControlWith(allow func(netip.AddrPort) bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("refusing to connect to %q: %w", address, err)
		}
		ap = netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())
		if allow != nil && !allow(ap) {
			return fmt.Errorf("%w (%s)", ErrPrivate, ap.Addr())
		}
		if a := ap.Addr(); a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
			return fmt.Errorf("refusing to connect to link-local address %s", a)
		}
		return nil
	}
}
