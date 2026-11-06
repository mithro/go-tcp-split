// Package cli implements the tcp-split command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mithro/go-tcp-split/internal/activation"
	"github.com/mithro/go-tcp-split/internal/policy"
	"github.com/mithro/go-tcp-split/internal/proxy"
)

// Version is the release version; the build overrides it via -ldflags -X.
var Version = "dev"

// testLookup, when set by a test, replaces the original-destination lookup.
// It is the only test seam in Main: without nft redirection a test cannot
// produce a connection with an original destination, so it cannot otherwise
// hold a live relay open through Main. Production never sets it.
var testLookup func(*net.TCPConn) (netip.AddrPort, error)

// Main runs tcp-split with args (excluding the program name) and returns the
// process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	cfg, version, err := parseArgs(args, stderr)
	if err != nil {
		// The flag package or parseArgs has already printed the message.
		return 2
	}
	if version {
		fmt.Fprintf(stdout, "tcp-split %s\n", Version)
		return 0
	}

	logger := slog.New(slog.NewTextHandler(stdout, nil))

	ls, err := activation.Listeners()
	if err != nil {
		logger.Error("socket activation failed", "err", err)
		return 1
	}
	if ls == nil {
		for _, a := range cfg.Listen {
			l, err := net.Listen("tcp", a)
			if err != nil {
				logger.Error("listen failed", "addr", a, "err", err)
				closeAll(ls)
				return 1
			}
			ls = append(ls, l)
		}
	}
	if len(ls) == 0 {
		fmt.Fprintln(stderr, "tcp-split: no listeners: pass --listen or run under systemd socket activation")
		return 2
	}

	own := make([]netip.AddrPort, 0, len(ls))
	for _, l := range ls {
		if ta, ok := l.Addr().(*net.TCPAddr); ok {
			own = append(own, ta.AddrPort())
		}
	}
	checker := policy.New(own, cfg.Deny)
	srv := proxy.New(proxy.Options{
		Lookup:         testLookup, // nil: proxy uses sockopt.OriginalDst
		Check:          checker.Check,
		ConnectTimeout: cfg.ConnectTimeout,
		Keepalive:      cfg.Keepalive,
		IdleTimeout:    cfg.IdleTimeout,
		MaxConns:       cfg.MaxConns,
		Congestion:     cfg.Congestion,
		Logger:         logger,
		Verbose:        cfg.Verbose,
	})

	// Install the handler before announcing the listeners (see tests that key
	// off the "listening" line to send a signal).
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	for _, l := range ls {
		logger.Info("listening", "addr", l.Addr().String())
	}

	stop := make(chan struct{})
	bg := make(chan struct{}, 2)
	nbg := 0
	if cfg.StatsInterval > 0 {
		nbg++
		go func() {
			defer func() { bg <- struct{}{} }()
			t := time.NewTicker(cfg.StatsInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					srv.LogStats()
				case <-stop:
					return
				}
			}
		}()
	}
	nbg++
	go func() {
		defer func() { bg <- struct{}{} }()
		select {
		case s := <-sigs:
			logger.Info("shutdown", "signal", s.String())
			srv.Shutdown(cfg.Drain)
		case <-stop:
		}
	}()

	// The ctx is only a hard-stop abort path; graceful drain is Shutdown.
	serveErr := srv.Serve(context.Background(), ls)
	close(stop)
	for ; nbg > 0; nbg-- {
		<-bg
	}
	srv.LogStats()
	if serveErr != nil {
		logger.Error("serve failed", "err", serveErr)
		return 1
	}
	return 0
}

func closeAll(ls []net.Listener) {
	for _, l := range ls {
		l.Close()
	}
}
