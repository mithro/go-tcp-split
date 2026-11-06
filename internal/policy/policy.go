// Package policy applies defence-in-depth checks to a redirected
// connection's original destination before the proxy dials it.
package policy

import (
	"errors"
	"net/netip"
	"slices"
)

// Sentinel errors returned by Check. Callers match them with errors.Is.
var (
	// ErrLoop means the destination is one of the proxy's own listeners.
	ErrLoop = errors.New("destination is a proxy listener")
	// ErrInvalid means the destination is not a dialable unicast address.
	ErrInvalid = errors.New("invalid destination")
	// ErrDenied means the destination matches a deny prefix.
	ErrDenied = errors.New("destination denied by policy")
)

// Checker decides whether a destination may be dialled.
type Checker struct {
	own  []netip.AddrPort
	deny []netip.Prefix
}

// New builds a Checker from the proxy's own listener addresses and the deny
// prefixes. The slices are copied. New(nil, nil) allows every valid address.
func New(own []netip.AddrPort, deny []netip.Prefix) *Checker {
	return &Checker{own: slices.Clone(own), deny: slices.Clone(deny)}
}

// Check returns nil if dst may be dialled, otherwise ErrInvalid, ErrLoop or
// ErrDenied, evaluated in that order.
func (c *Checker) Check(dst netip.AddrPort) error {
	a := dst.Addr()
	if !a.IsValid() || a.IsUnspecified() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsMulticast() || a.IsInterfaceLocalMulticast() || a.Is4In6() || dst.Port() == 0 {
		return ErrInvalid
	}
	u := a.Unmap()
	for _, o := range c.own {
		if o.Addr().Unmap() == u && o.Port() == dst.Port() {
			return ErrLoop
		}
	}
	for _, p := range c.deny {
		// Contains is false for a different address family or a 4-in-6
		// address, which is the behaviour we want.
		if p.Contains(a) {
			return ErrDenied
		}
	}
	return nil
}
