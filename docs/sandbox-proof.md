# Sandbox proof: `tcp-split.service` on systemd 257

**Redactions.** The transcripts below were captured on a real host and edited before
publication; the technical results are unchanged. Redacted: the hostname (elided); the
host's timezone (journal, `systemctl` and `ls` times are converted to UTC; the host uses
a non-UTC local timezone, so the daemon's own `time=` fields that were in host-local
time read `time=<host-local time, offset redacted>`); volume and device names (the LVM
volume group reads `vg0`) and the root volume's device number (`253:<N>`); the login
account and its home directory (`admin`, with the `ls` columns re-padded to fit it);
memory-derived sizes (tmpfs `size=`, the task limit) and the `proc` link count; process
IDs (`<PID>`); the systemd package build string; and the commit hashes the probe
binaries were built from (they belong to a pre-publication history, so the build source
is described in words; the binaries' sha256 sums are unchanged). In the renderer
listings, references to internal review notes were replaced by the technical reason.

- **Date:** 2026-11-06 (probe run 2026-11-06T08:59:54+00:00 to 2026-11-06T09:02:20+00:00)
- **Host:** a Debian 13 VPN server (systemd 257), x86-64, kernel 6.12
- **systemd:** `systemd 257`
- **Binary:** `tcp-split` built `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath
  -ldflags "-s -w -X github.com/mithro/go-tcp-split/internal/cli.Version=probe"` from
  the branch as it stood before this proof, sha256
  `70708e915435c932fcf8fab701c772e3eca4da42c63b34a7109c454e0cc0e684`.

## Method

The probe is a **transient, socket-activated** unit pair (`tcp-split-probe.socket` +
`tcp-split-probe.service`), exactly like the shipped `tcp-split.socket` /
`tcp-split.service`: `SocketBindDeny=any` forbids the daemon from binding a socket
itself, so the listener has to be passed in by systemd. Every `[Service]` directive of
`packaging/systemd/tcp-split.service` is rendered as a `-p Key=Value` option, except:

- `ExecStart=` and `EnvironmentFile=`: `systemd-run` supplies the command line;
- `Requires=`/`After=` (in `[Unit]`): the socket is the transient one;
- `BindReadOnlyPaths=/usr/bin/tcp-split` becomes
  `BindReadOnlyPaths=/home/admin/tcp-split-probe:/usr/bin/tcp-split`, because the
  package is not installed on the host; the binary appears at the same in-sandbox path.

One directive is added: `SystemCallLog=~@system-service`, which makes the kernel audit
log (`type=1326`) record every syscall outside `@system-service`, i.e. every syscall
the shipped `SystemCallFilter=` would have refused.

`systemd-run` is called with `--machine=.host`. Without it, `systemd-run` resolves the
command on the *host* before submitting the unit and refuses with `Failed to find
executable /usr/bin/tcp-split: No such file or directory` (no unit is created). With
`--machine=.host` the command is passed through unchanged to PID 1; the unit's
properties are identical.

### Renderer (not committed; verbatim apart from the redactions and reworded comments noted above)

```python
#!/usr/bin/env python3
"""Render packaging/systemd/tcp-split.service [Service] directives as a
socket-activated transient `systemd-run` command: SocketBindDeny=any means
the listener must be passed in by systemd."""
import shlex
import sys

UNIT = sys.argv[1] if len(sys.argv) > 1 else "packaging/systemd/tcp-split.service"
DROP = {"ExecStart", "EnvironmentFile"}  # systemd-run supplies the command

props = []
section = None
with open(UNIT) as f:
    for raw in f:
        line = raw.strip()
        if not line or line.startswith(("#", ";")):
            continue
        if line.startswith("["):
            section = line
            continue
        if section != "[Service]":
            continue  # Requires=/After= live in [Unit]; the socket is transient
        key, _, value = line.partition("=")
        if key in DROP:
            continue
        if key == "BindReadOnlyPaths" and value == "/usr/bin/tcp-split":
            # The package is not installed on the probe host: bind the probe
            # binary onto the same in-sandbox path instead.
            value = "/home/admin/tcp-split-probe:/usr/bin/tcp-split"
        props.append(f"{key}={value}")

props.append("SystemCallLog=~@system-service")

argv = [
    "sudo", "systemd-run", "--machine=.host", "--unit", "tcp-split-probe",
    "--socket-property", "ListenStream=127.0.0.1:10443",
    "--socket-property", "NoDelay=true",
    "--socket-property", "FreeBind=yes",
]
for p in props:
    argv += ["-p", p]
argv += ["/usr/bin/tcp-split", "--stats-interval", "0"]

if "--list" in sys.argv:
    print("\n".join(props))
else:
    print(shlex.join(argv))
```

### The exact command

```sh
sudo systemd-run --machine=.host --unit tcp-split-probe \
  --socket-property ListenStream=127.0.0.1:10443 \
  --socket-property NoDelay=true \
  --socket-property FreeBind=yes \
  -p DynamicUser=yes \
  -p NoNewPrivileges=yes \
  -p CapabilityBoundingSet= \
  -p AmbientCapabilities= \
  -p 'RestrictAddressFamilies=AF_INET AF_INET6' \
  -p SocketBindDeny=any \
  -p TemporaryFileSystem=/:ro \
  -p BindReadOnlyPaths=/home/admin/tcp-split-probe:/usr/bin/tcp-split \
  -p PrivateTmp=yes \
  -p PrivateDevices=yes \
  -p PrivateUsers=yes \
  -p ProtectHostname=yes \
  -p ProtectClock=yes \
  -p ProtectKernelTunables=yes \
  -p ProtectKernelModules=yes \
  -p ProtectKernelLogs=yes \
  -p ProtectControlGroups=yes \
  -p ProtectProc=invisible \
  -p ProcSubset=pid \
  -p RestrictNamespaces=yes \
  -p RestrictRealtime=yes \
  -p RestrictSUIDSGID=yes \
  -p LockPersonality=yes \
  -p MemoryDenyWriteExecute=yes \
  -p SystemCallArchitectures=native \
  -p SystemCallFilter=@system-service \
  -p 'SystemCallFilter=~@privileged @resources' \
  -p UMask=0077 \
  -p LimitNOFILE=65536 \
  -p Restart=on-failure \
  -p 'SystemCallLog=~@system-service' \
  /usr/bin/tcp-split --stats-interval 0
```

### Rendered directive list

```
DynamicUser=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_INET AF_INET6
SocketBindDeny=any
TemporaryFileSystem=/:ro
BindReadOnlyPaths=/home/admin/tcp-split-probe:/usr/bin/tcp-split
PrivateTmp=yes
PrivateDevices=yes
PrivateUsers=yes
ProtectHostname=yes
ProtectClock=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectProc=invisible
ProcSubset=pid
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
UMask=0077
LimitNOFILE=65536
Restart=on-failure
SystemCallLog=~@system-service
```

## Result

The socket listened, the first connection started the service, the service served it
and reset it, as designed, and stopped cleanly on SIGTERM.

```
$ systemctl status tcp-split-probe.socket tcp-split-probe.service   # before the first connection
● tcp-split-probe.socket - [systemd-run] /usr/bin/tcp-split --stats-interval 0
     Loaded: loaded (/run/systemd/transient/tcp-split-probe.socket; transient)
     Active: active (listening) since Fri 2026-11-06 08:59:54 UTC; 1s ago
     Listen: 127.0.0.1:10443 (Stream)
○ tcp-split-probe.service - [systemd-run] /usr/bin/tcp-split --stats-interval 0
     Active: inactive (dead)
TriggeredBy: ● tcp-split-probe.socket

$ ss -ltnp | grep 10443
LISTEN 0      4096                           127.0.0.1:10443      0.0.0.0:*

$ curl -sS -m 5 127.0.0.1:10443; echo rc=$?
curl: (56) Recv failure: Connection reset by peer
rc=56

$ systemctl status tcp-split-probe.service
● tcp-split-probe.service - [systemd-run] /usr/bin/tcp-split --stats-interval 0
     Active: active (running) since Fri 2026-11-06 08:59:55 UTC; 1s ago
   Main PID: <PID> (tcp-split)
      Tasks: 7 (limit: <redacted>)
     Memory: 1.6M (peak: 2.8M)
```

Journal (`journalctl -u tcp-split-probe.service -u tcp-split-probe.socket -o short-iso`,
hostname elided, times in UTC); three further `curl` calls were made at 09:00:20,
all `rc=56`:

```
2026-11-06T08:59:54+00:00 systemd[1]: Listening on tcp-split-probe.socket - [systemd-run] /usr/bin/tcp-split --stats-interval 0.
2026-11-06T08:59:55+00:00 systemd[1]: Started tcp-split-probe.service - [systemd-run] /usr/bin/tcp-split --stats-interval 0.
2026-11-06T08:59:55+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=listening addr=127.0.0.1:10443
2026-11-06T08:59:55+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=connection client=127.0.0.1:35912 dst=127.0.0.1:10443 up=0 down=0 dur=51.999µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:00:20+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=connection client=127.0.0.1:58044 dst=127.0.0.1:10443 up=0 down=0 dur=35.896µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:00:20+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=connection client=127.0.0.1:58052 dst=127.0.0.1:10443 up=0 down=0 dur=30.164µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:00:20+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=connection client=127.0.0.1:58054 dst=127.0.0.1:10443 up=0 down=0 dur=34.339µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:02:20+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=shutdown signal=terminated
2026-11-06T09:02:20+00:00 tcp-split[<PID>]: time=<host-local time, offset redacted> level=INFO msg=stats accepted=4 active=0 completed=0 failed=4 failed_by="not-redirected=4" up=0 down=0
2026-11-06T09:02:20+00:00 systemd[1]: tcp-split-probe.service: Deactivated successfully.
2026-11-06T09:02:20+00:00 systemd[1]: tcp-split-probe.socket: Deactivated successfully.
```

### SystemCallLog

**No `type=1326` record was produced by `tcp-split`** over the whole run (start, four
accepted-and-reset connections, SIGTERM shutdown). To show the absence is meaningful, a
positive control was run on the same host with the same logging directive
(`systemd-run --collect --wait --pipe -p DynamicUser=yes -p SystemCallLog=~@system-service
/usr/bin/python3 -c "import ctypes; print(ctypes.CDLL(None).syscall(103, 10, 0, 0))"`,
i.e. a read-only `syslog(2)` size query, which is outside `@system-service`). It was
logged:

```
kernel: audit: type=1326 ... uid=63793 ... comm="python3" exe="/usr/bin/python3.13" sig=0 arch=c000003e syscall=103 compat=0 ... code=0x7ffc0000
```

### Process state inside the sandbox (from `/proc/<PID>/status`)

```
Uid:	63202	63202	63202	63202      (DynamicUser)
CapEff:	0000000000000000
CapBnd:	0000000000000000
CapAmb:	0000000000000000
NoNewPrivs:	1
Seccomp:	2
```

`/proc` inside the sandbox is mounted `hidepid=invisible,subset=pid`; `/dev` is a private
read-only tmpfs; `/tmp` is a private tmpfs; `/usr/bin/tcp-split` is the bind-mounted
probe binary (`ro`).

## Per-directive verdict

All directives were accepted by systemd 257 and the service started and served with the
full set; **no directive had to be removed**. "Effect observed" means the probe saw the
directive's effect directly; "accepted" means systemd took it and the service ran, but
nothing in this probe would have exercised it.

| Directive | Verdict |
|---|---|
| `DynamicUser=yes` | pass; effect observed (uid 63202; implies `ProtectSystem=strict`, `ProtectHome=read-only`, both shown by `systemctl show`) |
| `NoNewPrivileges=yes` | pass; effect observed (`NoNewPrivs: 1`) |
| `CapabilityBoundingSet=` | pass; effect observed (`CapBnd` 0) |
| `AmbientCapabilities=` | pass; effect observed (`CapAmb` 0) |
| `RestrictAddressFamilies=AF_INET AF_INET6` | pass; accepted (logging to the journal via stdout still works) |
| `SocketBindDeny=any` | pass; the inherited socket served; the daemon makes no `bind()` of its own |
| `TemporaryFileSystem=/:ro` | **accepted, NO EFFECT** (see below) |
| `BindReadOnlyPaths=` (binary) | pass; effect observed (`/usr/bin/tcp-split` is the probe, mounted `ro`) |
| `PrivateTmp=yes` | pass; effect observed (private `/tmp`) |
| `PrivateDevices=yes` | pass; effect observed (private read-only `/dev`) |
| `PrivateUsers=yes` | pass; effect observed (`uid_map` maps only root and the dynamic uid) |
| `ProtectHostname=yes` | pass; accepted |
| `ProtectClock=yes` | pass; accepted |
| `ProtectKernelTunables=yes` | pass; accepted |
| `ProtectKernelModules=yes` | pass; accepted |
| `ProtectKernelLogs=yes` | pass; accepted |
| `ProtectControlGroups=yes` | pass; accepted |
| `ProtectProc=invisible` | pass; effect observed (`hidepid=invisible`) |
| `ProcSubset=pid` | pass; effect observed (`subset=pid`) |
| `RestrictNamespaces=yes` | pass; accepted |
| `RestrictRealtime=yes` | pass; accepted |
| `RestrictSUIDSGID=yes` | pass; accepted |
| `LockPersonality=yes` | pass; accepted |
| `MemoryDenyWriteExecute=yes` | pass; the Go runtime started and served under it |
| `SystemCallArchitectures=native` | pass; accepted |
| `SystemCallFilter=@system-service` + `~@privileged @resources` | pass; effect observed (`Seccomp: 2`); no syscall outside `@system-service` was logged |
| `UMask=0077` | pass; accepted |
| `LimitNOFILE=65536` | pass; accepted |
| `Restart=on-failure` | pass; accepted (not triggered) |

### `TemporaryFileSystem=/:ro` has no effect

`systemctl show` lists `TemporaryFileSystem=/:ro`, but the service's mount namespace
root is the host's root file system, not a tmpfs:

```
412 222 253:<N> / / ro,nosuid,relatime shared:238 master:1 - ext4 /dev/mapper/vg0-root rw
```

and the whole host tree (`/etc`, `/home`, `/usr/bin/*`, ...) is visible inside the
sandbox, read-only. The read-only root comes from the `ProtectSystem=strict` that
`DynamicUser=yes` implies, not from `TemporaryFileSystem=`. So the intent that
"`/usr/bin/tcp-split` is the only file in the sandbox" is **not** met on systemd 257:
the service can read (not write) anything the unprivileged dynamic user may read. The
directive is harmless and was left in the unit unchanged; replacing it with an
effective equivalent is a separate decision.

## What this probe does not cover

- **No onward connection.** There is no redirect rule on loopback, so every connection's
  original destination is the listener itself and is reset as `not-redirected`. The
  outbound `connect()`, the `splice(2)` relay path and the per-socket `setsockopt`s on
  the upstream leg did not run under the filter; they remain to be observed (with
  `SystemCallLog=` if wanted) on the first real deployment.
- `tcp-split-redirect.service` (the nftables helper) was not run: the host's firewall
  must not be changed.
- `EnvironmentFile=` / `$TCP_SPLIT_OPTS` expansion was not exercised.
- The `ExecStart=` path resolution of a persistent unit (here `systemd-run
  --machine=.host` submitted it as-is).
