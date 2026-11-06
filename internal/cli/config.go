package cli

import (
	"flag"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"
)

// Config holds the parsed command line.
type Config struct {
	Listen         []string
	Deny           []netip.Prefix
	ConnectTimeout time.Duration
	Keepalive      time.Duration
	IdleTimeout    time.Duration
	MaxConns       int
	Congestion     string
	StatsInterval  time.Duration
	Drain          time.Duration
	Verbose        bool
}

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// prefixList is a repeatable CIDR flag.
type prefixList []netip.Prefix

func (p *prefixList) String() string {
	parts := make([]string, len(*p))
	for i, x := range *p {
		parts[i] = x.String()
	}
	return strings.Join(parts, ",")
}

func (p *prefixList) Set(v string) error {
	pf, err := netip.ParsePrefix(v)
	if err != nil {
		return fmt.Errorf("invalid prefix %q: %w", v, err)
	}
	*p = append(*p, pf)
	return nil
}

// parseArgs parses args. The bool result is true when --version was given.
func parseArgs(args []string, stderr io.Writer) (Config, bool, error) {
	var (
		c       Config
		listen  stringList
		deny    prefixList
		version bool
	)
	fs := flag.NewFlagSet("tcp-split", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(&listen, "listen", "address to listen on (repeatable)")
	fs.Var(&deny, "deny", "CIDR destination to refuse (repeatable)")
	fs.DurationVar(&c.ConnectTimeout, "connect-timeout", 10*time.Second, "upstream connect timeout")
	fs.DurationVar(&c.Keepalive, "keepalive", 60*time.Second, "TCP keepalive period")
	fs.DurationVar(&c.IdleTimeout, "idle-timeout", 0, "close connections idle this long (0 disables)")
	fs.IntVar(&c.MaxConns, "max-conns", 4096, "maximum concurrent connections (0 means unlimited)")
	fs.StringVar(&c.Congestion, "congestion", "", "TCP congestion control algorithm")
	fs.DurationVar(&c.StatsInterval, "stats-interval", 5*time.Minute, "interval between stats log lines (0 disables)")
	fs.DurationVar(&c.Drain, "drain", 30*time.Second, "graceful shutdown drain time")
	fs.BoolVar(&c.Verbose, "verbose", false, "verbose logging")
	fs.BoolVar(&version, "version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return Config{}, false, err
	}
	c.Listen = listen
	c.Deny = deny
	if err := validate(fs, c); err != nil {
		// The flag package prints its own errors; ours are printed here so
		// that every rejected command line explains itself.
		fmt.Fprintf(stderr, "tcp-split: %v\n", err)
		return Config{}, false, err
	}
	return c, version, nil
}

// minIdleTimeout is the smallest non-zero --idle-timeout accepted. The relay
// checks for idleness every idle/4, so anything shorter is a typo, not a
// policy.
const minIdleTimeout = time.Millisecond

// validate rejects command lines that parse but cannot be meant: stray
// positional arguments (flag stops at the first one, silently discarding every
// flag after it, e.g. later --deny prefixes) and negative values.
func validate(fs *flag.FlagSet, c Config) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (every value needs its own flag, e.g. --deny A --deny B)", fs.Arg(0))
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"connect-timeout", c.ConnectTimeout},
		{"keepalive", c.Keepalive},
		{"idle-timeout", c.IdleTimeout},
		{"stats-interval", c.StatsInterval},
		{"drain", c.Drain},
	} {
		if d.v < 0 {
			return fmt.Errorf("--%s %v: must not be negative", d.name, d.v)
		}
	}
	if c.IdleTimeout > 0 && c.IdleTimeout < minIdleTimeout {
		return fmt.Errorf("--idle-timeout %v: must be 0 (disabled) or at least %v", c.IdleTimeout, minIdleTimeout)
	}
	if c.MaxConns < 0 {
		return fmt.Errorf("--max-conns %d: must not be negative (0 means unlimited)", c.MaxConns)
	}
	return nil
}
