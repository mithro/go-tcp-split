#!/usr/bin/env python3
"""Network-namespace end-to-end harness for tcp-split (stdlib only).

Topology:  client --veth-- server --veth-- internet
The real packaging/examples/redirect.nft is loaded in `server`, the real
binary runs there, and an echo server in `internet` is the destination.
See test/netns/README.md.  Must run as (namespace) root.
"""
import argparse
import json
import os
import re
import shutil
import signal
import socket
import socketserver
import subprocess
import sys
import threading
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
NFT_EXAMPLE = os.path.join(ROOT, "packaging", "examples", "redirect.nft")
NAMESPACES = ("client", "server", "internet")
PORT = 10443
ECHO_PORT = 8080
SIZE = 1 << 20

NFT_PREPEND = 'define VPN_IFS = { "veth-c" }\ndefine WAN_IF = "veth-i"\ndefine PORT = %d\n\n' % PORT
NFT_APPEND = """
table inet tcp_split_test {
	chain postrouting {
		type nat hook postrouting priority srcnat;
		oifname $WAN_IF masquerade
	}
}
"""


# ---------------------------------------------------------------- probes ---

def serve_echo(addr, port):
    """Run an echo server in the foreground (invoked inside `internet`)."""
    class Handler(socketserver.BaseRequestHandler):
        def handle(self):
            while True:
                data = self.request.recv(65536)
                if not data:
                    return
                self.request.sendall(data)

    class Server(socketserver.ThreadingTCPServer):
        allow_reuse_address = True
        daemon_threads = True
        address_family = socket.AF_INET6 if ":" in addr else socket.AF_INET

    srv = Server((addr, port), Handler)
    print("ready", flush=True)
    srv.serve_forever()


def probe_echo(addr, port):
    """Send SIZE bytes, read them back, compare."""
    fam = socket.AF_INET6 if ":" in addr else socket.AF_INET
    payload = os.urandom(SIZE)
    s = socket.socket(fam, socket.SOCK_STREAM)
    s.settimeout(20)
    s.connect((addr, port))

    def send():
        for i in range(0, SIZE, 16384):
            s.sendall(payload[i:i + 16384])
    t = threading.Thread(target=send)
    t.start()
    got = bytearray()
    while len(got) < SIZE:
        chunk = s.recv(65536)
        if not chunk:
            break
        got += chunk
    t.join()
    s.close()
    return {"received": len(got), "match": bytes(got) == payload}


def probe_connect(addr, port):
    """Connect, then try to read; report errnos by name."""
    fam = socket.AF_INET6 if ":" in addr else socket.AF_INET
    s = socket.socket(fam, socket.SOCK_STREAM)
    s.settimeout(5)
    res = {"connect": None, "read": None}
    try:
        s.connect((addr, port))
        res["connect"] = "ok"
    except OSError as e:
        res["connect"] = errname(e)
        return res
    try:
        data = s.recv(1)
        res["read"] = "eof" if data == b"" else "data"
    except OSError as e:
        res["read"] = errname(e)
    return res


def errname(e):
    import errno
    if e.errno is None:
        return type(e).__name__
    return errno.errorcode.get(e.errno, str(e.errno))


# ------------------------------------------------------------ plumbing -----

def run(cmd, **kw):
    """subprocess.run with check=True; failures show command and output."""
    try:
        return subprocess.run(cmd, check=True, text=True, capture_output=True, **kw)
    except subprocess.CalledProcessError as e:
        raise RuntimeError("command failed (%d): %s\n%s%s" % (
            e.returncode, " ".join(cmd), e.stdout or "", e.stderr or "")) from None


def ns_exec(ns, *cmd):
    return ["ip", "netns", "exec", ns] + list(cmd)


def nsrun(ns, *cmd):
    return run(ns_exec(ns, *cmd))


def in_userns():
    with open("/proc/self/uid_map") as f:
        fields = f.read().split()
    return fields != ["0", "0", "4294967295"]


def prepare_run_dir():
    """In a user namespace /run is root-owned by the real root,
    so mount a tmpfs over /run and create /run/netns on it."""
    if in_userns():
        run(["mount", "-t", "tmpfs", "tmpfs", "/run"])
        os.makedirs("/run/netns", exist_ok=True)


def build_topology():
    for ns in NAMESPACES:
        if ns in run(["ip", "netns", "list"]).stdout:
            run(["ip", "netns", "del", ns])
        run(["ip", "netns", "add", ns])
        nsrun(ns, "ip", "link", "set", "lo", "up")
    run(["ip", "-n", "client", "link", "add", "veth-c0", "type", "veth",
         "peer", "name", "veth-c", "netns", "server"])
    run(["ip", "-n", "internet", "link", "add", "veth-i0", "type", "veth",
         "peer", "name", "veth-i", "netns", "server"])

    def addr(ns, dev, *addrs):
        for a in addrs:
            run(["ip", "-n", ns, "addr", "add", a, "dev", dev] + (["nodad"] if ":" in a else []))
        run(["ip", "-n", ns, "link", "set", dev, "up"])
    addr("client", "veth-c0", "10.99.0.2/24", "fd99:0::2/64")
    addr("server", "veth-c", "10.99.0.1/24", "fd99:0::1/64")
    addr("server", "veth-i", "10.99.1.1/24", "fd99:1::1/64")
    addr("internet", "veth-i0", "10.99.1.2/24", "fd99:1::2/64")
    run(["ip", "-n", "client", "route", "add", "default", "via", "10.99.0.1"])
    run(["ip", "-n", "client", "-6", "route", "add", "default", "via", "fd99:0::1"])
    # The denied target: routed via the server, which sends it WAN-ward so the
    # redirect rule matches; nothing answers because the proxy refuses first.
    run(["ip", "-n", "client", "route", "add", "10.99.2.0/24", "via", "10.99.0.1"])
    run(["ip", "-n", "server", "route", "add", "10.99.2.0/24", "via", "10.99.1.2"])
    run(["ip", "-n", "internet", "route", "add", "default", "via", "10.99.1.1"])
    run(["ip", "-n", "internet", "-6", "route", "add", "default", "via", "fd99:1::1"])
    nsrun("server", "sysctl", "-w", "net.ipv4.ip_forward=1")
    nsrun("server", "sysctl", "-w", "net.ipv6.conf.all.forwarding=1")


def load_nft(workdir):
    with open(NFT_EXAMPLE) as f:
        example = f.read()
    path = os.path.join(workdir, "redirect-test.nft")
    with open(path, "w") as f:
        f.write(NFT_PREPEND + example + NFT_APPEND)
    nsrun("server", "nft", "-f", path)


class Daemon:
    def __init__(self, binary, logpath):
        self.logpath = logpath
        self.logf = open(logpath, "wb")
        self.p = subprocess.Popen(ns_exec(
            "server", binary, "--listen", "10.99.0.1:%d" % PORT,
            "--listen", "[fd99:0::1]:%d" % PORT, "--deny", "10.99.2.0/24",
            "--stats-interval", "0"), stdout=self.logf, stderr=subprocess.STDOUT)

    def text(self):
        with open(self.logpath, errors="replace") as f:
            return f.read()

    def wait_listening(self, timeout=10):
        end = time.time() + timeout
        while time.time() < end:
            if self.p.poll() is not None:
                raise RuntimeError("daemon exited early (%s)" % self.p.returncode)
            if self.text().count("listening") >= 2:
                return
            time.sleep(0.05)
        raise RuntimeError("daemon did not log two listening lines")

    def wait_line(self, pattern, since=0, timeout=5):
        """Poll for a log line matching regex, after `since` lines."""
        end = time.time() + timeout
        while True:
            lines = self.text().splitlines()[since:]
            for ln in lines:
                if re.search(pattern, ln):
                    return ln
            if time.time() > end:
                return None
            time.sleep(0.05)

    def nlines(self):
        return len(self.text().splitlines())

    def stop(self):
        if self.p.poll() is None:
            self.p.send_signal(signal.SIGTERM)
            try:
                self.p.wait(timeout=35)
            except subprocess.TimeoutExpired:
                self.p.kill()
                self.p.wait()
        self.logf.close()


def probe(name, *args):
    out = run(ns_exec("client", sys.executable, os.path.abspath(__file__),
                      "--probe", name, *map(str, args)), timeout=60).stdout
    return json.loads(out.strip().splitlines()[-1])


# --------------------------------------------------------------- checks ----

def check_echo(d, addr, dst_log):
    since = d.nlines()
    r = probe("echo", addr, ECHO_PORT)
    if r["received"] != SIZE or not r["match"]:
        return False, "echo mismatch: %r" % r
    ln = d.wait_line(r"dst=\"?%s\"? .*result=ok" % re.escape(dst_log), since)
    if ln is None:
        return False, "no log line with dst=%s result=ok" % dst_log
    return True, "1 MiB round trip; " + ln


def echo_v4(d):
    return check_echo(d, "10.99.1.2", "10.99.1.2:8080")


def echo_v6(d):
    return check_echo(d, "fd99:1::2", "[fd99:1::2]:8080")


def check_reset(d, addr, port, result):
    since = d.nlines()
    r = probe("connect", addr, port)
    if "ECONNRESET" not in (r["connect"], r["read"]):
        return False, "expected ECONNRESET, got %r" % r
    ln = d.wait_line(r"result=%s" % result, since)
    if ln is None:
        return False, "no log line with result=%s" % result
    return True, "%r; %s" % (r, ln)


def direct_connect_reset(d):
    return check_reset(d, "10.99.0.1", PORT, "not-redirected")


def denied(d):
    return check_reset(d, "10.99.2.9", 80, "denied")


def local_not_split(d):
    since = d.nlines()
    r = probe("connect", "10.99.0.1", 22)
    if r["connect"] != "ECONNREFUSED":
        return False, "expected ECONNREFUSED, got %r" % r
    time.sleep(0.5)
    extra = [ln for ln in d.text().splitlines()[since:] if "connection" in ln]
    if extra:
        return False, "proxy logged a connection: %r" % extra
    return True, "ECONNREFUSED and no proxy log line"


def nft_counters(d):
    out = nsrun("server", "nft", "list", "chain", "inet", "tcp_split", "prerouting").stdout
    redirect = [ln for ln in out.splitlines() if "redirect" in ln]
    mm = re.search(r"packets (\d+)", redirect[0]) if redirect else None
    if not mm:
        return False, "no redirect counter in:\n" + out
    n = int(mm.group(1))
    out_in = nsrun("server", "nft", "list", "chain", "inet", "tcp_split", "input").stdout
    drop = [ln for ln in out_in.splitlines() if "drop" in ln]
    md = re.search(r"packets (\d+)", drop[0]) if drop else None
    if not md:
        return False, "no drop counter in:\n" + out_in
    want = 3  # echo_v4 + echo_v6 + denied
    if n != want or int(md.group(1)) != 0:
        return False, "redirect packets=%d (want %d), input drop packets=%s (want 0)" % (
            n, want, md.group(1))
    return True, "redirect packets=%d, input drop packets=0" % n


CHECKS = [echo_v4, echo_v6, direct_connect_reset, denied, local_not_split, nft_counters]


# ----------------------------------------------------------------- main ----

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="dist/tcp-split")
    ap.add_argument("--keep", action="store_true", help="skip cleanup")
    ap.add_argument("--echo-server", nargs=2, metavar=("ADDR", "PORT"))
    ap.add_argument("--probe", nargs="+")
    a = ap.parse_args()

    if a.echo_server:
        serve_echo(a.echo_server[0], int(a.echo_server[1]))
        return 0
    if a.probe:
        name, args = a.probe[0], a.probe[1:]
        fn = {"echo": probe_echo, "connect": probe_connect}[name]
        print(json.dumps(fn(args[0], int(args[1]))))
        return 0

    if os.geteuid() != 0:
        print("must run as root: use `make test-netns` (sudo) or "
              "`make test-netns-unpriv` (unshare -Urnm)", file=sys.stderr)
        return 2
    os.environ["PATH"] = os.environ.get("PATH", "") + ":/usr/sbin:/sbin"
    binary = os.path.abspath(a.binary)
    workdir = os.path.join(ROOT, "tmp", "netns")
    os.makedirs(workdir, exist_ok=True)

    daemon = None
    echos = []
    failed = 0
    created = False
    try:
        prepare_run_dir()
        created = True
        build_topology()
        load_nft(workdir)
        for addr in ("10.99.1.2", "fd99:1::2"):
            p = subprocess.Popen(ns_exec("internet", sys.executable, os.path.abspath(__file__),
                                         "--echo-server", addr, str(ECHO_PORT)),
                                 stdout=subprocess.PIPE, text=True)
            echos.append(p)
            if p.stdout.readline().strip() != "ready":
                raise RuntimeError("echo server on %s failed to start" % addr)
        daemon = Daemon(binary, os.path.join(workdir, "daemon.log"))
        daemon.wait_listening()
        for fn in CHECKS:
            try:
                ok, detail = fn(daemon)
            except Exception as e:  # report and continue with the other checks
                ok, detail = False, "exception: %s" % e
            if ok:
                print("PASS %s: %s" % (fn.__name__, detail))
            else:
                failed += 1
                print("FAIL %s: %s" % (fn.__name__, detail))
        if failed:
            print("--- daemon log ---\n" + daemon.text() + "--- end daemon log ---")
    except Exception as e:
        failed += 1
        print("FAIL setup: %s" % e)
        if daemon:
            print("--- daemon log ---\n" + daemon.text() + "--- end daemon log ---")
    finally:
        if a.keep:
            print("--keep: left namespaces client/server/internet, processes and %s in place.\n"
                  "Inspect with `ip netns exec server nft list ruleset`; clean up with "
                  "`ip netns del client; ip netns del server; ip netns del internet` "
                  "(inside the same user namespace if unprivileged; it ends with it)." % workdir)
        else:
            if daemon:
                daemon.stop()
            for p in echos:
                p.terminate()
                p.wait()
            if created:
                for ns in NAMESPACES:
                    if ns in run(["ip", "netns", "list"]).stdout:
                        run(["ip", "netns", "del", ns])
            shutil.rmtree(workdir, ignore_errors=True)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
