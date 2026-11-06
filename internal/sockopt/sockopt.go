// Package sockopt reads a redirected connection's original destination and
// gets/sets the TCP congestion control algorithm. Linux only.
package sockopt

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"unsafe"
)

const (
	// soOriginalDst is SO_ORIGINAL_DST (linux/netfilter_ipv4.h) at level
	// SOL_IP. IP6T_SO_ORIGINAL_DST (linux/netfilter_ipv6/ip6_tables.h) has the
	// same value, at level SOL_IPV6.
	soOriginalDst = 80
	// tcpCongestion is TCP_CONGESTION (linux/tcp.h).
	tcpCongestion = 13

	sockaddrInLen  = 16
	sockaddrIn6Len = 28
	afInet         = 2  // AF_INET
	afInet6        = 10 // AF_INET6
)

// ParseSockaddr decodes a raw Linux sockaddr_in (16 bytes) or sockaddr_in6
// (28 bytes) as returned by SO_ORIGINAL_DST. The family field is native-endian
// (little-endian on every supported GOARCH: 386, amd64, arm, arm64,
// riscv64); the port is big-endian.
func ParseSockaddr(b []byte) (netip.AddrPort, error) {
	if len(b) < 2 {
		return netip.AddrPort{}, fmt.Errorf("sockaddr too short: %d bytes", len(b))
	}
	family := uint16(b[0]) | uint16(b[1])<<8
	switch family {
	case afInet:
		if len(b) < sockaddrInLen {
			return netip.AddrPort{}, fmt.Errorf("sockaddr_in too short: %d bytes, need %d", len(b), sockaddrInLen)
		}
		port := uint16(b[2])<<8 | uint16(b[3])
		return netip.AddrPortFrom(netip.AddrFrom4([4]byte(b[4:8])), port), nil
	case afInet6:
		if len(b) < sockaddrIn6Len {
			return netip.AddrPort{}, fmt.Errorf("sockaddr_in6 too short: %d bytes, need %d", len(b), sockaddrIn6Len)
		}
		port := uint16(b[2])<<8 | uint16(b[3])
		return netip.AddrPortFrom(netip.AddrFrom16([16]byte(b[8:24])), port), nil
	default:
		return netip.AddrPort{}, fmt.Errorf("unsupported sockaddr family %d", family)
	}
}

// OriginalDst returns the pre-redirect destination of an accepted connection
// (SO_ORIGINAL_DST / IP6T_SO_ORIGINAL_DST). For a connection that was not
// redirected the returned error wraps syscall.ENOENT.
func OriginalDst(c *net.TCPConn) (netip.AddrPort, error) {
	la, ok := c.LocalAddr().(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("original dst: unexpected local address type %T", c.LocalAddr())
	}
	level := syscall.SOL_IPV6
	if la.IP.To4() != nil {
		level = syscall.SOL_IP
	}
	rc, err := c.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("original dst: syscall conn: %w", err)
	}
	// syscall has no generic byte-buffer getsockopt, and SYS_GETSOCKOPT does
	// not exist on linux/386 (socket calls go through socketcall(2) there).
	// GetsockoptIPv6MTUInfo calls getsockopt portably on every GOARCH with a
	// 32-byte buffer, which holds a sockaddr_in (16) or a sockaddr_in6 (28).
	// The kernel only requires the buffer to be at least that large.
	var info *syscall.IPv6MTUInfo
	var serr error
	if err := rc.Control(func(fd uintptr) {
		info, serr = syscall.GetsockoptIPv6MTUInfo(int(fd), level, soOriginalDst)
	}); err != nil {
		return netip.AddrPort{}, fmt.Errorf("original dst: control: %w", err)
	}
	if serr != nil {
		return netip.AddrPort{}, fmt.Errorf("getsockopt SO_ORIGINAL_DST: %w", serr)
	}
	return ParseSockaddr(asBytes(info))
}

// asBytes views a fixed-size getsockopt result struct as raw bytes.
func asBytes[T any](p *T) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(p)), unsafe.Sizeof(*p))
}

// SetCongestion sets the TCP congestion control algorithm on c.
func SetCongestion(c *net.TCPConn, name string) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return fmt.Errorf("set congestion: syscall conn: %w", err)
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = syscall.SetsockoptString(int(fd), syscall.IPPROTO_TCP, tcpCongestion, name)
	}); err != nil {
		return fmt.Errorf("set congestion: control: %w", err)
	}
	if serr != nil {
		return fmt.Errorf("setsockopt TCP_CONGESTION %q: %w", name, serr)
	}
	return nil
}

// Congestion returns the TCP congestion control algorithm in use on c.
func Congestion(c *net.TCPConn) (string, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return "", fmt.Errorf("congestion: syscall conn: %w", err)
	}
	// syscall has no GetsockoptString. GetsockoptIPv6Mreq calls getsockopt
	// portably (including linux/386) with a 20-byte buffer; the kernel writes
	// at most TCP_CA_NAME_MAX (16) bytes, NUL-padded.
	var mreq *syscall.IPv6Mreq
	var serr error
	if err := rc.Control(func(fd uintptr) {
		mreq, serr = syscall.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_TCP, tcpCongestion)
	}); err != nil {
		return "", fmt.Errorf("congestion: control: %w", err)
	}
	if serr != nil {
		return "", fmt.Errorf("getsockopt TCP_CONGESTION: %w", serr)
	}
	b := asBytes(mreq)
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}
