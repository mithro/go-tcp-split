package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// pair returns a connected loopback (client, server) pair of TCP conns.
func pair(t *testing.T) (client, server *net.TCPConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type res struct {
		c   *net.TCPConn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			ch <- res{nil, err}
			return
		}
		ch <- res{c.(*net.TCPConn), nil}
	}()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	client, server = r.c, s.(*net.TCPConn)
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

type rig struct {
	client, upstream *net.TCPConn
	done             chan Result
}

func start(t *testing.T, ctx context.Context, idle time.Duration) *rig {
	t.Helper()
	client, proxyIn := pair(t)
	proxyOut, upstream := pair(t)
	r := &rig{client: client, upstream: upstream, done: make(chan Result, 1)}
	go func() { r.done <- Run(ctx, proxyIn, proxyOut, idle) }()
	return r
}

func wait(t *testing.T, r *rig, d time.Duration) Result {
	t.Helper()
	select {
	case res := <-r.done:
		return res
	case <-time.After(d):
		t.Fatal("Run did not return in time")
		return Result{}
	}
}

func TestEchoBothDirections(t *testing.T) {
	for _, idle := range []time.Duration{0, time.Minute} {
		r := start(t, context.Background(), idle)
		const n = 1 << 20
		up := bytes.Repeat([]byte{'u'}, n)
		down := bytes.Repeat([]byte{'d'}, n)
		go func() { r.client.Write(up); r.client.CloseWrite() }()
		go func() { r.upstream.Write(down); r.upstream.CloseWrite() }()
		gotUp, err := io.ReadAll(r.upstream)
		if err != nil || !bytes.Equal(gotUp, up) {
			t.Fatalf("upstream read: %d bytes, err %v", len(gotUp), err)
		}
		gotDown, err := io.ReadAll(r.client)
		if err != nil || !bytes.Equal(gotDown, down) {
			t.Fatalf("client read: %d bytes, err %v", len(gotDown), err)
		}
		res := wait(t, r, 2*time.Second)
		if res.Err != nil || res.Up != n || res.Down != n {
			t.Fatalf("idle=%v result %+v", idle, res)
		}
	}
}

func TestHalfClose(t *testing.T) {
	r := start(t, context.Background(), 0)
	if _, err := r.client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	r.client.CloseWrite()
	got, err := io.ReadAll(r.upstream)
	if err != nil || string(got) != "request" {
		t.Fatalf("upstream got %q err %v", got, err)
	}
	if _, err := r.upstream.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	r.upstream.CloseWrite()
	got, err = io.ReadAll(r.client)
	if err != nil || string(got) != "reply" {
		t.Fatalf("client got %q err %v", got, err)
	}
	res := wait(t, r, 2*time.Second)
	if res.Err != nil || res.Up != 7 || res.Down != 5 {
		t.Fatalf("result %+v", res)
	}
}

func TestUpstreamReset(t *testing.T) {
	baseline := runtime.NumGoroutine()
	r := start(t, context.Background(), 0)
	if _, err := r.client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(r.upstream, buf); err != nil {
		t.Fatal(err)
	}
	r.upstream.SetLinger(0)
	r.upstream.Close()
	res := wait(t, r, 2*time.Second)
	if !errors.Is(res.Err, syscall.ECONNRESET) {
		t.Fatalf("Err = %v, want ECONNRESET (not a secondary close error)", res.Err)
	}
	// The abort is relayed as an abort, never as a clean EOF.
	expectRST(t, r.client)
	// Close the test's own conns, then expect goroutines to settle.
	r.client.Close()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline {
		t.Fatalf("goroutine leak: %d > baseline %d", n, baseline)
	}
}

// expectRST requires the next read on c to fail with ECONNRESET (an RST from
// the relay), not EOF (a FIN) or a timeout.
func expectRST(t *testing.T, c *net.TCPConn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.Read(make([]byte, 64))
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("read err = %v, want ECONNRESET", err)
	}
}

// The other direction: a client RST reaches the upstream as RST,
// so the server never mistakes a truncated upload for a complete one.
func TestClientReset(t *testing.T) {
	for _, idle := range []time.Duration{0, time.Minute} {
		r := start(t, context.Background(), idle)
		if _, err := r.client.Write([]byte("partial upload")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 14)
		if _, err := io.ReadFull(r.upstream, buf); err != nil {
			t.Fatal(err)
		}
		r.client.SetLinger(0)
		r.client.Close()
		res := wait(t, r, 2*time.Second)
		if !errors.Is(res.Err, syscall.ECONNRESET) {
			t.Fatalf("idle=%v: Err = %v, want ECONNRESET", idle, res.Err)
		}
		expectRST(t, r.upstream)
	}
}

func TestIdle(t *testing.T) {
	r := start(t, context.Background(), 200*time.Millisecond)
	res := wait(t, r, time.Second)
	if !errors.Is(res.Err, ErrIdle) {
		t.Fatalf("Err = %v", res.Err)
	}
	// An idle timeout is not a clean end; both peers see RST.
	expectRST(t, r.client)
	expectRST(t, r.upstream)
}

func TestIdleResetByTraffic(t *testing.T) {
	r := start(t, context.Background(), 300*time.Millisecond)
	for i := 0; i < 6; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := r.client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case res := <-r.done:
		t.Fatalf("returned early: %+v", res)
	default:
	}
	res := wait(t, r, 2*time.Second)
	if !errors.Is(res.Err, ErrIdle) {
		t.Fatalf("Err = %v", res.Err)
	}
}

func TestContextCancel(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	r := start(t, ctx, 0)
	time.Sleep(50 * time.Millisecond)
	cancel()
	res := wait(t, r, time.Second)
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("Err = %v", res.Err)
	}
	// A cancelled (force-drained) relay resets both peers.
	expectRST(t, r.client)
	expectRST(t, r.upstream)
	r.client.Close()
	r.upstream.Close()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline {
		t.Fatalf("goroutine leak: %d > baseline %d", n, baseline)
	}
}

func TestResetSendsRST(t *testing.T) {
	client, server := pair(t)
	Reset(server)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, err := client.Read(buf)
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("err = %v, want ECONNRESET", err)
	}
}

// An idle timeout under 4ns made NewTicker(idle/4) panic.
func TestTinyIdleDoesNotPanic(t *testing.T) {
	r := start(t, context.Background(), time.Nanosecond)
	res := wait(t, r, 2*time.Second)
	if !errors.Is(res.Err, ErrIdle) {
		t.Fatalf("Err = %v", res.Err)
	}
}
