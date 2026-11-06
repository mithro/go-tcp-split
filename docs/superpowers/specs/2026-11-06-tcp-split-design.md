# tcp-split: a split-TCP proxy for lossy VPN clients

Status: DRAFT, 2026-11-06. Not approved. Nothing is built.

Revision 2026-11-06: the fail-open control plane is withdrawn by the owner
("Just make the rules permanent and the server run permanently"). There is
no `tcp-split-redirect.service` and no `BindsTo=` coupling: the interception
rules are the site's permanent firewall configuration, and both
`tcp-split.socket` and `tcp-split.service` are enabled at boot. Former Goal 4
("Fail open") and the matching anti-goal are withdrawn; a stopped proxy now
resets the tunnel's redirected TCP until it is restarted.

## Intent

A laptop reaches its home network (through the home router, `home`) and a
remote VPN server (`vps`) through WireGuard tunnels. Over a long, lossy path
the tunnel legs can see double-digit packet loss and several hundred
milliseconds of round-trip time. A TCP connection that is merely *forwarded*
through the tunnel to some server on the internet runs that server's
congestion control over the whole lossy path; a CUBIC sender collapses to a
small fraction of what the path can carry. Senders that terminate on the VPN
server itself (which runs BBR) keep many times that throughput over the same
path.

`tcp-split` makes every forwarded TCP connection terminate on the VPN server.
The server intercepts the connection, opens its own connection to the real
destination, and relays bytes both ways. The lossy leg (laptop to server) is
then always a server-side BBR socket; the clean leg (server to destination)
runs the destination's congestion control over a path with no loss.

This is a performance-enhancing proxy (PEP) of the classic satellite-link
kind. It is not a privacy, circumvention or security feature. It changes
which host the destination sees as the connection's source, and nothing else
about the traffic.

## Goals

1. Intercept forwarded TCP, IPv4 and IPv6, arriving on the tunnel interfaces
   of a VPN server, using an nftables `redirect` and `SO_ORIGINAL_DST` /
   `IP6T_SO_ORIGINAL_DST`. No TPROXY, no policy routing, no capabilities.
2. Only connections that would otherwise leave via the server's WAN interface
   are split. Connections to the server itself, to its LANs, VMs or other
   tunnels are never touched: the proxy must never widen what a client can
   reach.
3. The relay runs as an unprivileged dynamic user in a systemd sandbox that
   exposes nothing of the filesystem beyond the proxy binary itself.
4. One static binary, no dependencies, built for amd64 and arm64, shipped as
   a Debian package from a signed apt repository, identical on every host
   that runs it.
5. Observable: one journal line per connection, periodic aggregate counters,
   nft counters on every interception rule.
6. Deployed on the VPN server first, declaratively, through the site's
   configuration management, with a read-only verify pass.
   A second host (such as the home router) follows as a separately planned
   phase.

## Non-goals

- UDP and QUIC. A UDP relay cannot split QUIC's end-to-end congestion
  control. The only lever is to reject UDP/443 so browsers fall back to TCP;
  that is a firewall option, not proxy work.
- Preserving the client's source address towards the destination. That
  needs TPROXY or `IP_TRANSPARENT`, which needs `CAP_NET_ADMIN` and policy
  routing. Destinations see the VPN server's address, as they already do for
  IPv4 via masquerade.
- TLS, protocol awareness, authentication, access control by destination
  beyond the safety rules below, metrics endpoints, configuration files.
- Being a general proxy. The binary does one thing.
- The partial-tunnel laptop modes. Full-tunnel is the case being solved; the
  interception rules apply to whatever arrives on the tunnel interfaces.

## Anti-goals (failure even if it "works")

- The proxy lets a tunnel client reach something the VPN firewall forbids
  (a VM, a LAN, another tunnel, the server's own private addresses). The
  example VPN's whole policy is "internet exit only".
- The proxy port is reachable from anywhere other than the tunnel
  interfaces.
- The proxy process can read `/etc` (the WireGuard private key lives there)
  or write anywhere.
- A tunnel comes up without its firewall. The existing fail-closed chain
  (`wg-quick@` requires `vpn-firewall.service`) must be preserved.

## Constraints and measured facts

- Target platforms: Debian 13 (trixie) and later, amd64 and arm64, with
  nftables. The oldest systemd targeted is 257 (trixie's), so the sandbox
  directives used must exist in systemd 257.
- The VPN server is assumed to default to BBR
  (`net.ipv4.tcp_congestion_control`) and `fq_codel`. For example, with a
  `tcp_wmem` max of 4 MiB and a lossy-leg bandwidth-delay product of several
  hundred KB per connection, no sysctl change is needed; each host's value is
  checked before it is enabled.
- The nftables `redirect` statement rewrites the destination to the primary
  address of the *incoming* interface. On a tunnel `wg-<name>` that is the
  tunnel's server address (e.g. `192.0.2.1` and `2001:db8:1::1` for
  `wg-laptop` on `vps`). The VPN table's `input` chain is assumed to accept
  tunnel traffic to exactly those addresses already.
- NAT decisions are made on a connection's first packet, so loading or
  removing the interception rules never disturbs established connections.
- The kernel completes the client's TCP handshake before the proxy has
  connected onward. If the onward connect fails, the client has an open
  socket that must be reset; it will see a reset, not "connection refused".
- HAProxy was ruled out because its `SO_ORIGINAL_DST` support is IPv4 only
  (verified in its source). Envoy, gost and sslsplit were evaluated and
  are dual-stack; the custom binary was chosen for the smallest surface and
  the tightest sandbox.

## The decided design

### Repository and artefacts

The repository follows the existing style for Go binaries and packages
exactly as `go-tmux-saver` does today, which is the Set B ("our code")
convention in `mithro/apt-repo-action`'s `docs/packaging.md` and
`docs/conventions.md`. Where this document and those differ, those win.

- Repository `github.com/mithro/go-tcp-split`, public, Apache 2.0, default
  branch `main`, created and configured with the `github-setup` skill:
  merge commits only (no squash or rebase: the version count depends on
  it), delete branch on merge, the `Enforce vXX.ZZZ version tags` tag
  ruleset, Pages built from Actions with HTTPS enforced, the
  `github-pages` environment restricted to `main`, and the repository's
  **own** signing key (RSA 4096, no expiry, user ID
  `go-tcp-split apt repository <me@mith.ro>`) stored as the secret
  `APT_GPG_PRIVATE_KEY`. The GitHub description says what the package is.
- Layout as `go-tmux-saver`: `cmd/tcp-split/main.go`, `internal/...`,
  `go.mod` (`module github.com/mithro/go-tcp-split`, stdlib only, no
  `go.sum` entries), `Makefile` (`test`, `vet`, `build`), `nfpm.yaml`,
  `packaging/release-assets.sh` and `release-assets-test.sh` (copied),
  `.github/apt-packaging.toml` containing `kind = "B"` and the comment
  that it is a Go program packaged with nfpm, `.gitignore` as the
  sibling's (`.worktrees/`, `.superpowers/`, `/dist/`, the binary,
  `debian/changelog`), `LICENSE`, `README.md`.
- `.github/workflows/deb.yml`, `name: Debian packages`, is go-tmux-saver's
  workflow with the names changed: jobs `test`, `build-deb`, `publish-apt`,
  `release`; triggers `push` to `main`, `pull_request`,
  `workflow_dispatch`; the `deb-${{ github.ref }}` concurrency group; the
  **default** suites and architectures (`trixie`, `forky`, `sid`,
  `raspbian-trixie`, `raspbian-forky`; `amd64`, `i386`, `arm64`, `armhf`
  as ARMv6, `riscv64`), one static binary per architecture packaged once
  per suite with the version from `mithro/apt-repo-action/deb-version@main`
  (`version_schema: none` in nfpm); the install test in a `debian:<suite>`
  container checks `tcp-split --version` prints `tcp-split <version>`;
  `publish-apt` via the shared reusable workflow; a GitHub Release tagged
  `build-<version>` after it. Every push to `main` publishes; `vX.Y` tags
  are made by hand. The `test` job runs `gofmt -l`, `go vet`,
  `go test -race ./...`, the packaging asset-name test, and the namespace
  integration tests under `sudo`.
- The version is a `Version` variable set with `-X` at build time and
  printed by `--version`, as the siblings do. Flags are spelled `--long`.
- `README.md` has the `## Install` section in the convention's form: the
  one-setup block for `trixie` against `https://mith.ro/go-tcp-split/`,
  the sentence naming every published suite, and the key fingerprint;
  then what the package installs, the build line, and usage.
- Debian package `tcp-split` (a name not in Debian) installs:
  - `/usr/bin/tcp-split` (static, `CGO_ENABLED=0`, version via `-X`).
  - `tcp-split.socket` and `tcp-split.service` (below). The socket unit
    ships with **no** `ListenStream=`; the site supplies addresses in a
    drop-in. Nothing is enabled by the package.
  - `/usr/share/doc/tcp-split/` with README, an example socket drop-in and
    an example `redirect.nft`.
- The Go module has **no dependencies outside the standard library**. The
  systemd socket-activation protocol (`LISTEN_PID`, `LISTEN_FDS`) is a few
  lines and is implemented in-tree, not imported.

### The daemon

Listening. The daemon accepts on every socket passed by systemd
(`LISTEN_FDS`). For development and tests, `-listen <addr:port>` (repeatable)
binds its own listeners instead. It never binds otherwise.

Per accepted connection, in order:

1. Read the original destination: `getsockopt(SOL_IP, SO_ORIGINAL_DST)` on
   an IPv4 socket, `getsockopt(SOL_IPV6, IP6T_SO_ORIGINAL_DST)` (option
   80) on an IPv6 socket. If this fails the connection was not redirected
   (someone connected to the proxy port directly): log and reset.
2. Safety checks on the original destination, all of which log and reset:
   it equals one of the proxy's own listening addresses (loop); it is
   loopback, link-local, multicast, unspecified, or IPv4-mapped; it is in
   a prefix given by `-deny` (repeatable; the site passes its private,
   VM and tunnel ranges). These are defence in depth behind the nft rules.
3. If the number of live connections is at `-max-conns` (default 4096):
   log and reset.
4. Dial the original destination with `-connect-timeout` (default 10 s),
   source address chosen by the kernel. On failure: log and reset.
5. Set `TCP_NODELAY` on both sockets and TCP keepalive on both
   (`-keepalive`, default 60 s). If `-congestion <name>` is given, set
   `TCP_CONGESTION` to it on the client-facing socket (default: leave the
   system default, which is expected to be BBR).
6. Relay both directions concurrently with `io.Copy`, which the Go runtime
   turns into `splice(2)` between two TCP sockets on Linux. When one side
   returns EOF, half-close the other side (`CloseWrite`) and keep relaying
   the opposite direction until it also ends. When both directions are done,
   or either returns an error, close both.
7. `-idle-timeout` (default 0 = none) closes a connection with no bytes in
   either direction for that long. Dead peers are detected by keepalive, so
   long-idle sessions such as SSH stay up by default.

"Reset" means close with `SO_LINGER` set to zero so the client receives an
RST immediately rather than a FIN, and does not wait on a half-open socket.

Logging goes to stdout (the journal), one `key=value` line per connection at
close: client address, original destination, bytes up, bytes down, duration,
and the outcome (`ok`, `connect-failed`, `denied`, `not-redirected`,
`limit`, `idle`, or the error). Every `-stats-interval` (default 5 min) one
line with the aggregates since start: accepted, active, completed, failed by
reason, bytes each way. `-verbose` adds an accept line per connection.

Shutdown. On SIGTERM or SIGINT the daemon stops accepting, waits up to
`-drain` (default 30 s) for live connections to finish, then exits. With
socket activation the listening socket stays open in systemd across
restarts, so new connections queue in the backlog rather than being refused.

`tcp-split -version` prints the version. Any flag error exits non-zero with
a message; the daemon never falls back silently.

### The systemd units (shipped in the package)

`tcp-split.socket`: `Accept=no`, `FreeBind=yes` (the tunnel addresses may
not exist yet at boot), `ListenStream=` supplied by the site drop-in, one
line per tunnel address, both families. Listening only on the tunnel server
addresses, never a wildcard, is the first line of "unreachable from
elsewhere"; the nft `input` guard below is the second. Because the socket
unit passes the listeners, the service's sandbox can keep
`SocketBindDeny=any`. `[Install] WantedBy=sockets.target`.

`tcp-split.service`: `Requires=tcp-split.socket`, `After=tcp-split.socket`,
`[Install] WantedBy=multi-user.target` (the proxy runs permanently from boot
rather than waiting for a first connection), `ExecStart=/usr/bin/tcp-split`
with site flags from `EnvironmentFile=-/etc/default/tcp-split` (read by
systemd, never by the daemon), and the sandbox:

```
DynamicUser=yes
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_INET AF_INET6
SocketBindDeny=any
TemporaryFileSystem=/:ro
BindReadOnlyPaths=/usr/bin/tcp-split
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
```

The process sees an empty read-only root containing only its own binary.
`SocketBindDeny=any` is allowed because every listening socket comes from
the socket unit; outbound `connect()` does not bind. The exact set must be
proven on the VPN server's systemd 257 before the package units are
finalised: a locally cross-built binary run as a **transient** `systemd-run`
unit with the same directives and a loopback `--listen`, as an
administrative user with sudo, leaving nothing on the host. Any directive that systemd 257 lacks or
that breaks startup is dropped with a note, not worked around.

The site enables and starts both units
(`systemctl enable --now tcp-split.socket tcp-split.service`). No unit
loads or removes the interception rules (below). While the proxy is down
(the socket unit stopped, or the service failed past its restart limit),
redirected connections reach a port with no listener and are reset; they do
not fall back to plain forwarding. Stopping only the service is brief: the
socket unit keeps the listeners and the next connection starts it again.

Changing the socket unit's addresses (a peer added or removed) restarts
`tcp-split.socket`, and through `Requires=` the service, which resets every
live split connection on every tunnel. Peers change rarely; this is accepted
and documented in the role README rather than solved with per-tunnel unit
instances.

### The interception rules (site-provided, permanent)

The rules are part of the site's permanent firewall configuration, loaded at
boot with the rest of it (for example included from `/etc/nftables.conf` or
from the VPN firewall's own file) and left in place whether or not the proxy
runs. The package ships only `examples/redirect.nft` to copy from. Its own
table, `inet tcp_split`, create/delete/create for idempotence, so it never
touches the VPN table or libvirt's tables:

- `prerouting`, `type nat hook prerouting priority dstnat - 10`:
  for `iifname $VPN_IFS`, `meta l4proto tcp`, `fib daddr type != local`,
  **`fib daddr oif $WAN_IF`**, `counter redirect to :$PORT`.
  The `fib daddr oif` match is what keeps the proxy inside the VPN's policy:
  a destination is redirected only if the kernel would route it out of the
  WAN interface. Anything that would be dropped by the VPN's forward chain
  (VMs, LANs, other tunnels) is never redirected, and on a home router the
  same match is what excludes destinations on its internal networks.
- `input`, `type filter hook input priority filter - 10`:
  `iifname != $VPN_IFS tcp dport $PORT counter drop`. Defence in depth
  behind the non-wildcard listeners.
- `forward`, `type filter hook forward priority filter - 10`, optional:
  `iifname $VPN_IFS udp dport 443 counter reject` so browsers fall back
  from QUIC to TCP. A role toggle, default on.

Everything else stays in the VPN table. The VPN table's `forward`
confinement, `input` guards and masquerade are unchanged.

### Deployment on the VPN server (the site's configuration management)

- The change lands in the site's configuration management, on top of whatever
  branch manages the server's WireGuard VPN.
- The site's VPN role gains an optional block, in its own task file,
  driven by:

  ```yaml
  vpn_tcp_split:
    enabled: true
    port: 10443
    reject_quic: true
    deny: []          # extra -deny prefixes beyond the role's own
  ```

  When enabled it: installs the keyring
  `/etc/apt/keyrings/go-tcp-split.gpg` from
  `https://mith.ro/go-tcp-split/go-tcp-split.gpg` and the source
  `deb [signed-by=/etc/apt/keyrings/go-tcp-split.gpg] https://mith.ro/go-tcp-split/<suite>/ ./`
  (the convention's one setup, with the host's own codename as the suite);
  installs `tcp-split`; templates
  the socket drop-in (one `ListenStream` per tunnel server address, both
  families); templates `/etc/default/tcp-split` with `-deny` for
  `10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 fc00::/7` and the server's own
  VM or LAN prefixes;
  adds the `inet tcp_split` table, from the same tunnel list and WAN
  interface the VPN table uses, to the VPN firewall's permanent nft
  configuration; and enables and starts the socket and service units.
  When disabled or absent it removes all of it, and deletes the live
  table (same shape as the role's existing teardown).
- Verify (`--tags verify`, read-only, no `failed_when: false`): the two
  units active and enabled; the live `inet tcp_split` table contains the
  `fib daddr oif "<wan>"` match, the input guard and every tunnel interface;
  `ss -ltn` shows the proxy listening on every tunnel address and nothing
  else; the service's `DynamicUser`, `CapabilityBoundingSet` and
  `TemporaryFileSystem` properties are as specified; the daemon's version
  matches the installed package.
- Rollout is non-disruptive: established connections keep their path, new
  ones are split. Disabling is one variable change and a run.

### Acceptance

The deliverable includes the deploy to the VPN server (the playbook run,
after `--check` is clean and the operator has approved it). Items 2 and 3
below need the laptop on the tunnel and are run by the operator with the
builder watching the server's logs; no loss-emulation benchmark harness is
built as a stand-in for them.

1. The netns integration tests (below) pass for IPv4 and IPv6 in CI.
2. On the VPN server, with the laptop on `wg-laptop` in full-tunnel mode: a
   download from a known-CUBIC server through the tunnel, measured
   alternately with the `inet tcp_split` table loaded and deleted, ideally
   when the path's loss is highest. The split run must be materially faster. The
   connection log on the server shows the flows with sane byte counts and
   outcomes.
3. From the laptop through the tunnel: SSH to an internet host survives an
   hour idle (keepalive, no idle timeout); a connection to a closed port on
   an internet host fails promptly (reset) rather than hanging; an IPv6
   destination is split (log line shows the v6 original destination).
4. From the tunnel, connections to the server's VMs and private ranges are still
   dropped by the VPN table and never appear in the proxy log.
5. `systemctl stop tcp-split.socket tcp-split.service` causes new redirected
   tunnel connections to be reset; `systemctl start tcp-split.socket
   tcp-split.service` restores service. (Stopping `tcp-split.service` alone
   is undone by the socket unit's activation on the next connection.)

### Testing

- Unit tests (`go test -race`): original-destination parsing from raw
  `sockaddr_in`/`sockaddr_in6` bytes; `LISTEN_FDS` handling; the safety
  checks; the relay's half-close semantics, reset-on-connect-failure, limit
  and idle behaviour using loopback listeners with an injected
  original-destination function.
- Integration tests: two network namespaces joined by a veth pair, with
  nftables `redirect` rules in the "server" namespace identical in shape to
  production, a test server bound on a non-local address in that namespace,
  and a client in the other. Both families. They must run in CI (with
  `sudo` on the GitHub runner, since Ubuntu restricts unprivileged user
  namespaces) and locally (`unshare -Urn` on Debian). Also: a connection to
  the proxy port that was not redirected is reset; a redirected connection
  to a `-deny` prefix is reset.
- `gofmt`, `go vet`, and the packaging version helper tests, as in the
  sibling repositories.

## Left to the builder

- Go package layout under `internal/`, exact flag spellings, log field
  names, and the test harness mechanics for the namespaces.
- Whether the per-connection log is emitted via `log` or `log/slog`.
- The nfpm metadata beyond what the sibling repos use.
- Which `internal/` package holds `Version` (the siblings differ:
  `internal/cli` and `internal/version`).
- How the site's deployment tooling fetches the keyring (`get_url` to
  `/etc/apt/keyrings/go-tcp-split.gpg`, mode 0644, is the obvious form).

## Open for the operator (answered → move to "decided")

- Name: repository `go-tcp-split`, binary and package `tcp-split`, nft
  table `tcp_split`. (Proposed.)
- Interception applies to **all** tunnels declared in the role, not only
  the laptop's. (Proposed; per-peer opt-out is a two-line change later.)
- `reject_quic` defaults to on. (Proposed.)
- Phase 2, a second host (such as the home router), is planned separately
  once the VPN server is proven. Items to check for that plan: the host's
  existing firewall layout and where the `inet tcp_split` table fits in it;
  any policy routing, packet marks or transparent proxies already on the
  host that the redirect could interact with; and its `tcp_wmem`. The
  `fib daddr oif <wan>` match is expected to be the whole of the
  internal-network exclusion.
