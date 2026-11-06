# tcp-split

`tcp-split` is a split-TCP proxy for lossy VPN clients. A laptop reaches its home network and a remote VPN server through WireGuard tunnels; over a long, lossy path the tunnel legs can see double-digit packet loss and several hundred milliseconds of round-trip time, and a forwarded TCP connection runs the far server's congestion control over that whole lossy path. `tcp-split` makes every forwarded TCP connection terminate on the VPN server (which runs BBR): the server intercepts the connection, opens its own connection to the real destination, and relays bytes both ways, so the lossy leg is always a server-side BBR socket and the clean leg runs over a path with no loss. It is a performance-enhancing proxy, not a privacy, circumvention or security feature.

## Install

From the signed apt repository at <https://mith.ro/go-tcp-split/>, on Debian
or Raspberry Pi OS. There is one per suite: `trixie`, `forky` and `sid`, and
`raspbian-trixie` and `raspbian-forky` for 32-bit Raspberry Pi OS (ARMv6). Put
your suite's name in place of `trixie` below:

```sh
sudo install -d -m0755 /etc/apt/keyrings
curl -fsSL https://mith.ro/go-tcp-split/go-tcp-split.gpg | sudo tee /etc/apt/keyrings/go-tcp-split.gpg > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/go-tcp-split.gpg] https://mith.ro/go-tcp-split/trixie/ ./" \
  | sudo tee /etc/apt/sources.list.d/go-tcp-split.list
sudo apt update
sudo apt install tcp-split
```

The repository's signing key is
`45D2 E2D9 4305 121B 96BF  171B A767 675E B2EF 21AF`
(`gpg --show-keys /etc/apt/keyrings/go-tcp-split.gpg` shows it).

## Status

First release in progress: packages publish to https://mith.ro/go-tcp-split/ on merge to main.

## Build

```
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/mithro/go-tcp-split/internal/cli.Version=$(VERSION)" -o dist/tcp-split ./cmd/tcp-split
```

Or `make build` (`VERSION` defaults to `dev`).

## Units

The package ships three systemd units (see `packaging/README.md`) and enables none of them.

`tcp-split.socket` owns the listening sockets. The site adds a drop-in with one `ListenStream=` per tunnel server address (both families); it never listens on a wildcard. It is the unit to enable, and it pulls in the redirect unit.

`tcp-split.service` is the sandboxed daemon, started by the socket. Its flags come from `TCP_SPLIT_OPTS` in `/etc/default/tcp-split`.

`tcp-split-redirect.service` loads the site's `/etc/tcp-split/redirect.nft`, which redirects tunnel TCP to the daemon's port in its own `inet tcp_split` table.

The fail-open chain: the redirect unit is `BindsTo=` the socket, so if the socket stops, the redirect table is deleted and traffic forwards as before.

## Usage

```
tcp-split [flags]
```

The daemon takes its listening sockets from systemd socket activation; `--listen` is for running it by hand.

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | none | address to listen on (repeatable) |
| `--deny` | none | CIDR destination to refuse (repeatable) |
| `--connect-timeout` | `10s` | upstream connect timeout |
| `--keepalive` | `60s` | TCP keepalive period |
| `--idle-timeout` | `0` | close connections idle this long (0 disables) |
| `--max-conns` | `4096` | maximum concurrent connections |
| `--congestion` | `""` | TCP congestion control algorithm (empty keeps the system default) |
| `--stats-interval` | `5m` | interval between stats log lines (0 disables) |
| `--drain` | `30s` | graceful shutdown drain time |
| `--verbose` | off | verbose logging |
| `--version` | | print version and exit |

Each connection logs one line with a `result=` field, one of:

- `ok`: relayed and closed normally.
- `connect-failed`: the upstream connection could not be made.
- `denied`: the destination matched a `--deny` prefix or is invalid or a loop.
- `not-redirected`: the connection was not an intercepted one (no original destination); it is reset.
- `limit`: refused because `--max-conns` was reached.
- `idle`: closed by `--idle-timeout`.
- `error`: the relay ended on an I/O error or reset.
