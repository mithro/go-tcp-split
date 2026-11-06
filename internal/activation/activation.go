// Package activation implements the systemd socket-activation protocol
// (LISTEN_PID / LISTEN_FDS) in-tree, without a systemd library.
package activation

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

// listenFDsStart is SD_LISTEN_FDS_START: the first fd systemd passes.
const listenFDsStart = 3

// Listeners returns the TCP listeners passed by systemd, or nil, nil when
// the process was not socket-activated (LISTEN_PID unset or not our pid).
// A malformed LISTEN_PID or LISTEN_FDS is an error. LISTEN_PID, LISTEN_FDS
// and LISTEN_FDNAMES are always unset once read, including on the
// wrong-pid and error paths, so they are not inherited by children.
func Listeners() ([]net.Listener, error) {
	pidStr, ok := os.LookupEnv("LISTEN_PID")
	fdsStr := os.Getenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if !ok || pidStr == "" {
		return nil, nil
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, fmt.Errorf("activation: invalid LISTEN_PID %q: %w", pidStr, err)
	}
	if pid != os.Getpid() {
		return nil, nil
	}
	fds, err := strconv.Atoi(fdsStr)
	if err != nil || fds < 0 {
		return nil, fmt.Errorf("activation: invalid LISTEN_FDS %q", fdsStr)
	}
	return listenersFrom(pid, fds, listenFDsStart)
}

// listenersFrom wraps fds firstFD..firstFD+fds-1 as TCP listeners. pid is
// the already-validated target pid and is unused beyond documentation.
// On error every fd in the range is closed and nothing is returned.
func listenersFrom(pid, fds, firstFD int) ([]net.Listener, error) {
	_ = pid
	var out []net.Listener
	for i := 0; i < fds; i++ {
		fd := firstFD + i
		syscall.CloseOnExec(fd)
		f := os.NewFile(uintptr(fd), "LISTEN_FD_"+strconv.Itoa(fd))
		l, err := net.FileListener(f)
		f.Close() // FileListener dups the fd.
		if err == nil {
			if _, isTCP := l.(*net.TCPListener); !isTCP {
				l.Close()
				err = fmt.Errorf("not a TCP listener (%T)", l)
			}
		}
		if err != nil {
			for _, o := range out {
				o.Close()
			}
			for j := i + 1; j < fds; j++ {
				syscall.Close(firstFD + j)
			}
			return nil, fmt.Errorf("activation: fd %d: %w", fd, err)
		}
		out = append(out, l)
	}
	return out, nil
}
