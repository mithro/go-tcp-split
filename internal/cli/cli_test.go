package cli

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestVersionFlag(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.2.post3"
	var out, errb bytes.Buffer
	if rc := Main([]string{"--version"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d stderr=%q", rc, errb.String())
	}
	if got := out.String(); got != "tcp-split 1.2.post3\n" {
		t.Fatalf("stdout=%q", got)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := Main([]string{"--bogus"}, &out, &errb); rc == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(errb.String(), "bogus") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestParseArgsDefaults(t *testing.T) {
	var errb bytes.Buffer
	c, v, err := parseArgs(nil, &errb)
	if err != nil || v {
		t.Fatalf("err=%v version=%v", err, v)
	}
	if len(c.Listen) != 0 || len(c.Deny) != 0 || c.ConnectTimeout != 10*time.Second ||
		c.Keepalive != 60*time.Second || c.IdleTimeout != 0 || c.MaxConns != 4096 ||
		c.StatsInterval != 5*time.Minute || c.Drain != 30*time.Second ||
		c.Congestion != "" || c.Verbose {
		t.Fatalf("defaults wrong: %+v", c)
	}
}

func TestParseArgsRepeatable(t *testing.T) {
	var errb bytes.Buffer
	c, _, err := parseArgs([]string{"--listen", "a:1", "--listen", "b:2",
		"--deny", "10.0.0.0/8", "--deny", "fc00::/7"}, &errb)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Listen) != 2 || c.Listen[0] != "a:1" || c.Listen[1] != "b:2" {
		t.Fatalf("listen=%v", c.Listen)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fc00::/7")}
	if len(c.Deny) != 2 || c.Deny[0] != want[0] || c.Deny[1] != want[1] {
		t.Fatalf("deny=%v", c.Deny)
	}
}

func TestParseArgsBadPrefix(t *testing.T) {
	var errb bytes.Buffer
	if _, _, err := parseArgs([]string{"--deny", "nonsense"}, &errb); err == nil {
		t.Fatal("expected error")
	}
}

func TestMainNoListeners(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := Main(nil, &out, &errb); rc != 2 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(errb.String(), "--listen") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestMainEndToEnd(t *testing.T) {
	var out lockedBuf
	var errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"--listen", "127.0.0.1:0", "--stats-interval", "0"}, &out, &errb)
	}()
	re := regexp.MustCompile(`listening addr=(\S+)`)
	var addr string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			addr = m[1]
			break
		}
	}
	if addr == "" {
		t.Fatalf("no listening line; out=%q err=%q", out.String(), errb.String())
	}
	c, err := net.Dial("tcp", addr)
	if err == nil {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = c.Read(make([]byte, 1))
		c.Close()
	}
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("expected ECONNRESET, got %v", err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case rc := <-done:
		if rc != 0 {
			t.Fatalf("rc=%d out=%q err=%q", rc, out.String(), errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Main did not return")
	}
	if !strings.Contains(out.String(), "result=not-redirected") {
		t.Fatalf("out=%q", out.String())
	}
}

// Stray positional arguments would silently drop every flag
// after them (flag stops at the first non-flag), e.g. deny prefixes.
func TestParseArgsRejectsPositional(t *testing.T) {
	var errb bytes.Buffer
	_, _, err := parseArgs([]string{"--deny", "10.0.0.0/8", "172.16.0.0/12", "--deny", "fc00::/7"}, &errb)
	if err == nil {
		t.Fatal("expected error for stray positional argument")
	}
	if !strings.Contains(err.Error(), "172.16.0.0/12") {
		t.Fatalf("error does not name the stray argument: %v", err)
	}
	var out bytes.Buffer
	errb.Reset()
	if rc := Main([]string{"--listen", "127.0.0.1:0", "stray"}, &out, &errb); rc != 2 {
		t.Fatalf("Main rc=%d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "stray") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestParseArgsRejectsNegative(t *testing.T) {
	for _, args := range [][]string{
		{"--connect-timeout", "-1s"},
		{"--keepalive", "-1s"},
		{"--idle-timeout", "-1s"},
		{"--stats-interval", "-1s"},
		{"--drain", "-1s"},
		{"--max-conns", "-1"},
	} {
		var errb bytes.Buffer
		_, _, err := parseArgs(args, &errb)
		if err == nil {
			t.Errorf("%v: expected error", args)
			continue
		}
		if !strings.Contains(err.Error(), strings.TrimPrefix(args[0], "--")) {
			t.Errorf("%v: error does not name the flag: %v", args, err)
		}
	}
}

func TestParseArgsIdleTimeoutTooSmall(t *testing.T) {
	for _, v := range []string{"1ns", "3ns", "999us"} {
		var errb bytes.Buffer
		if _, _, err := parseArgs([]string{"--idle-timeout", v}, &errb); err == nil || !strings.Contains(err.Error(), "idle-timeout") {
			t.Errorf("--idle-timeout %s: err=%v, want rejection", v, err)
		}
	}
	for _, v := range []string{"0", "1ms", "10m"} {
		var errb bytes.Buffer
		if _, _, err := parseArgs([]string{"--idle-timeout", v}, &errb); err != nil {
			t.Errorf("--idle-timeout %s: unexpected error %v", v, err)
		}
	}
	// Zero values keep their documented meanings.
	var errb bytes.Buffer
	if _, _, err := parseArgs([]string{"--max-conns", "0", "--keepalive", "0", "--connect-timeout", "0",
		"--stats-interval", "0", "--drain", "0"}, &errb); err != nil {
		t.Errorf("zero values rejected: %v", err)
	}
}

// listenNonLoopback listens on the first global unicast address of this host.
func listenNonLoopback(t *testing.T) net.Listener {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		pfx, err := netip.ParsePrefix(a.String())
		if err != nil || !pfx.Addr().IsGlobalUnicast() {
			continue
		}
		l, err := net.Listen("tcp", netip.AddrPortFrom(pfx.Addr(), 0).String())
		if err == nil {
			return l
		}
	}
	t.Skip("no non-loopback address to run an upstream on (the real policy refuses loopback)")
	return nil
}

// Review Focus 5 at the CLI level: SIGTERM with a live relay drains it. Main
// keeps running while the relay is live, the relay finishes normally, its
// close line is logged and Main returns 0. Without nft redirection the test
// cannot make a real redirected connection, so it injects the original
// destination through testLookup (Main's only test seam).
func TestMainSIGTERMDrainsLiveRelay(t *testing.T) {
	// The real policy refuses loopback destinations, so the upstream listens
	// on one of this host's own non-loopback addresses.
	up := listenNonLoopback(t)
	defer up.Close()
	go func() {
		c, err := up.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c) // echo until the client half-closes
	}()
	dst := up.Addr().(*net.TCPAddr).AddrPort()
	testLookup = func(*net.TCPConn) (netip.AddrPort, error) { return dst, nil }
	t.Cleanup(func() { testLookup = nil })

	var out, errb lockedBuf
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"--listen", "127.0.0.1:0", "--stats-interval", "0", "--drain", "5s"}, &out, &errb)
	}()
	re := regexp.MustCompile(`listening addr=(\S+)`)
	var addr string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			addr = m[1]
			break
		}
	}
	if addr == "" {
		t.Fatalf("no listening line; out=%q err=%q", out.String(), errb.String())
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo before signal = %q, %v; out=%q", buf, err, out.String())
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), "signal=terminated"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no shutdown line; out=%q", out.String())
		}
	}
	// Draining: Main has not returned and the relay still carries data.
	select {
	case rc := <-done:
		t.Fatalf("Main returned %d with a live relay; out=%q", rc, out.String())
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := c.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "pong" {
		t.Fatalf("echo during drain = %q, %v", buf, err)
	}
	// New connections are refused while draining.
	if nc, err := net.Dial("tcp", addr); !errors.Is(err, syscall.ECONNREFUSED) {
		if nc != nil {
			nc.Close()
		}
		t.Fatalf("dial during drain: err = %v, want ECONNREFUSED", err)
	}
	// Finish the relay cleanly; Main must then return 0 well inside --drain.
	c.(*net.TCPConn).CloseWrite()
	if rest, err := io.ReadAll(c); err != nil || len(rest) != 0 {
		t.Fatalf("after half-close: %q, %v", rest, err)
	}
	select {
	case rc := <-done:
		if rc != 0 {
			t.Fatalf("rc=%d out=%q err=%q", rc, out.String(), errb.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Main did not return after the relay finished")
	}
	if !regexp.MustCompile(`msg=connection .*up=8 down=8 .*result=ok`).MatchString(out.String()) {
		t.Fatalf("no result=ok close line for the drained relay; out=%q", out.String())
	}
}
