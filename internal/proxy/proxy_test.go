package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mithro/go-tcp-split/internal/policy"
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

// waitLog polls the log until it contains substr.
func waitLog(t *testing.T, lb *lockedBuf, substr string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := lb.String(); strings.Contains(s, substr) {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("log never contained %q; log:\n%s", substr, lb.String())
	return ""
}

// testServer is an echo upstream; it returns its listener.
func testServer(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return l
}

func addrOf(l net.Listener) netip.AddrPort {
	return l.Addr().(*net.TCPAddr).AddrPort()
}

type harness struct {
	s    *Server
	addr string
	logs *lockedBuf
	done chan error
}

// startProxy runs Serve on a loopback listener. Lookup defaults to dst.
func startProxy(t *testing.T, o Options, dst netip.AddrPort) *harness {
	t.Helper()
	lb := &lockedBuf{}
	o.Logger = slog.New(slog.NewTextHandler(lb, nil))
	if o.Lookup == nil {
		o.Lookup = func(*net.TCPConn) (netip.AddrPort, error) { return dst, nil }
	}
	s := New(o)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{s: s, addr: l.Addr().String(), logs: lb, done: make(chan error, 1)}
	go func() { h.done <- s.Serve(context.Background(), []net.Listener{l}) }()
	t.Cleanup(func() { s.Shutdown(100 * time.Millisecond) })
	return h
}

func dial(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c.(*net.TCPConn)
}

// expectReset reads until an error arrives and requires ECONNRESET.
func expectReset(t *testing.T, c net.Conn, within time.Duration) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 1024)
	for {
		_, err := c.Read(buf)
		if err == nil {
			continue
		}
		if !errors.Is(err, syscall.ECONNRESET) {
			t.Fatalf("want ECONNRESET, got %v", err)
		}
		return
	}
}

// dialExpectReset connects and requires a reset. The proxy's reset can win
// the race with Dial's own connect completion, which also counts.
func dialExpectReset(t *testing.T, addr string, within time.Duration) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		if !errors.Is(err, syscall.ECONNRESET) {
			t.Fatal(err)
		}
		return
	}
	defer c.Close()
	expectReset(t, c, within)
}

func TestHappyPath(t *testing.T) {
	up := testServer(t)
	h := startProxy(t, Options{ConnectTimeout: time.Second, Keepalive: 30 * time.Second, Congestion: "cubic", Verbose: true}, addrOf(up))
	c := dial(t, h.addr)
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	c.Close()
	log := waitLog(t, h.logs, "result=ok")
	for _, want := range []string{"msg=connection", "up=5", "down=5", "dst=" + addrOf(up).String(), "client=", "dur=", "msg=accept"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q:\n%s", want, log)
		}
	}
}

func TestNotRedirected(t *testing.T) {
	h := startProxy(t, Options{Lookup: func(*net.TCPConn) (netip.AddrPort, error) {
		return netip.AddrPort{}, errors.New("ENOENT")
	}}, netip.AddrPort{})
	dialExpectReset(t, h.addr, 2*time.Second)
	log := waitLog(t, h.logs, "result=not-redirected")
	if !strings.Contains(log, "up=0 down=0") || !strings.Contains(log, `dst=""`) {
		t.Errorf("unexpected line:\n%s", log)
	}
}

// SO_ORIGINAL_DST on a non-redirected conn returns its own local
// address, which must also count as not-redirected.
func TestNotRedirectedSelfAddress(t *testing.T) {
	h := startProxy(t, Options{Lookup: func(c *net.TCPConn) (netip.AddrPort, error) {
		return c.LocalAddr().(*net.TCPAddr).AddrPort(), nil
	}}, netip.AddrPort{})
	dialExpectReset(t, h.addr, 2*time.Second)
	waitLog(t, h.logs, "result=not-redirected")
}

func TestDenied(t *testing.T) {
	up := testServer(t)
	h := startProxy(t, Options{Check: func(netip.AddrPort) error { return policy.ErrDenied }}, addrOf(up))
	dialExpectReset(t, h.addr, 2*time.Second)
	waitLog(t, h.logs, "result=denied")
}

func TestConnectFailed(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dst := addrOf(l)
	l.Close()
	h := startProxy(t, Options{ConnectTimeout: time.Second}, dst)
	dialExpectReset(t, h.addr, 2*time.Second)
	waitLog(t, h.logs, "result=connect-failed")
}

func TestLimit(t *testing.T) {
	up := testServer(t)
	h := startProxy(t, Options{MaxConns: 1, ConnectTimeout: time.Second}, addrOf(up))
	c1 := dial(t, h.addr)
	c1.Write([]byte("x"))
	io.ReadFull(c1, make([]byte, 1)) // first is live
	dialExpectReset(t, h.addr, 2*time.Second)
	waitLog(t, h.logs, "result=limit")
	if a := h.s.Stats().Active; a != 1 {
		t.Errorf("Active = %d, want 1", a)
	}
}

func TestEarlyClientData(t *testing.T) {
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	h := startProxy(t, Options{ConnectTimeout: time.Second}, addrOf(l))
	c := dial(t, h.addr)
	payload := make([]byte, 64<<10)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // proxy has dialled; upstream not yet accepted
	uc, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	uc.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(uc, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload corrupted or reordered")
	}
}

func TestDialTimeout(t *testing.T) {
	h := startProxy(t, Options{
		ConnectTimeout: 300 * time.Millisecond,
		Dial: func(ctx context.Context, dst netip.AddrPort) (*net.TCPConn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, netip.MustParseAddrPort("192.0.2.1:80"))
	start := time.Now()
	dialExpectReset(t, h.addr, time.Second)
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
	waitLog(t, h.logs, "result=connect-failed")
}

func TestShutdownDrains(t *testing.T) {
	// Natural completion: upstream echoes then closes after ~200ms.
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		io.ReadFull(c, buf)
		time.Sleep(200 * time.Millisecond)
		c.Write(buf)
	}()
	h := startProxy(t, Options{ConnectTimeout: time.Second}, addrOf(l))
	c := dial(t, h.addr)
	c.Write([]byte("ping"))
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	go func() {
		io.Copy(io.Discard, c)
	}()
	// Client closes after the upstream is done so the relay can finish.
	go func() {
		time.Sleep(300 * time.Millisecond)
		c.CloseWrite()
	}()
	h.s.Shutdown(2 * time.Second)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("drain took %v, want prompt", d)
	}
	if err := <-h.done; err != nil {
		t.Errorf("Serve = %v", err)
	}
	waitLog(t, h.logs, "result=ok")
	h.s.Shutdown(time.Second) // second call is a no-op

	// Stuck relay: upstream never replies, forced close.
	stuck, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer stuck.Close()
	go func() {
		c, err := stuck.Accept()
		if err == nil {
			defer c.Close()
			io.Copy(io.Discard, c)
		}
	}()
	h2 := startProxy(t, Options{ConnectTimeout: time.Second}, addrOf(stuck))
	c2 := dial(t, h2.addr)
	c2.Write([]byte("x"))
	for h2.s.Stats().Active != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	start = time.Now()
	h2.s.Shutdown(100 * time.Millisecond)
	if d := time.Since(start); d > time.Second {
		t.Errorf("forced shutdown took %v", d)
	}
	if st := h2.s.Stats(); st.Active != 0 || st.Failed != 1 || st.FailedBy["error"] != 1 {
		t.Errorf("stats after forced shutdown: %+v", st)
	}
	waitLog(t, h2.logs, "result=error")
}

func TestStats(t *testing.T) {
	var deny atomic.Bool
	up := testServer(t)
	h := startProxy(t, Options{
		ConnectTimeout: time.Second,
		Check: func(netip.AddrPort) error {
			if deny.Load() {
				return policy.ErrLoop
			}
			return nil
		},
	}, addrOf(up))
	c := dial(t, h.addr)
	c.Write([]byte("abc"))
	io.ReadFull(c, make([]byte, 3))
	c.Close()
	waitLog(t, h.logs, "result=ok")
	for i := 0; i < 2; i++ {
		deny.Store(true)
		dialExpectReset(t, h.addr, 2*time.Second)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.s.Stats().Failed < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st := h.s.Stats()
	if st.Accepted != 3 || st.Completed != 1 || st.Failed != 2 || st.Active != 0 ||
		st.FailedBy["denied"] != 2 || st.Up != 3 || st.Down != 3 {
		t.Errorf("stats = %+v", st)
	}
	h.s.LogStats()
	log := h.logs.String()
	for _, want := range []string{"msg=stats", "accepted=3", "completed=1", "failed=2", "up=3", "down=3", "denied=2"} {
		if !strings.Contains(log, want) {
			t.Errorf("stats log missing %q:\n%s", want, log)
		}
	}
}
