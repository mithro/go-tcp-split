// Package relay implements the bidirectional byte pump between a client
// connection and the upstream connection.
package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrIdle is reported in Result.Err when no bytes moved in either direction
// for the configured idle period.
var ErrIdle = errors.New("idle timeout")

// Result describes a finished relay. Up counts client->upstream bytes and
// Down counts upstream->client bytes. Err is nil only when both directions
// ended cleanly; otherwise it is the first real cause of termination.
type Result struct {
	Up, Down int64
	Err      error
}

// countingWriter records that bytes moved, for idle detection.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// Run copies bytes in both directions until both are done, one fails, the
// context is cancelled or the idle timeout fires. Both conns are closed
// before it returns. idle <= 0 disables the idle timeout.
//
// Only a clean end (EOF in both directions) closes the conns normally. On any
// other end (a copy error such as a peer's reset, the idle timeout, or ctx
// cancellation, e.g. a forced drain) both conns are reset, so
// neither peer can mistake an aborted transfer for a complete one.
func Run(ctx context.Context, client, upstream *net.TCPConn, idle time.Duration) Result {
	var (
		mu       sync.Mutex
		cause    error
		finished bool // both copies ended; late ctx/idle events are ignored
		moved    atomic.Int64
	)
	// fail records the first cause and resets both conns, which makes the
	// other io.Copy return with a secondary "closed" error that is ignored
	// because cause is already set. Once both copies have finished cleanly
	// it does nothing: resetting then could discard data still queued in a
	// send buffer behind the FIN.
	fail := func(err error) {
		mu.Lock()
		if finished {
			mu.Unlock()
			return
		}
		if cause == nil {
			cause = err
		}
		mu.Unlock()
		Reset(client)
		Reset(upstream)
	}

	// When idle == 0 the raw *net.TCPConn is the destination, so io.Copy
	// uses splice(2). A wrapping io.Writer (needed to observe activity)
	// defeats that fast path, so it is only used when idle > 0.
	dstFor := func(c *net.TCPConn) io.Writer {
		if idle > 0 {
			return countingWriter{c, &moved}
		}
		return c
	}

	var wg sync.WaitGroup
	var up, down int64
	pump := func(dst, src *net.TCPConn, n *int64) {
		defer wg.Done()
		var err error
		*n, err = io.Copy(dstFor(dst), src)
		if err != nil {
			fail(err)
			return
		}
		dst.CloseWrite()
	}
	wg.Add(2)
	go pump(upstream, client, &up)
	go pump(client, upstream, &down)

	done := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go func() {
		defer aux.Done()
		select {
		case <-ctx.Done():
			fail(ctx.Err())
		case <-done:
		}
	}()
	if idle > 0 {
		aux.Add(1)
		go func() {
			defer aux.Done()
			// The CLI rejects idle timeouts under 1ms; clamp anyway so a
			// tiny value can never make NewTicker panic.
			tick := time.NewTicker(max(idle/4, time.Millisecond))
			defer tick.Stop()
			last, lastChange := moved.Load(), time.Now()
			for {
				select {
				case <-done:
					return
				case now := <-tick.C:
					if cur := moved.Load(); cur != last {
						last, lastChange = cur, now
					} else if now.Sub(lastChange) >= idle {
						fail(ErrIdle)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	mu.Lock()
	finished = true
	mu.Unlock()
	close(done)
	aux.Wait()
	client.Close()
	upstream.Close()

	mu.Lock()
	defer mu.Unlock()
	return Result{Up: up, Down: down, Err: cause}
}

// Reset aborts c with a TCP RST by disabling linger before closing.
func Reset(c *net.TCPConn) {
	_ = c.SetLinger(0) // Close must happen even if this fails
	_ = c.Close()
}
