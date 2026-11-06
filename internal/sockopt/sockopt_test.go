package sockopt

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestParseSockaddr(t *testing.T) {
	in6 := make([]byte, 28)
	in6[0] = 10
	in6[2], in6[3] = 0x01, 0xbb
	copy(in6[8:24], netip.MustParseAddr("2606:4700::1111").AsSlice())

	tests := []struct {
		name    string
		in      []byte
		want    string
		wantErr bool
	}{
		{"ipv4", []byte{2, 0, 0x1f, 0x90, 93, 184, 216, 34, 0, 0, 0, 0, 0, 0, 0, 0}, "93.184.216.34:8080", false},
		{"ipv6", in6, "[2606:4700::1111]:443", false},
		{"bad family", []byte{1, 0, 0x1f, 0x90, 93, 184, 216, 34, 0, 0, 0, 0, 0, 0, 0, 0}, "", true},
		{"short v4", make([]byte, 15), "", true},
		{"short v6", append([]byte{10, 0}, make([]byte, 20)...), "", true},
		{"empty", nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSockaddr(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got.String() != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func pair(t *testing.T, addr string) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	t.Cleanup(func() { ln.Close() })
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	srv, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return client.(*net.TCPConn), srv.(*net.TCPConn)
}

func TestOriginalDstNotRedirected(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(addr, func(t *testing.T) {
			_, srv := pair(t, addr)
			got, err := OriginalDst(srv)
			if err == nil {
				// With conntrack loaded, the kernel tracks the connection and
				// reports its own tuple: the "original" destination of a
				// non-redirected connection is its local address.
				want := srv.LocalAddr().(*net.TCPAddr).AddrPort()
				if got != want {
					t.Fatalf("got %s, want local address %s", got, want)
				}
				return
			}
			if !errors.Is(err, syscall.ENOENT) {
				t.Fatalf("err = %v, want wrapping ENOENT", err)
			}
		})
	}
}

func TestSetCongestionRoundTrip(t *testing.T) {
	b, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		t.Skipf("cannot read default congestion control: %v", err)
	}
	want := strings.TrimSpace(string(b))
	_, srv := pair(t, "127.0.0.1:0")
	if err := SetCongestion(srv, want); err != nil {
		t.Fatal(err)
	}
	got, err := Congestion(srv)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
