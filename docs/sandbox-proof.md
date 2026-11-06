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

Two rounds were run. Round 1 proved the original directive set starts and serves, and
found that `TemporaryFileSystem=/:ro` has no effect. Round 2 replaced it with an empty
`RootDirectory=` and proved that the process then sees only its own binary plus the API
file systems. The shipped unit is the round 2 set.

**Recipe notes.** The probe is a transient unit pair started with `systemd-run
--machine=.host` (see Method); `--machine=.host` is required whenever the unit's binary
does not exist at its `ExecStart=` path on the host. The renderer and command for each
round are recorded below, verbatim apart from the redactions listed above;
neither is committed.

# Round 1

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
| `TemporaryFileSystem=/:ro` | **accepted, NO EFFECT** (see below); replaced in round 2 |
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
directive was harmless but did nothing; round 2 replaces it.

# Round 2: empty root via `RootDirectory=`

- **Run:** 2026-11-06T09:05:51+00:00 to 2026-11-06T09:06:29+00:00, same host.
- **Binary:** same build command, from the branch with the round 1
  results committed; sha256
  `f0676ce9439bcedc776ed2b40e6726cb9a8ba3edccc39a8df0c72aae2d2eff3b`.
- **Change to the directive set:** `TemporaryFileSystem=/:ro` removed;
  `RuntimeDirectory=tcp-split`, `RootDirectory=/run/tcp-split`, `MountAPIVFS=yes` added.
  Everything else is identical to round 1. The probe bind
  `BindReadOnlyPaths=/home/admin/tcp-split-probe:/usr/bin/tcp-split` now lands inside
  the new root.

### Renderer, round 2 (run with `--round2`; not committed)

```python
#!/usr/bin/env python3
"""Render packaging/systemd/tcp-split.service [Service] directives as a
socket-activated transient `systemd-run` command: SocketBindDeny=any means
the listener must be passed in by systemd."""
import shlex
import sys

UNIT = sys.argv[1] if len(sys.argv) > 1 else "packaging/systemd/tcp-split.service"
DROP = {"ExecStart", "EnvironmentFile", "TemporaryFileSystem"}  # systemd-run supplies the command

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

# Round 2: an empty root instead of TemporaryFileSystem=/ (no effect in round 1).
if "--round2" in sys.argv:
    props += ["RuntimeDirectory=tcp-split", "RootDirectory=/run/tcp-split", "MountAPIVFS=yes"]
if "--no-apivfs" in sys.argv:
    props = [p for p in props if p != "MountAPIVFS=yes"]
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

### The exact command, round 2

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
  -p RuntimeDirectory=tcp-split \
  -p RootDirectory=/run/tcp-split \
  -p MountAPIVFS=yes \
  -p 'SystemCallLog=~@system-service' \
  /usr/bin/tcp-split --stats-interval 0
```

### Result, round 2

The service started on the first connection (no `226/NAMESPACE`; the retry without
`MountAPIVFS=yes` was not needed), served it, and stopped cleanly on SIGTERM.

```
$ curl -sS -m 5 127.0.0.1:10443; echo rc=$?        # once, then three more times
curl: (56) Recv failure: Connection reset by peer
rc=56
```

```
2026-11-06T09:05:51+00:00 systemd[1]: Listening on tcp-split-probe.socket - [systemd-run] /usr/bin/tcp-split --stats-interval 0.
2026-11-06T09:05:53+00:00 systemd[1]: Started tcp-split-probe.service - [systemd-run] /usr/bin/tcp-split --stats-interval 0.
2026-11-06T09:05:53+00:00 tcp-split[<PID>]: time=2026-11-06T09:05:53.132Z level=INFO msg=listening addr=127.0.0.1:10443
2026-11-06T09:05:53+00:00 tcp-split[<PID>]: time=2026-11-06T09:05:53.132Z level=INFO msg=connection client=127.0.0.1:40748 dst=127.0.0.1:10443 up=0 down=0 dur=51.992µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:06:13+00:00 tcp-split[<PID>]: time=2026-11-06T09:06:13.110Z level=INFO msg=connection client=127.0.0.1:55350 dst=127.0.0.1:10443 up=0 down=0 dur=32.89µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:06:13+00:00 tcp-split[<PID>]: time=2026-11-06T09:06:13.117Z level=INFO msg=connection client=127.0.0.1:55358 dst=127.0.0.1:10443 up=0 down=0 dur=32.496µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:06:13+00:00 tcp-split[<PID>]: time=2026-11-06T09:06:13.124Z level=INFO msg=connection client=127.0.0.1:55370 dst=127.0.0.1:10443 up=0 down=0 dur=30.922µs result=not-redirected err="destination is the listener's own address"
2026-11-06T09:06:29+00:00 tcp-split[<PID>]: time=2026-11-06T09:06:29.737Z level=INFO msg=shutdown signal=terminated
2026-11-06T09:06:29+00:00 tcp-split[<PID>]: time=2026-11-06T09:06:29.737Z level=INFO msg=stats accepted=4 active=0 completed=0 failed=4 failed_by="not-redirected=4" up=0 down=0
2026-11-06T09:06:29+00:00 systemd[1]: tcp-split-probe.service: Deactivated successfully.
2026-11-06T09:06:29+00:00 systemd[1]: tcp-split-probe.socket: Deactivated successfully.
```

The daemon's own timestamps are now UTC (`...Z`), unlike round 1's host-local time:
there is no `/etc/localtime` in the root, which is itself a sign that the host's
`/etc` is gone. No `type=1326`
(`SystemCallLog`) record was produced. `/proc/<PID>/status`: uid 63202, `CapEff`/`CapBnd`
0, `NoNewPrivs: 1`, `Seccomp: 2`.

### What the process can see

`sudo ls -la /proc/<PID>/root/ /proc/<PID>/root/usr/bin/ /proc/<PID>/root/etc` from
the host (the mount namespace's root, as the process sees it), verbatim apart from the
redactions listed above:

```
/proc/<PID>/root/:
total 0
drwxr-xr-x  11 tcp-split-probe tcp-split-probe 220 Nov  6 09:05 .
drwxr-xr-x  11 tcp-split-probe tcp-split-probe 220 Nov  6 09:05 ..
drwxr-xr-x   7 root            root            400 Nov  6 09:05 dev
drwxr-xr-x   2 root            root             40 Nov  6 09:05 etc
dr-xr-xr-x <N> root            root              0 Nov  6 09:05 proc
drwxr-x---   2 root            root             40 Nov  6 09:05 root
drwxr-xr-x   5 root            root            100 Nov  6 09:05 run
dr-xr-xr-x  13 root            root              0 Nov  6 09:00 sys
drwxrwxrwt   2 root            root             40 Nov  6 09:05 tmp
drwxr-xr-x   3 root            root             60 Nov  6 09:05 usr
drwxr-xr-x   3 root            root             60 Nov  6 09:05 var

/proc/<PID>/root/etc:
total 0
drwxr-xr-x  2 root            root             40 Nov  6 09:05 .
drwxr-xr-x 11 tcp-split-probe tcp-split-probe 220 Nov  6 09:05 ..

/proc/<PID>/root/usr/bin/:
total 3432
drwxr-xr-x 2 root  root       60 Nov  6 09:05 .
drwxr-xr-x 3 root  root       60 Nov  6 09:05 ..
-rwxr-xr-x 1 admin admin 3510432 Nov  6 09:05 tcp-split
```

The whole non-API tree (`find /proc/<PID>/root/ -xdev -maxdepth 4`): empty `/etc`,
empty `/root` (0750 root), empty private `/tmp` and `/var/tmp` (from `PrivateTmp=`), `/usr/bin/tcp-split`,
and `/run` holding only what systemd itself mounts (the journal sockets and
`/run/host/.os-release-stage`, `/run/systemd/incoming`). The `proc` listing as seen by
root from the host shows all PIDs; inside, `hidepid=invisible,subset=pid` applies.

`sudo cat /proc/<PID>/mountinfo | wc -l` is `38`. The first lines:

```
412 222 0:25 /tcp-split / ro,nosuid,nodev,noexec,relatime shared:238 master:5 - tmpfs tmpfs rw,size=<redacted>,mode=755,inode64
413 412 0:56 / /dev ro,nosuid,noexec shared:239 - tmpfs tmpfs rw,size=4096k,nr_inodes=65536,mode=755,inode64
414 413 0:24 / /dev/pts rw,nosuid,noexec,relatime shared:240 master:3 - devpts devpts rw,gid=5,mode=600,ptmxmode=000
415 413 0:27 / /dev/shm rw,nosuid,nodev shared:241 master:4 - tmpfs tmpfs rw,inode64
416 413 0:21 / /dev/mqueue rw,nosuid,nodev,noexec,relatime shared:242 master:14 - mqueue mqueue rw
417 413 0:37 / /dev/hugepages rw,nosuid,nodev,relatime shared:243 master:21 - hugetlbfs hugetlbfs rw,pagesize=2M
418 412 0:57 / /proc rw,nosuid,nodev,noexec,relatime shared:244 - proc proc rw,hidepid=invisible,subset=pid
419 412 0:58 / /run rw,nosuid,nodev shared:245 - tmpfs tmpfs rw,size=<redacted>,nr_inodes=819200,mode=755,inode64
...
448 412 253:<N> /home/admin/tcp-split-probe /usr/bin/tcp-split ro,nosuid,relatime shared:274 master:1 - ext4 /dev/mapper/vg0-root rw
```

The root (`412`) is now the runtime directory's tmpfs, mounted `ro,nosuid,nodev,noexec`;
the only file from the host's disk is the bind-mounted binary (`448`, `ro`). The
runtime directory `/run/tcp-split` was gone from the host after `systemctl stop`.

### Verdict, round 2

Pass. `RuntimeDirectory=tcp-split` + `RootDirectory=/run/tcp-split` + `MountAPIVFS=yes`
start and serve under the full round 1 directive set on systemd 257, and the process
sees nothing of the host file system beyond its own binary and the API file systems
(`/proc`, `/sys`, `/dev`), which meets the spec's goal 3. `packaging/systemd/tcp-split.service`
now carries these three directives in place of `TemporaryFileSystem=/:ro`.

# What neither round covers


- **No onward connection.** There is no redirect rule on loopback, so every connection's
  original destination is the listener itself and is reset as `not-redirected`. The
  outbound `connect()`, the `splice(2)` relay path and the per-socket `setsockopt`s on
  the upstream leg did not run under the filter; they remain to be observed (with
  `SystemCallLog=` if wanted) on the first real deployment.
- The nftables interception rules were not loaded: the host's firewall must not be
  changed. (The package no longer ships a unit for them; they are site firewall
  configuration.)
- `EnvironmentFile=` / `$TCP_SPLIT_OPTS` expansion was not exercised.
- The `ExecStart=` path resolution of a persistent unit (here `systemd-run
  --machine=.host` submitted it as-is).
