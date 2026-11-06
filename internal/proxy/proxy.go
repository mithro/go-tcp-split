// Package proxy accepts redirected TCP connections, recovers their original
// destination, applies policy, dials upstream and relays bytes.
package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mithro/go-tcp-split/internal/relay"
	"github.com/mithro/go-tcp-split/internal/sockopt"
)

// Result strings reported in the per-connection close line.
const (
	ResultOK            = "ok"
	ResultIdle          = "idle"
	ResultError         = "error"
	ResultNotRedirected = "not-redirected"
	ResultDenied        = "denied"
	ResultLimit         = "limit"
	ResultConnectFailed = "connect-failed"
)

// Options configures a Server. Zero values select the documented defaults.
type Options struct {
	Lookup         func(*net.TCPConn) (netip.AddrPort, error)                          // default sockopt.OriginalDst
	Check          func(netip.AddrPort) error                                          // default: allow all
	Dial           func(ctx context.Context, dst netip.AddrPort) (*net.TCPConn, error) // default: net.Dialer with ConnectTimeout
	ConnectTimeout time.Duration
	Keepalive      time.Duration
	IdleTimeout    time.Duration
	MaxConns       int // <= 0 means unlimited
	Congestion     string
	Logger         *slog.Logger
	Verbose        bool
}

// Server is the split-TCP proxy.
type Server struct {
	opts Options
	log  *slog.Logger

	base   context.Context // parent of every relay; cancelled to force-close
	cancel context.CancelFunc

	accepted, active, completed, failed atomic.Int64
	up, down                            atomic.Int64
	mu                                  sync.Mutex // guards failedBy, listeners, closed
	failedBy                            map[string]int64
	listeners                           []net.Listener
	closed                              bool

	loops, handlers sync.WaitGroup
	shutdownOnce    sync.Once
}

// New builds a Server, filling in defaults.
func New(o Options) *Server {
	if o.Lookup == nil {
		o.Lookup = sockopt.OriginalDst
	}
	if o.Check == nil {
		o.Check = func(netip.AddrPort) error { return nil }
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}
	s := &Server{opts: o, log: o.Logger, failedBy: map[string]int64{}}
	if o.Dial == nil {
		s.opts.Dial = s.defaultDial
	}
	s.base, s.cancel = context.WithCancel(context.Background())
	return s
}

func (s *Server) defaultDial(ctx context.Context, dst netip.AddrPort) (*net.TCPConn, error) {
	d := net.Dialer{Timeout: s.opts.ConnectTimeout, KeepAlive: -1}
	c, err := d.DialContext(ctx, "tcp", dst.String())
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

// Serve accepts on every listener until Shutdown closes them or ctx is done.
// It returns nil after an orderly shutdown, else the first other accept error.
// Cancelling ctx is an abort: listeners are closed and live relays are cut
// immediately; for a graceful drain call Shutdown. Either way Serve returns
// only after all accept loops and all connection handlers have finished, so
// no close log line or stats update is lost.
func (s *Server) Serve(ctx context.Context, ls []net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		for _, l := range ls {
			l.Close()
		}
		return nil
	}
	s.listeners = append(s.listeners, ls...)
	s.mu.Unlock()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			s.closeListeners()
			s.cancel()
		case <-stop:
		}
	}()

	errc := make(chan error, len(ls))
	for _, l := range ls {
		s.loops.Add(1)
		go func() {
			defer s.loops.Done()
			errc <- s.acceptLoop(l)
		}()
	}
	s.loops.Wait()
	s.handlers.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) acceptLoop(l net.Listener) error {
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if s.isClosed() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if isTemporary(err) {
				// Back off like net/http.Server.Serve: 5ms doubling to 1s.
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else if delay *= 2; delay > time.Second {
					delay = time.Second
				}
				s.log.Warn("accept failed, retrying", "listener", l.Addr().String(), "err", err, "retry_in", delay)
				time.Sleep(delay)
				continue
			}
			s.log.Error("accept failed, listener stopped", "listener", l.Addr().String(), "err", err)
			return err
		}
		delay = 0
		tc, ok := c.(*net.TCPConn)
		if !ok {
			c.Close()
			continue
		}
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			s.Handle(tc)
		}()
	}
}

// isTemporary reports accept errors worth retrying (fd or memory pressure,
// aborted handshakes, timeouts).
func isTemporary(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	for _, e := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.ECONNABORTED} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func (s *Server) closeListeners() {
	s.mu.Lock()
	s.closed = true
	ls := s.listeners
	s.mu.Unlock()
	for _, l := range ls {
		l.Close()
	}
}

// Shutdown stops accepting, waits up to drain for live connections to finish,
// then force-closes the rest. Calling it again is a no-op.
func (s *Server) Shutdown(drain time.Duration) {
	s.shutdownOnce.Do(func() {
		s.closeListeners()
		s.loops.Wait()
		done := make(chan struct{})
		go func() {
			s.handlers.Wait()
			close(done)
		}()
		t := time.NewTimer(drain)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			s.cancel()
			<-done
		}
		s.cancel()
	})
}

// Handle serves one accepted connection synchronously and owns c: on every
// path c is closed before it returns and the counters are updated.
//
// Order: Lookup, not-redirected test, Check, limit, Dial, tuning, relay.
// A connection is classified result=not-redirected (and reset) when Lookup
// fails OR the returned destination equals the connection's own local
// address (compared as netip.AddrPort after Unmap on both sides). With
// conntrack loaded, SO_ORIGINAL_DST on a connection that was not redirected
// does not fail with ENOENT; it succeeds and returns the local address.
func (s *Server) Handle(c *net.TCPConn) {
	start := time.Now()
	s.accepted.Add(1)
	n := s.active.Add(1)
	client := c.RemoteAddr().String()
	dst := ""
	var res relay.Result
	result := ResultOK
	defer func() {
		s.active.Add(-1)
		s.up.Add(res.Up)
		s.down.Add(res.Down)
		if result == ResultOK {
			s.completed.Add(1)
		} else {
			s.failed.Add(1)
			s.mu.Lock()
			s.failedBy[result]++
			s.mu.Unlock()
		}
		attrs := []any{"client", client, "dst", dst, "up", res.Up, "down", res.Down,
			"dur", time.Since(start), "result", result}
		if result == ResultError {
			attrs = append(attrs, "err", res.Err)
		}
		s.log.Info("connection", attrs...)
	}()
	if s.opts.Verbose {
		s.log.Info("accept", "client", client)
	}
	reset := func(r string) {
		result = r
		relay.Reset(c)
	}

	d, err := s.opts.Lookup(c)
	if err != nil {
		reset(ResultNotRedirected)
		return
	}
	dst = d.String()
	if local, ok := c.LocalAddr().(*net.TCPAddr); ok {
		la := local.AddrPort()
		if netip.AddrPortFrom(la.Addr().Unmap(), la.Port()) == netip.AddrPortFrom(d.Addr().Unmap(), d.Port()) {
			reset(ResultNotRedirected)
			return
		}
	}
	if err := s.opts.Check(d); err != nil {
		// policy.ErrLoop, ErrInvalid and ErrDenied all map to denied; any
		// other checker failure is refused the same way (fail closed).
		reset(ResultDenied)
		return
	}
	if s.opts.MaxConns > 0 && n > int64(s.opts.MaxConns) {
		reset(ResultLimit)
		return
	}

	ctx := s.base
	if s.opts.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(s.base, s.opts.ConnectTimeout)
		defer cancel()
	}
	up, err := s.opts.Dial(ctx, d)
	if err != nil {
		reset(ResultConnectFailed)
		return
	}

	s.tune(c, up)
	res = relay.Run(s.base, c, up, s.opts.IdleTimeout)
	switch {
	case res.Err == nil:
	case errors.Is(res.Err, relay.ErrIdle):
		result = ResultIdle
	default:
		result = ResultError
	}
}

// tune applies socket options; failures are logged and ignored.
func (s *Server) tune(client, up *net.TCPConn) {
	warn := func(what string, err error) {
		if err != nil {
			s.log.Warn("setsockopt failed", "opt", what, "err", err)
		}
	}
	for _, c := range []*net.TCPConn{client, up} {
		warn("TCP_NODELAY", c.SetNoDelay(true))
		if s.opts.Keepalive > 0 {
			warn("SO_KEEPALIVE", c.SetKeepAlive(true))
			warn("keepalive period", c.SetKeepAlivePeriod(s.opts.Keepalive))
		}
	}
	if s.opts.Congestion != "" {
		warn("TCP_CONGESTION", sockopt.SetCongestion(client, s.opts.Congestion))
	}
}
