package cli

import (
	"bytes"
	"errors"
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
