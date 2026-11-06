# tcp-split packaging

The Debian package installs the binary, three systemd units and examples. It
enables and starts nothing; the site does that.

## Units

- `tcp-split.socket` owns the listening sockets (`Accept=no`, `FreeBind=yes`
  because tunnel addresses may not exist yet at boot, `NoDelay=true`). It has
  no `ListenStream=`: the site supplies one per tunnel server address, both
  families, in a drop-in (`examples/10-listen.conf`). Listening only on tunnel
  server addresses, never a wildcard, is the first line of "unreachable from
  elsewhere". This is the unit that gets enabled; it `Wants=` the redirect unit.
- `tcp-split.service` runs `/usr/bin/tcp-split $TCP_SPLIT_OPTS`, with flags
  from `/etc/default/tcp-split` (`examples/default`; read by systemd, never by
  the daemon), inside a sandbox: dynamic user, no capabilities, IPv4/IPv6
  sockets only, an empty read-only root containing only its binary. It has no
  `[Install]` section; the socket starts it.
- `tcp-split-redirect.service` loads `/etc/tcp-split/redirect.nft` (a
  site-provided file; see `examples/redirect.nft`) into its own table,
  `inet tcp_split`. `ConditionPathExists=` skips it when the file is absent.

## Fail-open chain

`tcp-split-redirect.service` has `BindsTo=tcp-split.socket`. When the socket
unit stops, the redirect unit stops, `ExecStop=` deletes the `inet tcp_split`
table, and the kernel forwards traffic as before. Nothing is left redirecting
to a port no one listens on. The table is separate from the VPN's own, so
deleting it never touches other rules.

## Peer-change restart caveat

Changing the socket unit's addresses (a peer added or removed) restarts
`tcp-split.socket`. That stops the service, which resets every live split
connection on every tunnel, and drops the redirect table until the socket's
`Wants=tcp-split-redirect.service` brings it back. Peers change rarely; this
is accepted rather than solved with per-tunnel unit instances.

## Examples

Installed under `/usr/share/doc/tcp-split/examples/`:

- `10-listen.conf`: socket drop-in with a v4 and a v6 `ListenStream=`.
- `default`: an `/etc/default/tcp-split` denying the RFC 1918 and ULA ranges.
- `redirect.nft`: the interception table. Uncomment and edit its three
  `define` lines (`VPN_IFS`, `WAN_IF`, `PORT`) for the site; nft refuses to
  redefine a symbol, so they appear once.

`sh packaging/check-units.sh` (run by `make test`) checks the units with
`systemd-analyze verify` and the example with `nft -c`; the latter needs root
or unprivileged user namespaces.
