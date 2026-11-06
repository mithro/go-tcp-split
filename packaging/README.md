# tcp-split packaging

The Debian package installs the binary, two systemd units and examples. It
enables and starts nothing; the site does that, once, for both units:

```sh
systemctl enable --now tcp-split.socket tcp-split.service
```

The proxy then runs permanently. The nftables interception rules are not
managed by any unit: they are part of the site's permanent firewall
configuration (see `examples/redirect.nft` below).

## Units

- `tcp-split.socket` owns the listening sockets (`Accept=no`, `FreeBind=yes`
  because tunnel addresses may not exist yet at boot, `NoDelay=true`). It has
  no `ListenStream=`: the site supplies one per tunnel server address, both
  families, in a drop-in (`examples/10-listen.conf`). Listening only on tunnel
  server addresses, never a wildcard, is the first line of "unreachable from
  elsewhere". `FreeBind=yes` lets it bind the tunnel addresses before the
  tunnels exist, and because systemd passes the listeners to the daemon, the
  service's sandbox can keep `SocketBindDeny=any`. `[Install]
  WantedBy=sockets.target`.
- `tcp-split.service` runs `/usr/bin/tcp-split $TCP_SPLIT_OPTS`, with flags
  from `/etc/default/tcp-split` (`examples/default`; read by systemd, never by
  the daemon), inside a sandbox: dynamic user, no capabilities, IPv4/IPv6
  sockets only, an empty read-only root containing only its binary.
  `Requires=` and `After=tcp-split.socket`, `Restart=on-failure`, and
  `[Install] WantedBy=multi-user.target`: it starts at boot rather than
  waiting for a first connection.

## The interception rules are site configuration

`examples/redirect.nft` is an example of the `inet tcp_split` table to copy
into the site's persistent nftables configuration (for example included from
`/etc/nftables.conf`, or merged into the VPN firewall's own file). It is a
table of its own, so it never touches the VPN's rules. The rules stay loaded
whether or not the proxy is running.

There is no fail-open: while the proxy is down, redirected connections are
reset. The redirect still sends them to the proxy's port and nothing listens
there, so the kernel answers with a reset; tunnel TCP that the rules match
does not fall back to plain forwarding until the proxy is running again.
"Down" means the socket unit is stopped too (`systemctl stop tcp-split.socket`
stops both units, through the service's `Requires=`), or the service has
failed past its restart limit. Stopping only `tcp-split.service` does not take
the proxy down for long: the socket unit still holds the listeners, so
connections queue in its backlog and the next one starts the service again.
`systemctl start tcp-split.socket tcp-split.service` restores service. To stop
splitting on purpose, remove the table from the site's configuration (and
`nft delete table inet tcp_split` for the running system).

## Firewall: split traffic bypasses the forward chain

Split connections originate from the host itself. After the redirect, the
onward connection to the destination is opened by the `tcp-split` process, so
it leaves through the host's `output` path and never traverses the VPN table's
`forward` chain. Any per-destination, per-port or per-peer restriction that
the `forward` chain applies to tunnel traffic (for example `tcp dport 25
drop`, or a peer limited to some destinations) is therefore bypassed for every
split connection. `tcp-split` can reach every destination routed out of
`WAN_IF` except the `--deny` prefixes. Such restrictions must instead be
expressed in the redirect rule's match in `redirect.nft` (`fib daddr oif
$WAN_IF`, plus the `input` guard; traffic the redirect does not match is
forwarded and still meets the `forward` chain) and in `--deny`, never only in
the `forward` chain. Check the VPN's `forward` chain against this before
deploying.

## Peer-change restart caveat

Changing the socket unit's addresses (a peer added or removed) restarts
`tcp-split.socket`, and through `Requires=` the service with it, which resets
every live split connection on every tunnel; new redirected connections are
reset until the listeners are back. Peers change rarely; this is accepted
rather than solved with per-tunnel unit instances.

## Examples

Installed under `/usr/share/doc/tcp-split/examples/`:

- `10-listen.conf`: socket drop-in with a v4 and a v6 `ListenStream=`.
- `default`: an `/etc/default/tcp-split` denying the RFC 1918 and ULA ranges.
- `redirect.nft`: the interception table, to copy into the site's permanent
  nftables configuration. Uncomment and edit its three `define` lines
  (`VPN_IFS`, `WAN_IF`, `PORT`) for the site; nft refuses to redefine a
  symbol, so they appear once.

`sh packaging/check-units.sh` (run by `make test`) checks the units with
`systemd-analyze verify` and the example with `nft -c`; the latter needs root
or unprivileged user namespaces.
