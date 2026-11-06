package policy

import (
	"errors"
	"net/netip"
	"testing"
)

func TestCheck(t *testing.T) {
	own := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:10443")}
	deny := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fc00::/7"),
	}
	c := New(own, deny)

	tests := []struct {
		name string
		dst  string
		want error
	}{
		{"public v4 ok", "1.1.1.1:443", nil},
		{"public v6 ok", "[2606:4700::1111]:443", nil},
		{"unspecified invalid", "0.0.0.0:443", ErrInvalid},
		{"loopback invalid", "127.0.0.1:443", ErrInvalid},
		{"link-local invalid", "169.254.1.1:80", ErrInvalid},
		{"multicast invalid", "224.0.0.1:80", ErrInvalid},
		{"v4-mapped invalid", "[::ffff:1.1.1.1]:443", ErrInvalid},
		{"port zero invalid", "1.1.1.1:0", ErrInvalid},
		{"own address loop", "192.0.2.1:10443", ErrLoop},
		{"own address mapped loop", "[::ffff:192.0.2.1]:10443", ErrInvalid},
		{"own address other port ok", "192.0.2.1:443", nil},
		{"deny v4", "10.1.2.3:22", ErrDenied},
		{"deny v6", "[fd00::1]:22", ErrDenied},
		{"invalid beats deny", "10.0.0.0:0", ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := c.Check(netip.MustParseAddrPort(tc.dst))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check(%s) = %v, want nil", tc.dst, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check(%s) = %v, want %v", tc.dst, err, tc.want)
			}
		})
	}
}

func TestMappedOwnEntryLoop(t *testing.T) {
	c := New([]netip.AddrPort{netip.MustParseAddrPort("[::ffff:192.0.2.1]:10443")}, nil)
	if err := c.Check(netip.MustParseAddrPort("192.0.2.1:10443")); !errors.Is(err, ErrLoop) {
		t.Fatalf("got %v, want ErrLoop", err)
	}
}

func TestNewCopiesInputs(t *testing.T) {
	own := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:10443")}
	deny := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	c := New(own, deny)
	own[0] = netip.MustParseAddrPort("9.9.9.9:1")
	deny[0] = netip.MustParsePrefix("11.0.0.0/8")
	if err := c.Check(netip.MustParseAddrPort("192.0.2.1:10443")); !errors.Is(err, ErrLoop) {
		t.Fatalf("own mutated: %v", err)
	}
	if err := c.Check(netip.MustParseAddrPort("10.1.2.3:22")); !errors.Is(err, ErrDenied) {
		t.Fatalf("deny mutated: %v", err)
	}
}

func TestNilAllowsEverythingValid(t *testing.T) {
	c := New(nil, nil)
	if err := c.Check(netip.MustParseAddrPort("10.1.2.3:22")); err != nil {
		t.Fatalf("got %v", err)
	}
	if err := c.Check(netip.MustParseAddrPort("127.0.0.1:22")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}
