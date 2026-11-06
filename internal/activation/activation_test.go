package activation

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestNoEnv(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	ls, err := Listeners()
	if err != nil || ls != nil {
		t.Fatalf("got %v, %v; want nil, nil", ls, err)
	}
}

func TestWrongPid(t *testing.T) {
	t.Setenv("LISTEN_PID", "1")
	t.Setenv("LISTEN_FDS", "1")
	ls, err := Listeners()
	if err != nil || ls != nil {
		t.Fatalf("got %v, %v; want nil, nil", ls, err)
	}
}

func TestMalformedEnv(t *testing.T) {
	t.Setenv("LISTEN_PID", "abc")
	t.Setenv("LISTEN_FDS", "1")
	if _, err := Listeners(); err == nil {
		t.Fatal("want error for malformed LISTEN_PID")
	}
}

func TestListenersFrom(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f, err := l.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	dupfd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	f.Close()

	ls, err := listenersFrom(os.Getpid(), 1, dupfd)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 {
		t.Fatalf("got %d listeners", len(ls))
	}
	t.Cleanup(func() { ls[0].Close() })

	done := make(chan error, 1)
	go func() {
		c, err := ls[0].Accept()
		if err == nil {
			c.Close()
		}
		done <- err
	}()
	c, err := net.Dial("tcp", ls[0].Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNonSocketFD(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	dupfd, err := syscall.Dup(int(r.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := listenersFrom(os.Getpid(), 1, dupfd); err == nil {
		t.Fatal("want error for non-socket fd")
	}
}
